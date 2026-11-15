//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/scubadam/homebox-ai-inventory/internal/config"
	"github.com/scubadam/homebox-ai-inventory/internal/homebox"
	"github.com/scubadam/homebox-ai-inventory/internal/providers"
	"github.com/scubadam/homebox-ai-inventory/internal/server"
)

type deterministicVisionProvider struct {
	lastReq *providers.AnalyzeRequest
	result  *providers.RawVisionResult
}

func (d *deterministicVisionProvider) Name() string { return "deterministic-mock" }
func (d *deterministicVisionProvider) Analyze(_ context.Context, req providers.AnalyzeRequest) (*providers.RawVisionResult, error) {
	d.lastReq = &req
	if d.result != nil {
		return d.result, nil
	}
	return &providers.RawVisionResult{
		ContainerName:            "Live Test Bin",
		RecommendedNewCategories: []string{"Precision Instruments"},
		Items: []providers.RawDetectedItem{
			{
				Name:                     "Digital Caliper 150mm",
				Quantity:                 2,
				Description:              "Stainless steel electronic vernier",
				Category:                 "Precision Instruments",
				FallbackExistingCategory: "Tools",
				Status:                   "new",
				PhotoIndices:             []int{0},
			},
		},
	}, nil
}

func setupLiveServerForTest(t *testing.T, provider providers.VisionProvider) (*homebox.Client, *httptest.Server, int64) {
	t.Helper()
	baseURL := getTestHomeboxURL()

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(baseURL + "/manifest.json")
	if err != nil {
		resp, err = client.Get(baseURL + "/")
	}
	if err != nil {
		t.Skipf("Live Homebox instance not reachable at %s, skipping: %v", baseURL, err)
	}
	if resp != nil {
		resp.Body.Close()
	}

	uniqueID := time.Now().UnixNano()
	testEmail := fmt.Sprintf("e2e-%d@example.com", uniqueID)
	testPassword := "E2ePassword123!"

	regPayload := map[string]string{
		"email":    testEmail,
		"name":     "E2E Test Runner",
		"password": testPassword,
	}
	regBody, _ := json.Marshal(regPayload)
	_, _ = client.Post(baseURL+"/api/v1/users/register", "application/json", bytes.NewReader(regBody))

	cfg := &config.Config{
		Port:                "8000",
		HomeboxURL:          baseURL,
		HomeboxPublicURL:    baseURL,
		HomeboxUsername:     testEmail,
		HomeboxPassword:     testPassword,
		AIProvider:          provider.Name(),
		AIModel:             "mock",
		MaxImageDimension:   1600,
		JPEGQuality:         85,
		AllowClientSettings: true,
	}
	hbClient := homebox.NewClient(cfg.HomeboxURL, cfg.HomeboxPublicURL, "", cfg.HomeboxUsername, cfg.HomeboxPassword)
	srv := server.New(cfg, hbClient, provider)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return hbClient, ts, uniqueID
}

func TestServerE2EAgainstLiveHomebox(t *testing.T) {
	hbClient, ts, uniqueID := setupLiveServerForTest(t, &deterministicVisionProvider{})

	// 1. Check Bootstrap
	resBoot, err := http.Get(ts.URL + "/api/bootstrap")
	if err != nil || resBoot.StatusCode != http.StatusOK {
		t.Fatalf("bootstrap failed: %v", err)
	}
	var bootData struct {
		Locations []any `json:"locations"`
		ItemTypes []any `json:"item_types"`
	}
	_ = json.NewDecoder(resBoot.Body).Decode(&bootData)
	resBoot.Body.Close()
	t.Logf("Bootstrap returned %d locations, %d item types", len(bootData.Locations), len(bootData.ItemTypes))

	// 2. Create Entity Type via Server
	typeReq := map[string]any{"name": fmt.Sprintf("Live Type %d", uniqueID), "isLocation": false}
	typeBody, _ := json.Marshal(typeReq)
	resType, err := http.Post(ts.URL+"/api/entity-types", "application/json", bytes.NewReader(typeBody))
	if err != nil || resType.StatusCode != http.StatusOK {
		t.Fatalf("create entity type failed: %v", err)
	}
	var createdType homebox.EntityType
	_ = json.NewDecoder(resType.Body).Decode(&createdType)
	resType.Body.Close()
	t.Logf("Created entity type via server: ID=%s, Name=%s", createdType.ID, createdType.Name)

	// 3. Analyze Photo via Server
	var formBuf bytes.Buffer
	mw := multipart.NewWriter(&formBuf)
	_ = mw.WriteField("mode", "new")
	fw, _ := mw.CreateFormFile("files", "caliper.jpg")
	_, _ = fw.Write(makeTestJPEG())
	_ = mw.Close()

	resAnalyze, err := http.Post(ts.URL+"/api/analyze", mw.FormDataContentType(), &formBuf)
	if err != nil || resAnalyze.StatusCode != http.StatusOK {
		t.Fatalf("analyze failed: %v", err)
	}
	var analyzeResult struct {
		SessionID             string                 `json:"session_id"`
		ContainerName         string                 `json:"container_name"`
		RecommendedCategories []string               `json:"recommended_categories"`
		Items                 []server.ProcessedItem `json:"items"`
	}
	_ = json.NewDecoder(resAnalyze.Body).Decode(&analyzeResult)
	resAnalyze.Body.Close()

	if analyzeResult.SessionID == "" || len(analyzeResult.Items) == 0 {
		t.Fatalf("invalid analyze result: %+v", analyzeResult)
	}
	t.Logf("Analyze succeeded: session_id=%s, items=%d", analyzeResult.SessionID, len(analyzeResult.Items))

	// 4. Commit to Real Homebox via Server
	primaryRef := "new:0"
	commitPayload := server.CommitPayload{
		SessionID:       analyzeResult.SessionID,
		Mode:            "new",
		ContainerName:   analyzeResult.ContainerName,
		PrimaryPhotoRef: &primaryRef,
		Items: []server.CommitItem{
			{
				Name:         analyzeResult.Items[0].Name,
				Quantity:     analyzeResult.Items[0].Quantity,
				Description:  analyzeResult.Items[0].Description,
				EntityTypeID: createdType.ID,
				Status:       "new",
				Action:       "save",
				PhotoIndices: []int{0},
			},
		},
	}
	commitBytes, _ := json.Marshal(commitPayload)
	resCommit, err := http.Post(ts.URL+"/api/commit", "application/json", bytes.NewReader(commitBytes))
	if err != nil || resCommit.StatusCode != http.StatusOK {
		t.Fatalf("commit failed: %v", err)
	}
	var commitResult struct {
		Success             bool   `json:"success"`
		ContainerID         string `json:"container_id"`
		CreatedCount        int    `json:"created_count"`
		UploadedPhotosCount int    `json:"uploaded_photos_count"`
		ViewURL             string `json:"view_url"`
	}
	_ = json.NewDecoder(resCommit.Body).Decode(&commitResult)
	resCommit.Body.Close()

	if !commitResult.Success || commitResult.CreatedCount != 1 || commitResult.UploadedPhotosCount != 1 {
		t.Fatalf("unexpected commit result: %+v", commitResult)
	}
	t.Logf("Commit succeeded: container_id=%s, view_url=%s", commitResult.ContainerID, commitResult.ViewURL)

	// 5. Query Container Items & Attachments from Real Homebox via Server
	resItems, err := http.Get(fmt.Sprintf("%s/api/container-items/%s", ts.URL, commitResult.ContainerID))
	if err != nil || resItems.StatusCode != http.StatusOK {
		t.Fatalf("get container items failed: %v", err)
	}
	var details homebox.ContainerDetails
	_ = json.NewDecoder(resItems.Body).Decode(&details)
	resItems.Body.Close()

	if len(details.Items) != 1 || len(details.Attachments) != 1 {
		t.Fatalf("unexpected container details: %+v", details)
	}
	t.Logf("Verified live Homebox items=%d, attachments=%d", len(details.Items), len(details.Attachments))

	// 6. Test Attachment Proxy Streaming
	attURL := ts.URL + details.Attachments[0].URL
	resAtt, err := http.Get(attURL)
	if err != nil || resAtt.StatusCode != http.StatusOK {
		t.Fatalf("attachment proxy failed (%s): %v", attURL, err)
	}
	resAtt.Body.Close()
	t.Logf("Attachment proxy successfully streamed thumbnail (%s)", resAtt.Header.Get("Content-Type"))

	// Cleanup container
	_ = hbClient.DeleteEntity(context.Background(), commitResult.ContainerID)
	t.Logf("Successfully verified full server E2E pipeline against live Homebox!")
}

func TestServerItemCropsAndExteriorCoverAgainstLiveHomebox(t *testing.T) {
	coverIdx := 1
	cropIdx := 0
	mockProvider := &deterministicVisionProvider{
		result: &providers.RawVisionResult{
			ContainerName:        "Live Crop & Cover Bin",
			CoverPhotoIndex:      &coverIdx,
			ExteriorPhotoIndices: []int{1},
			Items: []providers.RawDetectedItem{
				{
					Name:           "Precision Screwdriver",
					Quantity:       1,
					Description:    "Isolated item crop attached",
					Status:         "new",
					PhotoIndices:   []int{0},
					CropPhotoIndex: &cropIdx,
					Box2D:          []int{100, 100, 500, 500},
				},
				{
					Name:           "Brass Feeler Gauge",
					Quantity:       1,
					Description:    "Crop skipped by user",
					Status:         "new",
					PhotoIndices:   []int{0},
					CropPhotoIndex: &cropIdx,
					Box2D:          []int{450, 450, 900, 900},
				},
			},
		},
	}

	hbClient, ts, _ := setupLiveServerForTest(t, mockProvider)

	// 1. Analyze two photos with isolate_items=true
	var formBuf bytes.Buffer
	mw := multipart.NewWriter(&formBuf)
	_ = mw.WriteField("mode", "new")
	_ = mw.WriteField("isolate_items", "true")
	fw0, _ := mw.CreateFormFile("files", "interior.jpg")
	_, _ = fw0.Write(makeTestJPEG())
	fw1, _ := mw.CreateFormFile("files", "exterior.jpg")
	_, _ = fw1.Write(makeTestJPEG())
	_ = mw.Close()

	resAnalyze, err := http.Post(ts.URL+"/api/analyze", mw.FormDataContentType(), &formBuf)
	if err != nil || resAnalyze.StatusCode != http.StatusOK {
		t.Fatalf("analyze failed: %v", err)
	}
	var analyzeResult struct {
		SessionID            string                 `json:"session_id"`
		ContainerName        string                 `json:"container_name"`
		CoverPhotoIndex      *int                   `json:"cover_photo_index"`
		ExteriorPhotoIndices []int                  `json:"exterior_photo_indices"`
		Items                []server.ProcessedItem `json:"items"`
	}
	_ = json.NewDecoder(resAnalyze.Body).Decode(&analyzeResult)
	resAnalyze.Body.Close()

	if mockProvider.lastReq == nil || !mockProvider.lastReq.IsolateItemPhotos {
		t.Fatalf("expected IsolateItemPhotos=true forwarded to provider, got %+v", mockProvider.lastReq)
	}
	if analyzeResult.CoverPhotoIndex == nil || *analyzeResult.CoverPhotoIndex != 1 {
		t.Fatalf("expected cover_photo_index=1, got %v", analyzeResult.CoverPhotoIndex)
	}
	if len(analyzeResult.ExteriorPhotoIndices) != 1 || analyzeResult.ExteriorPhotoIndices[0] != 1 {
		t.Fatalf("expected exterior_photo_indices=[1], got %v", analyzeResult.ExteriorPhotoIndices)
	}
	if len(analyzeResult.Items) != 2 || analyzeResult.Items[0].CropURL == "" || analyzeResult.Items[1].CropURL == "" {
		t.Fatalf("expected crop URLs on both analyzed items, got %+v", analyzeResult.Items)
	}

	// 2. Verify session crop preview endpoint serves a valid JPEG
	resCrop, err := http.Get(ts.URL + analyzeResult.Items[0].CropURL)
	if err != nil || resCrop.StatusCode != http.StatusOK {
		t.Fatalf("crop preview fetch failed (%s): %v", analyzeResult.Items[0].CropURL, err)
	}
	cropBytes, _ := io.ReadAll(resCrop.Body)
	resCrop.Body.Close()
	if ct := resCrop.Header.Get("Content-Type"); ct != "image/jpeg" || len(cropBytes) < 100 {
		t.Fatalf("unexpected crop preview response: content-type=%q bytes=%d", ct, len(cropBytes))
	}

	// 3. Commit with exterior photo 1 as primary cover, attaching only item 0's crop
	primaryRef := fmt.Sprintf("new:%d", *analyzeResult.CoverPhotoIndex)
	commitPayload := server.CommitPayload{
		SessionID:       analyzeResult.SessionID,
		Mode:            "new",
		ContainerName:   analyzeResult.ContainerName,
		PrimaryPhotoRef: &primaryRef,
		Items: []server.CommitItem{
			{
				Name:         analyzeResult.Items[0].Name,
				Quantity:     analyzeResult.Items[0].Quantity,
				Description:  analyzeResult.Items[0].Description,
				Status:       "new",
				Action:       "save",
				PhotoIndices: []int{0},
				CropID:       analyzeResult.Items[0].CropID,
				AttachCrop:   true,
			},
			{
				Name:         analyzeResult.Items[1].Name,
				Quantity:     analyzeResult.Items[1].Quantity,
				Description:  analyzeResult.Items[1].Description,
				Status:       "new",
				Action:       "save",
				PhotoIndices: []int{0},
				CropID:       analyzeResult.Items[1].CropID,
				AttachCrop:   false,
			},
		},
	}
	commitBytes, _ := json.Marshal(commitPayload)
	resCommit, err := http.Post(ts.URL+"/api/commit", "application/json", bytes.NewReader(commitBytes))
	if err != nil || resCommit.StatusCode != http.StatusOK {
		t.Fatalf("commit failed: %v", err)
	}
	var commitResult struct {
		Success             bool   `json:"success"`
		ContainerID         string `json:"container_id"`
		CreatedCount        int    `json:"created_count"`
		UploadedPhotosCount int    `json:"uploaded_photos_count"`
	}
	_ = json.NewDecoder(resCommit.Body).Decode(&commitResult)
	resCommit.Body.Close()

	if !commitResult.Success || commitResult.CreatedCount != 2 || commitResult.UploadedPhotosCount != 3 {
		t.Fatalf("unexpected commit result: %+v", commitResult)
	}
	t.Cleanup(func() {
		_ = hbClient.DeleteEntity(context.Background(), commitResult.ContainerID)
	})

	// 4. Verify container photos (primary cover = photo_1.jpg) and item crop attachments on real Homebox
	details, err := hbClient.GetContainerDetails(context.Background(), commitResult.ContainerID)
	if err != nil {
		t.Fatalf("GetContainerDetails failed: %v", err)
	}
	if len(details.Items) != 2 || len(details.Attachments) != 2 {
		t.Fatalf("expected 2 items and 2 container attachments, got items=%d attachments=%d", len(details.Items), len(details.Attachments))
	}

	var primaryTitle string
	for _, att := range details.Attachments {
		if att.Primary {
			primaryTitle = att.Title
		}
	}
	if primaryTitle != "exterior.jpg" {
		t.Fatalf("expected primary container attachment to be exterior.jpg, got %q (attachments=%+v)", primaryTitle, details.Attachments)
	}

	itemByName := map[string]homebox.ContainerItem{}
	for _, item := range details.Items {
		itemByName[item.Name] = item
	}
	screwdriver := itemByName["Precision Screwdriver"]
	feeler := itemByName["Brass Feeler Gauge"]
	if screwdriver.ID == "" || feeler.ID == "" {
		t.Fatalf("missing created items on Homebox: %+v", details.Items)
	}

	item0Atts, err := hbClient.GetEntityAttachments(context.Background(), screwdriver.ID)
	if err != nil {
		t.Fatalf("GetEntityAttachments(%s) failed: %v", screwdriver.ID, err)
	}
	if len(item0Atts) != 1 || !item0Atts[0].Primary {
		t.Fatalf("expected 1 primary crop attachment on Precision Screwdriver, got %+v", item0Atts)
	}

	item1Atts, err := hbClient.GetEntityAttachments(context.Background(), feeler.ID)
	if err != nil {
		t.Fatalf("GetEntityAttachments(%s) failed: %v", feeler.ID, err)
	}
	if len(item1Atts) != 0 {
		t.Fatalf("expected 0 attachments on Brass Feeler Gauge (attach_crop=false), got %+v", item1Atts)
	}

	// Verify item crop attachment streams cleanly via attachment proxy
	thumbURL := fmt.Sprintf("%s/api/attachment-proxy/%s/%s", ts.URL, screwdriver.ID, item0Atts[0].ID)
	resThumb, err := http.Get(thumbURL)
	if err != nil || resThumb.StatusCode != http.StatusOK {
		t.Fatalf("item attachment proxy failed (%s): %v", thumbURL, err)
	}
	resThumb.Body.Close()

	t.Logf("Successfully verified live Homebox exterior cover (%s) and item crop attachment on %s", primaryTitle, screwdriver.ID)
}
