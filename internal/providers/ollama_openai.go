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

// OllamaOpenAIProvider supports both local Ollama instances (/api/chat) and
// OpenAI-compatible multimodal endpoints (/v1/chat/completions).
type OllamaOpenAIProvider struct {
	providerType string // "ollama" or "openai"
	baseURL      string
	model        string
	apiKey       string
	httpClient   *http.Client
}

// NewOllamaOpenAIProvider creates a provider for Ollama or OpenAI-compatible vision APIs.
func NewOllamaOpenAIProvider(providerType, baseURL, model, apiKey string, timeout time.Duration) *OllamaOpenAIProvider {
	providerType = strings.ToLower(strings.TrimSpace(providerType))
	if providerType == "" {
		providerType = "ollama"
	}
	if baseURL == "" {
		if providerType == "openai" {
			baseURL = "https://api.openai.com"
		} else {
			baseURL = "http://localhost:11434"
		}
	}
	if model == "" {
		if providerType == "openai" {
			model = "gpt-4o-mini"
		} else {
			model = "moondream"
		}
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &OllamaOpenAIProvider{
		providerType: providerType,
		baseURL:      strings.TrimRight(baseURL, "/"),
		model:        model,
		apiKey:       strings.TrimSpace(apiKey),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (p *OllamaOpenAIProvider) Name() string {
	return p.providerType
}

const jsonFormattingSuffix = `

IMPORTANT: Respond ONLY with a valid JSON object matching this exact structure (no markdown fences, no commentary):
{
  "container_name": "Suggested Box Name",
  "recommended_new_categories": ["NewCat1"],
  "items": [
    {
      "name": "Item Name",
      "quantity": 1,
      "description": "Details",
      "category": "CategoryName",
      "fallback_existing_category": "ExistingCategoryName",
      "status": "new",
      "existing_id": null,
      "photo_indices": [0]
    }
  ]
}`

func (p *OllamaOpenAIProvider) Analyze(ctx context.Context, req AnalyzeRequest) (*RawVisionResult, error) {
	if len(req.Images) == 0 {
		return nil, fmt.Errorf("no images provided for analysis")
	}
	if p.providerType == "openai" {
		return p.analyzeOpenAI(ctx, req)
	}
	return p.analyzeOllama(ctx, req)
}

func (p *OllamaOpenAIProvider) analyzeOllama(ctx context.Context, req AnalyzeRequest) (*RawVisionResult, error) {
	prompt := BuildPrompt(req) + jsonFormattingSuffix
	imagesB64 := make([]string, 0, len(req.Images))
	for _, img := range req.Images {
		imagesB64 = append(imagesB64, base64.StdEncoding.EncodeToString(img.Bytes))
	}

	payload := map[string]any{
		"model":  p.model,
		"stream": false,
		"format": "json",
		"messages": []map[string]any{
			{
				"role":    "user",
				"content": prompt,
				"images":  imagesB64,
			},
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/chat", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Ollama API error (status %d): %s", resp.StatusCode, string(respBytes))
	}

	var ollamaResp struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(respBytes, &ollamaResp); err != nil {
		return nil, fmt.Errorf("failed to decode Ollama response: %w", err)
	}
	return ParseCleanJSON(ollamaResp.Message.Content)
}

func (p *OllamaOpenAIProvider) analyzeOpenAI(ctx context.Context, req AnalyzeRequest) (*RawVisionResult, error) {
	prompt := BuildPrompt(req) + jsonFormattingSuffix
	contentParts := make([]map[string]any, 0, len(req.Images)+1)
	contentParts = append(contentParts, map[string]any{
		"type": "text",
		"text": prompt,
	})

	for _, img := range req.Images {
		mimeType := img.ContentType
		if mimeType == "" {
			mimeType = "image/jpeg"
		}
		dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(img.Bytes))
		contentParts = append(contentParts, map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": dataURI,
			},
		})
	}

	payload := map[string]any{
		"model":           p.model,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []map[string]any{
			{
				"role":    "user",
				"content": contentParts,
			},
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	endpoint := p.baseURL
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		if strings.HasSuffix(endpoint, "/v1") {
			endpoint += "/chat/completions"
		} else {
			endpoint += "/v1/chat/completions"
		}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenAI API error (status %d): %s", resp.StatusCode, string(respBytes))
	}

	var oaiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBytes, &oaiResp); err != nil {
		return nil, fmt.Errorf("failed to decode OpenAI response: %w", err)
	}
	if len(oaiResp.Choices) == 0 {
		return nil, fmt.Errorf("OpenAI returned no choices: %s", string(respBytes))
	}
	return ParseCleanJSON(oaiResp.Choices[0].Message.Content)
}
