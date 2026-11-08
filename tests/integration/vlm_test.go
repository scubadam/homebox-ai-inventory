//go:build integration

package integration

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/scubadam/homebox-ai-inventory/internal/providers"
)

func getOllamaURL() string {
	url := os.Getenv("TEST_OLLAMA_URL")
	if url == "" {
		url = "http://10.43.137.132:11434"
	}
	return strings.TrimRight(url, "/")
}

// createSampleInventoryImage creates an image with two colored blocks (e.g. blue and red)
func createSampleInventoryImage() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 400, 300))
	// Gray background
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 200, G: 200, B: 200, A: 255}}, image.Point{}, draw.Src)
	// Blue box representing cables/tools
	draw.Draw(img, image.Rect(50, 50, 150, 150), &image.Uniform{C: color.RGBA{R: 30, G: 80, B: 220, A: 255}}, image.Point{}, draw.Src)
	// Red box
	draw.Draw(img, image.Rect(200, 100, 320, 220), &image.Uniform{C: color.RGBA{R: 220, G: 40, B: 40, A: 255}}, image.Point{}, draw.Src)

	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85})
	return buf.Bytes()
}

func TestOllamaVisionOnBeast(t *testing.T) {
	baseURL := getOllamaURL()

	// Check if Ollama is reachable
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(baseURL + "/api/tags")
	if err != nil {
		t.Skipf("Ollama not reachable at %s, skipping: %v", baseURL, err)
	}
	defer resp.Body.Close()

	model := os.Getenv("TEST_OLLAMA_MODEL")
	if model == "" {
		model = "llava-phi3:latest"
	}

	p := providers.NewOllamaOpenAIProvider("ollama", baseURL, model, "", 120*time.Second)

	imgBytes := createSampleInventoryImage()

	req := providers.AnalyzeRequest{
		Mode: "new",
		Images: []providers.ImageInput{
			{
				Filename:    "test_inventory.jpg",
				Bytes:       imgBytes,
				ContentType: "image/jpeg",
			},
		},
		ExistingTypeNames: []string{"Tools", "Cables", "Electronics", "Misc"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	t.Logf("Sending multimodal analyze request to Ollama (%s at %s)...", model, baseURL)
	start := time.Now()
	result, err := p.Analyze(ctx, req)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Ollama Analyze failed after %v: %v", elapsed, err)
	}

	t.Logf("Ollama (%s) succeeded in %v! Result: container_name='%s', items_count=%d",
		model, elapsed, result.ContainerName, len(result.Items))

	for i, item := range result.Items {
		t.Logf("  Item #%d: '%s' (qty=%d, cat='%s', fallback='%s', desc='%s')",
			i+1, item.Name, item.Quantity, item.Category, item.FallbackExistingCategory, item.Description)
	}

	if result.ContainerName == "" {
		t.Errorf("Expected non-empty container_name from Ollama VLM")
	}
	if len(result.Items) == 0 {
		t.Errorf("Expected at least one item identified in sample image")
	}
}
