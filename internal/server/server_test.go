package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/scubadam/homebox-ai-inventory/internal/config"
	"github.com/scubadam/homebox-ai-inventory/internal/homebox"
	"github.com/scubadam/homebox-ai-inventory/internal/providers"
)

type mockVisionProvider struct {
	result  *providers.RawVisionResult
	err     error
	lastReq providers.AnalyzeRequest
}

func (m *mockVisionProvider) Name() string { return "mock-vision" }
func (m *mockVisionProvider) Analyze(_ context.Context, req providers.AnalyzeRequest) (*providers.RawVisionResult, error) {
	m.lastReq = req
	return m.result, m.err
}

func makeTestJPEG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, nil)
	return buf.Bytes()
}

func TestServerEndToEndAnalyzeAndCommitFlows(t *testing.T) {
	// Mock Homebox API server
	createdEntities := []map[string]any{}
	updatedEntities := []string{}
	deletedEntities := []string{}
	uploadedAttachments := []string{}

	hbMux := http.NewServeMux()
	hbMux.HandleFunc("/api/v1/entity-types", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]homebox.EntityType{
			{ID: "loc-box", Name: "Storage Box", IsLocation: true},
			{ID: "type-cables", Name: "Cables", IsLocation: false},
			{ID: "type-misc", Name: "Misc", IsLocation: false},
		})
	})
	hbMux.HandleFunc("/api/v1/entities/tree", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "loc-1", "name": "Loft", "type": "location", "children": []any{}},
		})
	})
	hbMux.HandleFunc("/api/v1/entities/box-existing", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			updatedEntities = append(updatedEntities, "box-existing")
			w.WriteHeader(http.StatusOK)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "box-existing",
			"attachments": []any{},
		})
	})
	hbMux.HandleFunc("/api/v1/entities", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.RawQuery, "parentIds=box-existing") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{
						"id":          "item-existing-1",
						"name":        "HDMI Cable",
						"quantity":    1,
						"description": "1.5m",
						"entityType":  map[string]string{"id": "type-cables", "name": "Cables"},
					},
					{
						"id":          "item-missing-2",
						"name":        "Old Adapter",
						"quantity":    1,
						"description": "VGA",
						"entityType":  map[string]string{"id": "type-misc", "name": "Misc"},
					},
				},
			})
			return
		}
		if r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			createdEntities = append(createdEntities, body)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "new-ent-id"})
			return
		}
	})
	hbMux.HandleFunc("/api/v1/entities/item-existing-1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			updatedEntities = append(updatedEntities, "item-existing-1")
			w.WriteHeader(http.StatusOK)
		}
	})
	hbMux.HandleFunc("/api/v1/entities/item-missing-2", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletedEntities = append(deletedEntities, "item-missing-2")
			w.WriteHeader(http.StatusNoContent)
		}
	})
	hbMux.HandleFunc("/api/v1/entities/box-existing/attachments", func(w http.ResponseWriter, r *http.Request) {
		uploadedAttachments = append(uploadedAttachments, "box-existing")
		w.WriteHeader(http.StatusCreated)
	})

	hbServer := httptest.NewServer(hbMux)
	defer hbServer.Close()

	cfg := &config.Config{
		Port:              "8000",
		HomeboxURL:        hbServer.URL,
		HomeboxPublicURL:  "https://homebox.example.com",
		HomeboxAPIKey:     "hb-key",
		AIModel:           "gemini-2.5-flash",
		MaxImageDimension: 1600,
		JPEGQuality:       85,
	}
	hbClient := homebox.NewClient(cfg.HomeboxURL, cfg.HomeboxPublicURL, cfg.HomeboxAPIKey, "", "")

	matchedID := "item-existing-1"
	mockAI := &mockVisionProvider{
		result: &providers.RawVisionResult{
			ContainerName:            "Updated Cable Box",
			RecommendedNewCategories: []string{"Audio Gear", "Cables"}, // "Cables" already exists, should be filtered out!
			Items: []providers.RawDetectedItem{
				{
					Name:                     "HDMI Cable",
					Quantity:                 2,
					Description:              "Updated to 2 cables",
					Category:                 "Cables",
					FallbackExistingCategory: "Cables",
					Status:                   "matched",
					ExistingID:               &matchedID,
					PhotoIndices:             []int{0},
				},
				{
					Name:                     "XLR Microphone Cable",
					Quantity:                 1,
					Description:              "3m balanced",
					Category:                 "Audio Gear", // doesn't exist yet -> should fallback to type-cables
					FallbackExistingCategory: "Cables",
					Status:                   "new",
					PhotoIndices:             []int{0},
				},
			},
		},
	}

	appServer := New(cfg, hbClient, mockAI)
	ts := httptest.NewServer(appServer.Handler())
	defer ts.Close()

	// 1. Test /api/health
	resHealth, err := http.Get(ts.URL + "/api/health")
	if err != nil || resHealth.StatusCode != http.StatusOK {
		t.Fatalf("health check failed: %v", err)
	}

	// 2. Test /api/bootstrap
	resBoot, err := http.Get(ts.URL + "/api/bootstrap")
	if err != nil || resBoot.StatusCode != http.StatusOK {
		t.Fatalf("bootstrap failed: %v", err)
	}

	// 3. Test /api/analyze in "update" mode (differential container audit)
	var bodyBuf bytes.Buffer
	mw := multipart.NewWriter(&bodyBuf)
	_ = mw.WriteField("mode", "update")
	_ = mw.WriteField("container_id", "box-existing")
	fw, _ := mw.CreateFormFile("files", "box_angle1.jpg")
	_, _ = fw.Write(makeTestJPEG())
	_ = mw.Close()

	resAnalyze, err := http.Post(ts.URL+"/api/analyze", mw.FormDataContentType(), &bodyBuf)
	if err != nil || resAnalyze.StatusCode != http.StatusOK {
		t.Fatalf("analyze failed: status=%v, err=%v", resAnalyze.StatusCode, err)
	}

	var analyzeData struct {
		SessionID             string          `json:"session_id"`
		ContainerName         string          `json:"container_name"`
		RecommendedCategories []string        `json:"recommended_categories"`
		Items                 []ProcessedItem `json:"items"`
		MissingItems          []ProcessedItem `json:"missing_items"`
	}
	if err := json.NewDecoder(resAnalyze.Body).Decode(&analyzeData); err != nil {
		t.Fatalf("failed to decode analyze response: %v", err)
	}
	resAnalyze.Body.Close()

	// Verify "Cables" was filtered from RecommendedCategories while "Audio Gear" remained
	if len(analyzeData.RecommendedCategories) != 1 || analyzeData.RecommendedCategories[0] != "Audio Gear" {
		t.Errorf("expected RecommendedCategories=['Audio Gear'], got %v", analyzeData.RecommendedCategories)
	}
	// Verify "Audio Gear" item fell back to "type-cables"
	if len(analyzeData.Items) != 2 || analyzeData.Items[1].EntityTypeID != "type-cables" {
		t.Errorf("expected fallback entityTypeId='type-cables', got %+v", analyzeData.Items)
	}
	// Verify "item-missing-2" ("Old Adapter") was detected as missing!
	if len(analyzeData.MissingItems) != 1 || analyzeData.MissingItems[0].Name != "Old Adapter" {
		t.Errorf("expected 1 missing item 'Old Adapter', got %+v", analyzeData.MissingItems)
	}

	// 4. Test /api/commit (updating matched item, creating new item, deleting missing item, uploading photo)
	containerID := "box-existing"
	missingID := "item-missing-2"
	primaryRef := "new:0"
	commitReq := CommitPayload{
		SessionID:       analyzeData.SessionID,
		Mode:            "update",
		ContainerID:     &containerID,
		ContainerName:   "Updated Cable Box",
		PrimaryPhotoRef: &primaryRef,
		Items: []CommitItem{
			{
				Name:         "HDMI Cable",
				Quantity:     2,
				EntityTypeID: "type-cables",
				Status:       "matched",
				ExistingID:   &matchedID,
				Action:       "save",
			},
			{
				Name:         "XLR Microphone Cable",
				Quantity:     1,
				EntityTypeID: "type-cables",
				Status:       "new",
				Action:       "save",
			},
			{
				Name:         "Old Adapter",
				Quantity:     1,
				EntityTypeID: "type-misc",
				Status:       "missing",
				ExistingID:   &missingID,
				Action:       "delete",
			},
		},
	}
	commitBytes, _ := json.Marshal(commitReq)
	resCommit, err := http.Post(ts.URL+"/api/commit", "application/json", bytes.NewReader(commitBytes))
	if err != nil || resCommit.StatusCode != http.StatusOK {
		t.Fatalf("commit failed: status=%v, err=%v", resCommit.StatusCode, err)
	}

	var commitResp map[string]any
	_ = json.NewDecoder(resCommit.Body).Decode(&commitResp)
	resCommit.Body.Close()

	if commitResp["created_count"] != float64(1) || commitResp["updated_count"] != float64(1) || commitResp["deleted_count"] != float64(1) {
		t.Errorf("unexpected commit counts: %+v", commitResp)
	}
	if len(uploadedAttachments) != 1 {
		t.Errorf("expected 1 uploaded attachment, got %d", len(uploadedAttachments))
	}
}

func TestServerBasicAuth(t *testing.T) {
	cfg := &config.Config{
		AuthUsername: "admin",
		AuthPassword: "secretpassword123",
		AuthEnabled:  true,
	}
	srv := New(cfg, nil, &mockVisionProvider{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 1. /api/health should always bypass auth
	resHealth, err := http.Get(ts.URL + "/api/health")
	if err != nil || resHealth.StatusCode != http.StatusOK {
		t.Fatalf("expected /api/health to succeed without auth, got %v (err: %v)", resHealth.StatusCode, err)
	}

	// 2. Unauthenticated request to protected route should return 401
	resIndex, err := http.Get(ts.URL + "/")
	if err != nil || resIndex.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected / to return 401 without auth, got %v (err: %v)", resIndex.StatusCode, err)
	}
	if authHeader := resIndex.Header.Get("WWW-Authenticate"); !strings.Contains(authHeader, "Basic") {
		t.Errorf("expected WWW-Authenticate header with Basic realm, got %q", authHeader)
	}

	// 3. Request with invalid credentials should return 401
	reqBad, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	reqBad.SetBasicAuth("admin", "wrongpassword")
	resBad, err := http.DefaultClient.Do(reqBad)
	if err != nil || resBad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected / to return 401 with bad auth, got %v", resBad.StatusCode)
	}

	// 4. Request with valid credentials should succeed (200)
	reqGood, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	reqGood.SetBasicAuth("admin", "secretpassword123")
	resGood, err := http.DefaultClient.Do(reqGood)
	if err != nil || resGood.StatusCode != http.StatusOK {
		t.Fatalf("expected / to return 200 with valid auth, got %v", resGood.StatusCode)
	}
}

func TestServerItemCropsAndExteriorCoverFlow(t *testing.T) {
	uploadedByEntity := map[string]int{}
	createdIdx := 0

	hbMux := http.NewServeMux()
	hbMux.HandleFunc("/api/v1/entity-types", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]homebox.EntityType{
			{ID: "loc-box", Name: "Box", IsLocation: true},
			{ID: "type-tools", Name: "Tools", IsLocation: false},
		})
	})
	hbMux.HandleFunc("/api/v1/entities", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			createdIdx++
			id := fmt.Sprintf("ent-%d", createdIdx)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id})
		}
	})
	hbMux.HandleFunc("/api/v1/entities/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/attachments") && r.Method == http.MethodPost {
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/entities/"), "/")
			uploadedByEntity[parts[0]]++
			w.WriteHeader(http.StatusCreated)
			return
		}
		http.NotFound(w, r)
	})

	hbServer := httptest.NewServer(hbMux)
	defer hbServer.Close()

	cfg := &config.Config{
		Port:              "8000",
		HomeboxURL:        hbServer.URL,
		HomeboxPublicURL:  "https://homebox.example.com",
		HomeboxAPIKey:     "hb-key",
		MaxImageDimension: 1600,
		JPEGQuality:       85,
	}
	hbClient := homebox.NewClient(cfg.HomeboxURL, cfg.HomeboxPublicURL, cfg.HomeboxAPIKey, "", "")

	coverIdx := 0
	cropIdx := 1
	mockAI := &mockVisionProvider{
		result: &providers.RawVisionResult{
			ContainerName:        "Garage Tool Drawer",
			CoverPhotoIndex:      &coverIdx,
			ExteriorPhotoIndices: []int{0},
			Items: []providers.RawDetectedItem{
				{
					Name:                     "Screwdriver",
					Quantity:                 1,
					Description:              "Phillips #2",
					Category:                 "Tools",
					FallbackExistingCategory: "Tools",
					Status:                   "new",
					PhotoIndices:             []int{1},
					CropPhotoIndex:           &cropIdx,
					Box2D:                    []int{100, 100, 600, 600},
				},
				{
					Name:                     "Loose Screws",
					Quantity:                 15,
					Description:              "Assorted",
					Category:                 "Tools",
					FallbackExistingCategory: "Tools",
					Status:                   "new",
					PhotoIndices:             []int{1},
					Box2D:                    nil, // verify graceful fallback when no bounding box
				},
			},
		},
	}

	appServer := New(cfg, hbClient, mockAI)
	ts := httptest.NewServer(appServer.Handler())
	defer ts.Close()

	var bodyBuf bytes.Buffer
	mw := multipart.NewWriter(&bodyBuf)
	_ = mw.WriteField("mode", "new")
	_ = mw.WriteField("isolate_items", "true")
	f1, _ := mw.CreateFormFile("files", "exterior.jpg")
	_, _ = f1.Write(makeTestJPEG())
	f2, _ := mw.CreateFormFile("files", "interior.jpg")
	_, _ = f2.Write(makeTestJPEG())
	_ = mw.Close()

	resAnalyze, err := http.Post(ts.URL+"/api/analyze", mw.FormDataContentType(), &bodyBuf)
	if err != nil || resAnalyze.StatusCode != http.StatusOK {
		t.Fatalf("analyze failed: err=%v status=%v", err, resAnalyze.StatusCode)
	}
	var analyzeData struct {
		SessionID            string          `json:"session_id"`
		CoverPhotoIndex      *int            `json:"cover_photo_index"`
		ExteriorPhotoIndices []int           `json:"exterior_photo_indices"`
		Items                []ProcessedItem `json:"items"`
	}
	if err := json.NewDecoder(resAnalyze.Body).Decode(&analyzeData); err != nil {
		t.Fatalf("failed to decode analyze response: %v", err)
	}
	resAnalyze.Body.Close()

	if !mockAI.lastReq.IsolateItemPhotos {
		t.Errorf("expected AnalyzeRequest.IsolateItemPhotos=true")
	}
	if analyzeData.CoverPhotoIndex == nil || *analyzeData.CoverPhotoIndex != 0 {
		t.Errorf("expected cover_photo_index=0, got %+v", analyzeData.CoverPhotoIndex)
	}
	if len(analyzeData.ExteriorPhotoIndices) != 1 || analyzeData.ExteriorPhotoIndices[0] != 0 {
		t.Errorf("expected exterior_photo_indices=[0], got %v", analyzeData.ExteriorPhotoIndices)
	}
	if len(analyzeData.Items) != 2 || analyzeData.Items[0].CropID == "" || analyzeData.Items[0].CropURL == "" {
		t.Fatalf("expected crop_id and crop_url on first item, got %+v", analyzeData.Items)
	}
	if analyzeData.Items[1].CropID != "" {
		t.Errorf("expected empty crop_id on second item without box_2d, got %q", analyzeData.Items[1].CropID)
	}

	// Verify /api/session-crop serves the cropped JPEG preview
	resCrop, err := http.Get(ts.URL + analyzeData.Items[0].CropURL)
	if err != nil || resCrop.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from crop URL, got err=%v status=%v", err, resCrop.StatusCode)
	}
	if ct := resCrop.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("expected image/jpeg from crop endpoint, got %q", ct)
	}
	resCrop.Body.Close()

	// Commit with attach_crop=true on item 0
	primaryRef := "new:0"
	commitReq := CommitPayload{
		SessionID:       analyzeData.SessionID,
		Mode:            "new",
		ContainerName:   "Garage Tool Drawer",
		PrimaryPhotoRef: &primaryRef,
		Items: []CommitItem{
			{
				Name:         "Screwdriver",
				Quantity:     1,
				EntityTypeID: "type-tools",
				Status:       "new",
				Action:       "save",
				CropID:       analyzeData.Items[0].CropID,
				AttachCrop:   true,
			},
			{
				Name:         "Loose Screws",
				Quantity:     15,
				EntityTypeID: "type-tools",
				Status:       "new",
				Action:       "save",
			},
		},
	}
	commitBytes, _ := json.Marshal(commitReq)
	resCommit, err := http.Post(ts.URL+"/api/commit", "application/json", bytes.NewReader(commitBytes))
	if err != nil || resCommit.StatusCode != http.StatusOK {
		t.Fatalf("commit failed: err=%v status=%v", err, resCommit.StatusCode)
	}
	resCommit.Body.Close()

	// ent-1 is the container (2 uploaded photos), ent-2 is Screwdriver (1 cropped item photo), ent-3 is Loose Screws (0)
	if uploadedByEntity["ent-1"] != 2 {
		t.Errorf("expected 2 photos uploaded to container ent-1, got %d", uploadedByEntity["ent-1"])
	}
	if uploadedByEntity["ent-2"] != 1 {
		t.Errorf("expected 1 cropped photo uploaded to item ent-2, got %d", uploadedByEntity["ent-2"])
	}
	if uploadedByEntity["ent-3"] != 0 {
		t.Errorf("expected 0 photos uploaded to item ent-3, got %d", uploadedByEntity["ent-3"])
	}
}
