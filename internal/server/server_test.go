package server

import (
	"bytes"
	"context"
	"encoding/json"
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
	result *providers.RawVisionResult
	err    error
}

func (m *mockVisionProvider) Name() string { return "mock-vision" }
func (m *mockVisionProvider) Analyze(_ context.Context, _ providers.AnalyzeRequest) (*providers.RawVisionResult, error) {
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
		HomeboxPublicURL:  "https://homebox.homelab",
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
