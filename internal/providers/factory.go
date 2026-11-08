package providers

import (
	"context"
	"fmt"

	"github.com/scubadam/homebox-ai-inventory/internal/config"
)

// AnthropicProvider is a clean interface stub for Anthropic Claude Vision support.
type AnthropicProvider struct {
	model string
}

// NewAnthropicProvider creates an Anthropic provider stub.
func NewAnthropicProvider(model string) *AnthropicProvider {
	if model == "" {
		model = "claude-3-5-sonnet-latest"
	}
	return &AnthropicProvider{model: model}
}

func (a *AnthropicProvider) Name() string {
	return "anthropic"
}

func (a *AnthropicProvider) Analyze(_ context.Context, _ AnalyzeRequest) (*RawVisionResult, error) {
	return nil, fmt.Errorf("provider 'anthropic' (%s) is currently a stub; community PRs welcome or switch AI_PROVIDER to 'gemini', 'ollama', or 'openai'", a.model)
}

// NewFromConfig instantiates the configured VisionProvider.
func NewFromConfig(cfg *config.Config) (VisionProvider, error) {
	switch cfg.AIProvider {
	case "gemini", "":
		return NewGeminiProvider(cfg.AIBaseURL, cfg.AIModel, cfg.AIAPIKey, cfg.AITimeout), nil
	case "ollama", "openai":
		return NewOllamaOpenAIProvider(cfg.AIProvider, cfg.AIBaseURL, cfg.AIModel, cfg.AIAPIKey, cfg.AITimeout), nil
	case "anthropic":
		return NewAnthropicProvider(cfg.AIModel), nil
	default:
		return nil, fmt.Errorf("unsupported AI_PROVIDER '%s' (supported: gemini, ollama, openai, anthropic)", cfg.AIProvider)
	}
}
