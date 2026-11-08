# Homebox AI Inventory 📦🤖

A lightweight, mobile-first Progressive Web App (PWA) companion for [Homebox](https://homebox.software) that leverages multimodal AI vision to effortlessly scan, catalog, and synchronize containers, boxes, drawers, and physical inventory.

Built in **Go**, compiled to a single static binary running on **distroless** (~15MB multi-arch image), and embedded with zero frontend build dependencies.

---

## 🌟 Key Features

* **📱 Mobile-First PWA Experience**: Installable directly to your phone's home screen with native rear camera acquisition and drag-and-drop gallery uploads.
* **⚡ Client-Side Canvas Compression**: High-res smartphone captures (10–25MB) are resized in the browser to 1600px (~250KB) in ~50ms, eliminating upload timeouts and payload limits.
* **📦 3 Workflow Modes**:
  * **`[ 📦 New Box ]`**: Take multi-angle photos of an uncataloged container, auto-generate title, categories, and items, and create both the container and contents in Homebox.
  * **`[ 🔄 Re-Inventory (Audit) ]`**: Reconciles photos against existing database items. Flags `matched` items with updated counts and `missing` items with interactive **`[ Keep ]` / `[ ✕ Delete ]`** toggles.
  * **`[ ➕ Add Items ]`**: Add items into an existing box with photos attached directly to the individual item entities (with multi-photo attribution chips).
* **🔄 Multi-Angle Synthesis & Deduplication**: Capture dense bins or drawers from different angles and depths; vision models deduplicate items so each physical item is counted only once.
* **🏷️ 1-Tap Category Creation**: Suggests new categories if detected items don't fit existing Homebox entity types, allowing 1-tap registration into Homebox (`POST /api/v1/entity-types`).
* **⭐ Cover Photo Management**: View existing photos, delete outdated images, and designate any photo as the Primary Cover in Homebox.

---

## 🧠 Pluggable AI Vision Providers

Configure your preferred vision model via environment variables:

| Provider | `AI_PROVIDER` | Default Model | Best For | Notes |
| :--- | :--- | :--- | :--- | :--- |
| **Google Gemini** | `gemini` (default) | `gemini-2.5-flash` | Production & multi-angle | Fast (~5s), free tier available, constrained JSON schema. |
| **Local Ollama** | `ollama` | `llava-phi3` | Privacy / offline | 100% self-hosted on GPU (`llava-phi3` fits in 4GB VRAM). |
| **OpenAI / Compatible** | `openai` | `gpt-4o-mini` | Hosted alternative | Also supports OpenRouter, LiteLLM, Groq, and vLLM. |
| **Anthropic Claude** | `anthropic` | `claude-3-5-sonnet` | Custom / Community | Stubbed and ready for community PRs. |

---

## 🚀 Quickstart: Docker Compose

Deploy `homebox-ai-inventory` as a lightweight sidecar alongside your official Homebox container:

```yaml
services:
  homebox:
    image: ghcr.io/sysadminsmedia/homebox:latest
    container_name: homebox
    restart: unless-stopped
    environment:
      - HBOX_MODE=production
      - HBOX_STORAGE_CONN_STRING=file:///data?no_tmp_dir=true
      - HBOX_OPTIONS_ALLOW_REGISTRATION=true
      - HBOX_WEB_PORT=7745
      - HBOX_AUTH_API_KEY_PEPPER=change-me-to-at-least-32-random-bytes
    volumes:
      - homebox_data:/data
    ports:
      - "7745:7745"

  homebox-ai-inventory:
    image: ghcr.io/scubadam/homebox-ai-inventory:latest
    container_name: homebox-ai-inventory
    restart: unless-stopped
    environment:
      - PORT=8000
      - HOMEBOX_URL=http://homebox:7745
      - HOMEBOX_PUBLIC_URL=http://localhost:7745
      - HOMEBOX_API_KEY=your-homebox-api-key # OR use HOMEBOX_USERNAME and HOMEBOX_PASSWORD
      - AI_PROVIDER=gemini # gemini, ollama, or openai
      - AI_API_KEY=your-gemini-or-openai-api-key
      - AI_MODEL=gemini-2.5-flash
    ports:
      - "8000:8000"
    depends_on:
      - homebox

volumes:
  homebox_data:
```

Open `http://localhost:8000` on your smartphone browser and tap **Add to Home Screen**.

---

## ☸️ Kubernetes (Helm Chart)

Deploy via the official Helm chart:

```bash
helm install homebox-ai-inventory oci://ghcr.io/scubadam/charts/homebox-ai-inventory \
  --set homebox.url="http://homebox.homebox.svc.cluster.local:7745" \
  --set ai.provider="gemini" \
  --set secrets.existingSecret="inventory-ai-secret" \
  --set ingress.enabled=true \
  --set ingress.hosts[0].host="inventory.yourdomain.com"
```

See [`charts/homebox-ai-inventory/values.yaml`](charts/homebox-ai-inventory/values.yaml) for full configuration options.

---

## 🛠️ Development & Testing

### Using DevContainer
Open in VS Code or GitHub Codespaces. The devcontainer automatically boots an ephemeral Homebox instance on `:7745` and configures the Go toolchain.

### Running Tests Locally
```bash
# Run unit tests with Go race detector
go test -v -race ./...

# Run live Homebox and VLM integration tests
TEST_HOMEBOX_URL=http://localhost:7745 go test -tags=integration -v ./tests/integration/...
```

---

## 🤝 Contributing

Contributions are welcome! See [`CONTRIBUTING.md`](CONTRIBUTING.md) for guidelines on adding new AI vision providers or enhancing the PWA frontend.

---

## 📄 License

Licensed under the [MIT License](LICENSE).
