//go:build integration

package integration

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/scubadam/homebox-ai-inventory/internal/providers"
)

func getGeminiAPIKey() string {
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		key = os.Getenv("AI_API_KEY")
	}
	return strings.TrimSpace(key)
}

func TestGeminiLiveVision(t *testing.T) {
	apiKey := getGeminiAPIKey()
	if apiKey == "" {
		t.Skip("GEMINI_API_KEY or AI_API_KEY not set; skipping live Gemini integration test")
	}

	model := os.Getenv("GEMINI_MODEL")
	if model == "" {
		model = "gemini-2.5-flash"
	}

	p := providers.NewGeminiProvider("", model, apiKey, 30*time.Second)

	imgBytes := createSampleInventoryImage()

	req := providers.AnalyzeRequest{
		Mode: "new",
		Images: []providers.ImageInput{
			{
				Filename:    "sample_hardware.jpg",
				Bytes:       imgBytes,
				ContentType: "image/jpeg",
			},
		},
		ExistingTypeNames: []string{"Tools", "Cables", "Electronics", "Misc"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Logf("Sending real multimodal analyze request to Google Gemini API (%s)...", model)
	start := time.Now()
	result, err := p.Analyze(ctx, req)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Live Gemini Analyze failed after %v: %v", elapsed, err)
	}

	t.Logf("Live Gemini API (%s) succeeded in %v! Container: '%s', Items: %d, Recommended Categories: %v",
		model, elapsed, result.ContainerName, len(result.Items), result.RecommendedNewCategories)

	for i, it := range result.Items {
		t.Logf("  Item #%d: '%s' (qty=%d, cat='%s', fallback='%s', desc='%s')",
			i+1, it.Name, it.Quantity, it.Category, it.FallbackExistingCategory, it.Description)
	}

	if result.ContainerName == "" {
		t.Errorf("Expected non-empty container_name from Gemini")
	}
	if len(result.Items) == 0 {
		t.Errorf("Expected at least one item detected by Gemini")
	}
}
