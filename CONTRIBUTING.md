# Contributing to Homebox AI Inventory

Thank you for your interest in contributing to **Homebox AI Inventory**! This project is a lightweight, mobile-first Progressive Web App (PWA) written in Go that integrates physical item scanning with [Homebox](https://homebox.software) using multimodal AI vision.

---

## Architecture Overview

* **Embedded PWA Frontend (`web/static/`)**: Pure ES6 JavaScript, HTML5 Canvas downscaling, and Tailwind CSS embedded into the Go binary with `//go:embed`. No Node.js build step is required.
* **REST Client (`internal/homebox/`)**: Clean REST client for the Homebox v1 API (entities, location hierarchy, categories, attachments).
* **Imaging Engine (`internal/imaging/`)**: Pure-Go image processing (EXIF orientation auto-rotation, Catmull-Rom downscaling to 1600px, RGB JPEG conversion).
* **AI Provider Interface (`internal/providers/`)**: Pluggable multimodal vision provider system.

---

## Adding a New AI Vision Provider

Adding support for a new AI vision service (such as Anthropic Claude, Mistral Pixtral, AWS Bedrock, or custom local models) requires only **two simple steps**:

### Step 1: Implement the `VisionProvider` Interface
Create a new file in `internal/providers/` (e.g. `anthropic.go` or `my_provider.go`) that implements the 1-method [`VisionProvider`](internal/providers/provider.go) interface:

```go
package providers

import (
    "context"
)

type MyNewProvider struct {
    baseURL string
    apiKey  string
    model   string
}

func (p *MyNewProvider) Name() string {
    return "my-provider"
}

func (p *MyNewProvider) Analyze(ctx context.Context, req AnalyzeRequest) (*RawVisionResult, error) {
    // 1. Build standard prompt (deduplication, category fallbacks, update vs add mode)
    prompt := BuildPrompt(req)

    // 2. Format request payload with images (req.Images[i].Bytes are pre-optimized JPEGs)
    // ... make your HTTP request to the provider's API ...

    // 3. Parse JSON output (ParseCleanJSON automatically strips ```json code fences)
    return ParseCleanJSON(rawResponseText)
}
```

### Step 2: Register in `providers.NewFromConfig`
Open [`internal/providers/factory.go`](internal/providers/factory.go) and add your provider to the switch statement:

```go
switch cfg.AIProvider {
case "gemini":
    return NewGeminiProvider(...), nil
case "openai", "ollama":
    return NewOllamaOpenAIProvider(...), nil
case "my-provider":
    return NewMyProvider(cfg.AIBaseURL, cfg.AIModel, cfg.AIAPIKey), nil
}
```

Add unit tests in `internal/providers/providers_test.go` using `httptest.NewServer` to mock the provider API, and open a Pull Request!

---

## Development & Testing

### Using the DevContainer (Recommended)
Open the repository in VS Code or GitHub Codespaces. The devcontainer automatically boots a disposable `homebox` container on port `7745` and configures Go tools.

### Running Tests Locally
```bash
# Run fast unit tests (with race detector)
go test -v -race ./...

# Run live Homebox & VLM integration tests (requires live instances)
TEST_HOMEBOX_URL=http://localhost:7745 go test -tags=integration -v ./tests/integration/...
```
