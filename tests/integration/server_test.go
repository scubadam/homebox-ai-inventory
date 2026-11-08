//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

type deterministicVisionProvider struct{}

func (d *deterministicVisionProvider) Name() string { return "deterministic-mock" }
func (d *deterministicVisionProvider) Analyze(_ context.Context, _ providers.AnalyzeRequest) (*providers.RawVisionResult, error) {
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

func TestServerE2EAgainstLiveHomebox(t *testing.T) {
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

	// Register user
	regPayload := map[string]string{
		"email":    testEmail,
		"name":     "E2E Test Runner",
		"password": testPassword,
	}
	regBody, _ := json.Marshal(regPayload)
	_, _ = client.Post(baseURL+"/api/v1/users/register", "application/json", bytes.NewReader(regBody))

	// Setup Homebox client & Server
	cfg := &config.Config{
		Port:                "8000",
		HomeboxURL:          baseURL,
		HomeboxPublicURL:    baseURL,
		HomeboxUsername:     testEmail,
		HomeboxPassword:     testPassword,
		AIProvider:          "deterministic-mock",
		AIModel:             "mock",
		MaxImageDimension:   1600,
		JPEGQuality:         85,
		AllowClientSettings: true,
	}
	hbClient := homebox.NewClient(cfg.HomeboxURL, cfg.HomeboxPublicURL, "", cfg.HomeboxUsername, cfg.HomeboxPassword)
	srv := server.New(cfg, hbClient, &deterministicVisionProvider{})

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

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
