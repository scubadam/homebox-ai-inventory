package providers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GeminiProvider implements VisionProvider using the Google Gemini REST API.
type GeminiProvider struct {
	baseURL    string
	model      string
	apiKey     string
	httpClient *http.Client
}

// NewGeminiProvider creates a concrete Google Gemini vision provider.
func NewGeminiProvider(baseURL, model, apiKey string, timeout time.Duration) *GeminiProvider {
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}
	if model == "" {
		model = "gemini-2.5-flash"
	}
	model = strings.TrimPrefix(model, "models/")
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &GeminiProvider{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		apiKey:  strings.TrimSpace(apiKey),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (g *GeminiProvider) Name() string {
	return "gemini"
}

func (g *GeminiProvider) Analyze(ctx context.Context, req AnalyzeRequest) (*RawVisionResult, error) {
	if g.apiKey == "" {
		return nil, fmt.Errorf("AI_API_KEY (or GEMINI_API_KEY) is not configured")
	}
	if len(req.Images) == 0 {
		return nil, fmt.Errorf("no images provided for analysis")
	}

	prompt := BuildPrompt(req)
	parts := make([]map[string]any, 0, len(req.Images)+1)
	parts = append(parts, map[string]any{"text": prompt})

	for _, img := range req.Images {
		mimeType := img.ContentType
		if mimeType == "" {
			mimeType = "image/jpeg"
		}
		parts = append(parts, map[string]any{
			"inlineData": map[string]any{
				"mimeType": mimeType,
				"data":     base64.StdEncoding.EncodeToString(img.Bytes),
			},
		})
	}

	payload := map[string]any{
		"contents": []map[string]any{
			{"parts": parts},
		},
		"generationConfig": map[string]any{
			"responseMimeType": "application/json",
			"responseSchema":   geminiResponseSchema(),
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", g.baseURL, g.model, g.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := g.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Gemini API error (status %d): %s", resp.StatusCode, string(respBytes))
	}

	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(respBytes, &geminiResp); err != nil {
		return nil, fmt.Errorf("failed to decode Gemini response envelope: %w", err)
	}
	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("Gemini returned no candidates: %s", string(respBytes))
	}

	contentText := geminiResp.Candidates[0].Content.Parts[0].Text
	return ParseCleanJSON(contentText)
}

func geminiResponseSchema() map[string]any {
	return map[string]any{
		"type": "OBJECT",
		"properties": map[string]any{
			"container_name": map[string]any{
				"type":        "STRING",
				"description": "Suggested title for the container",
			},
			"recommended_new_categories": map[string]any{
				"type":        "ARRAY",
				"items":       map[string]any{"type": "STRING"},
				"description": "Any new category names recommended that do not exist yet in Homebox",
			},
			"items": map[string]any{
				"type": "ARRAY",
				"items": map[string]any{
					"type": "OBJECT",
					"properties": map[string]any{
						"name":                       map[string]any{"type": "STRING"},
						"quantity":                   map[string]any{"type": "INTEGER"},
						"description":                map[string]any{"type": "STRING"},
						"category":                   map[string]any{"type": "STRING"},
						"fallback_existing_category": map[string]any{"type": "STRING"},
						"status":                     map[string]any{"type": "STRING", "enum": []string{"new", "matched"}},
						"existing_id":                map[string]any{"type": "STRING", "nullable": true},
						"photo_indices": map[string]any{
							"type":        "ARRAY",
							"items":       map[string]any{"type": "INTEGER"},
							"description": "0-based indices of the photos where this item appears",
						},
					},
					"required": []string{"name", "quantity", "description", "category", "fallback_existing_category"},
				},
			},
		},
		"required": []string{"items"},
	}
}
