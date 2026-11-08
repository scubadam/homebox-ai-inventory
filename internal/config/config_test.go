package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	// Clear relevant env vars
	keys := []string{
		"PORT", "HOMEBOX_URL", "HOMEBOX_PUBLIC_URL", "HOMEBOX_API_KEY",
		"HOMEBOX_USERNAME", "HOMEBOX_PASSWORD", "AI_PROVIDER", "AI_MODEL",
		"AI_API_KEY", "GEMINI_API_KEY", "GEMINI_MODEL", "AI_BASE_URL",
		"AI_TIMEOUT_SECONDS", "MAX_IMAGE_DIMENSION", "JPEG_QUALITY", "ALLOW_CLIENT_SETTINGS",
	}
	for _, k := range keys {
		os.Unsetenv(k)
	}

	cfg := Load()
	if cfg.Port != "8000" {
		t.Errorf("expected Port=8000, got %s", cfg.Port)
	}
	if cfg.HomeboxURL != "http://localhost:7745" {
		t.Errorf("expected HomeboxURL=http://localhost:7745, got %s", cfg.HomeboxURL)
	}
	if cfg.AIProvider != "gemini" {
		t.Errorf("expected AIProvider=gemini, got %s", cfg.AIProvider)
	}
	if cfg.AIModel != "gemini-2.5-flash" {
		t.Errorf("expected AIModel=gemini-2.5-flash, got %s", cfg.AIModel)
	}
	if cfg.MaxImageDimension != 1600 {
		t.Errorf("expected MaxImageDimension=1600, got %d", cfg.MaxImageDimension)
	}
	if cfg.JPEGQuality != 85 {
		t.Errorf("expected JPEGQuality=85, got %d", cfg.JPEGQuality)
	}
	if cfg.AITimeout != 60*time.Second {
		t.Errorf("expected AITimeout=60s, got %v", cfg.AITimeout)
	}
}

func TestLoadBackwardCompatibilityAndProviderDefaults(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "legacy-gemini-key")
	t.Setenv("HOMEBOX_URL", "http://homebox.local:7745/")
	t.Setenv("AI_PROVIDER", "ollama")

	cfg := Load()
	if cfg.AIAPIKey != "legacy-gemini-key" {
		t.Errorf("expected AIAPIKey to fall back to GEMINI_API_KEY, got %s", cfg.AIAPIKey)
	}
	if cfg.HomeboxURL != "http://homebox.local:7745" {
		t.Errorf("expected trailing slash trimmed on HomeboxURL, got %s", cfg.HomeboxURL)
	}
	if cfg.HomeboxPublicURL != "http://homebox.local:7745" {
		t.Errorf("expected HomeboxPublicURL to default to HomeboxURL, got %s", cfg.HomeboxPublicURL)
	}
	if cfg.AIProvider != "ollama" {
		t.Errorf("expected AIProvider=ollama, got %s", cfg.AIProvider)
	}
	if cfg.AIModel != "llava-phi3" {
		t.Errorf("expected default ollama model=llava-phi3, got %s", cfg.AIModel)
	}
	if cfg.AIBaseURL != "http://localhost:11434" {
		t.Errorf("expected default ollama base URL, got %s", cfg.AIBaseURL)
	}
}

func TestLoadAuthConfig(t *testing.T) {
	// Neither set -> AuthEnabled is false
	t.Setenv("AUTH_USERNAME", "")
	t.Setenv("AUTH_PASSWORD", "")
	cfgNone := Load()
	if cfgNone.AuthEnabled {
		t.Errorf("expected AuthEnabled=false when vars are unset")
	}

	// Both set -> AuthEnabled is true
	t.Setenv("AUTH_USERNAME", "myuser")
	t.Setenv("AUTH_PASSWORD", "mypassword")
	cfgBoth := Load()
	if !cfgBoth.AuthEnabled || cfgBoth.AuthUsername != "myuser" || cfgBoth.AuthPassword != "mypassword" {
		t.Errorf("expected AuthEnabled=true with correct credentials, got %+v", cfgBoth)
	}
}
