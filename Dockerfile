# Multi-stage, multi-arch Dockerfile for homebox-ai-inventory
# Compiles a single static Go binary and runs on a minimal distroless image (~15MB total).

# Stage 1: Build static binary
FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /app

# Cache Go modules
COPY go.mod go.sum ./
RUN go mod download

# Copy source tree and embedded static assets
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/ web/

# Compile stripped static binary for target architecture
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w -extldflags '-static'" -o /homebox-ai-inventory ./cmd/server

# Stage 2: Minimal Distroless Runtime
FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.title="Homebox AI Inventory" \
      org.opencontainers.image.description="Smart multi-angle AI vision inventory cataloger for Homebox" \
      org.opencontainers.image.source="https://github.com/scubadam/homebox-ai-inventory" \
      org.opencontainers.image.licenses="MIT"

WORKDIR /

COPY --from=builder /homebox-ai-inventory /homebox-ai-inventory

# Default listen port
EXPOSE 8000

# Runs as unprivileged nonroot user (UID 65532)
USER nonroot:nonroot

ENTRYPOINT ["/homebox-ai-inventory"]
