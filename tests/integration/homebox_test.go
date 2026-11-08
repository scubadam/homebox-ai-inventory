//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/scubadam/homebox-ai-inventory/internal/homebox"
)

func getTestHomeboxURL() string {
	url := os.Getenv("TEST_HOMEBOX_URL")
	if url == "" {
		url = os.Getenv("HOMEBOX_URL")
	}
	if url == "" {
		url = "http://localhost:7745"
	}
	return strings.TrimRight(url, "/")
}

func makeTestJPEG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 120, 120))
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80})
	return buf.Bytes()
}

func TestLiveHomeboxE2E(t *testing.T) {
	baseURL := getTestHomeboxURL()

	// Check if Homebox is reachable
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(baseURL + "/manifest.json")
	if err != nil {
		resp, err = client.Get(baseURL + "/")
	}
	if err != nil {
		t.Skipf("Live Homebox instance not reachable at %s, skipping integration test: %v", baseURL, err)
	}
	if resp != nil {
		resp.Body.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	uniqueID := time.Now().UnixNano()
	testEmail := fmt.Sprintf("test-%d@example.com", uniqueID)
	testPassword := "TestPassword123!"

	// Attempt user registration on test Homebox instance
	regPayload := map[string]string{
		"email":    testEmail,
		"name":     "Integration Test Runner",
		"password": testPassword,
	}
	regBody, _ := json.Marshal(regPayload)
	regReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/v1/users/register", bytes.NewReader(regBody))
	regReq.Header.Set("Content-Type", "application/json")
	regResp, err := client.Do(regReq)
	if err != nil {
		t.Fatalf("failed to call user registration: %v", err)
	}
	defer regResp.Body.Close()

	if regResp.StatusCode != http.StatusOK && regResp.StatusCode != http.StatusCreated {
		t.Logf("registration returned status %d (registration might be disabled or user exists)", regResp.StatusCode)
	}

	// Instantiate the Homebox client using auto-login
	hb := homebox.NewClient(baseURL, baseURL, "", testEmail, testPassword)

	// 1. Create a Location entity type
	locType, err := hb.CreateEntityType(ctx, fmt.Sprintf("Test Bin %d", uniqueID), true)
	if err != nil {
		t.Fatalf("CreateEntityType (location) failed: %v", err)
	}
	if locType.ID == "" || !locType.IsLocation {
		t.Fatalf("Unexpected locType: %+v", locType)
	}

	// 2. Create an Item entity type
	itemType, err := hb.CreateEntityType(ctx, fmt.Sprintf("Test Part %d", uniqueID), false)
	if err != nil {
		t.Fatalf("CreateEntityType (item) failed: %v", err)
	}
	if itemType.ID == "" || itemType.IsLocation {
		t.Fatalf("Unexpected itemType: %+v", itemType)
	}

	// 3. Create Container Entity
	containerID, err := hb.CreateEntity(ctx, homebox.EntityInput{
		Name:         fmt.Sprintf("Storage Box %d", uniqueID),
		EntityTypeID: locType.ID,
		Description:  "Created by integration test harness",
		Quantity:     1,
	})
	if err != nil {
		t.Fatalf("CreateEntity (container) failed: %v", err)
	}
	if containerID == "" {
		t.Fatalf("Expected non-empty containerID")
	}

	// 4. Create child item inside container
	parentID := containerID
	itemID, err := hb.CreateEntity(ctx, homebox.EntityInput{
		Name:         "Sensor DS18B20",
		ParentID:     &parentID,
		EntityTypeID: itemType.ID,
		Description:  "Digital thermometer probe",
		Quantity:     10,
	})
	if err != nil {
		t.Fatalf("CreateEntity (child item) failed: %v", err)
	}
	if itemID == "" {
		t.Fatalf("Expected non-empty itemID")
	}

	// 5. Upload photo attachment
	testJPEG := makeTestJPEG()
	err = hb.UploadAttachment(ctx, containerID, "box_photo.jpg", "image/jpeg", testJPEG, true)
	if err != nil {
		t.Fatalf("UploadAttachment failed: %v", err)
	}

	// 6. Verify container items and attachments via GetContainerDetails
	details, err := hb.GetContainerDetails(ctx, containerID)
	if err != nil {
		t.Fatalf("GetContainerDetails failed: %v", err)
	}

	if len(details.Items) != 1 {
		t.Fatalf("Expected 1 child item in container, got %d", len(details.Items))
	}
	if details.Items[0].ID != itemID || details.Items[0].Quantity != 10 {
		t.Errorf("Unexpected child item details: %+v", details.Items[0])
	}
	if len(details.Attachments) != 1 {
		t.Fatalf("Expected 1 photo attachment on container, got %d", len(details.Attachments))
	}
	if !details.Attachments[0].Primary {
		t.Errorf("Expected uploaded photo attachment to be marked primary")
	}

	// 7. Cleanup created items
	_ = hb.DeleteEntity(ctx, itemID)
	_ = hb.DeleteEntity(ctx, containerID)
	t.Logf("Successfully executed Homebox live E2E integration test against %s", baseURL)
}
