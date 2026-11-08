package config

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds the runtime configuration for homebox-ai-inventory.
type Config struct {
	Port                string
	HomeboxURL          string
	HomeboxPublicURL    string
	HomeboxAPIKey       string
	HomeboxUsername     string
	HomeboxPassword     string
	AIProvider          string // "gemini" (default), "ollama", "openai", "anthropic"
	AIModel             string
	AIAPIKey            string
	AIBaseURL           string
	AITimeout           time.Duration
	MaxImageDimension   int
	JPEGQuality         int
	AllowClientSettings bool
	AuthUsername        string
	AuthPassword        string
	AuthEnabled         bool
}

// Load reads configuration from environment variables with sensible defaults
// and backward compatibility for legacy GEMINI_API_KEY / GEMINI_MODEL variables.
func Load() *Config {
	port := getEnv("PORT", "8000")
	hbURL := strings.TrimRight(getEnv("HOMEBOX_URL", "http://localhost:7745"), "/")
	hbPublicURL := strings.TrimRight(getEnv("HOMEBOX_PUBLIC_URL", hbURL), "/")
	hbAPIKey := getEnv("HOMEBOX_API_KEY", "")
	hbUser := getEnv("HOMEBOX_USERNAME", "")
	hbPass := getEnv("HOMEBOX_PASSWORD", "")

	provider := strings.ToLower(strings.TrimSpace(getEnv("AI_PROVIDER", "gemini")))

	// Backward compatibility: fall back to GEMINI_API_KEY if AI_API_KEY is not set
	aiKey := getEnv("AI_API_KEY", "")
	if aiKey == "" {
		aiKey = getEnv("GEMINI_API_KEY", "")
	}

	defaultModel := "gemini-2.5-flash"
	switch provider {
	case "ollama":
		defaultModel = "llava-phi3"
	case "openai":
		defaultModel = "gpt-4o-mini"
	case "anthropic":
		defaultModel = "claude-3-5-sonnet-latest"
	}

	aiModel := getEnv("AI_MODEL", "")
	if aiModel == "" {
		aiModel = getEnv("GEMINI_MODEL", defaultModel)
	}

	defaultBaseURL := ""
	switch provider {
	case "gemini":
		defaultBaseURL = "https://generativelanguage.googleapis.com"
	case "ollama":
		defaultBaseURL = "http://localhost:11434"
	case "openai":
		defaultBaseURL = "https://api.openai.com"
	case "anthropic":
		defaultBaseURL = "https://api.anthropic.com"
	}
	aiBaseURL := strings.TrimRight(getEnv("AI_BASE_URL", defaultBaseURL), "/")

	timeoutSec := getEnvInt("AI_TIMEOUT_SECONDS", 60)
	maxDim := getEnvInt("MAX_IMAGE_DIMENSION", 1600)
	jpegQual := getEnvInt("JPEG_QUALITY", 85)
	allowClient := getEnvBool("ALLOW_CLIENT_SETTINGS", false)

	authUsername := getEnv("AUTH_USERNAME", getEnv("APP_AUTH_USERNAME", ""))
	authPassword := getRawEnv("AUTH_PASSWORD", getRawEnv("APP_AUTH_PASSWORD", ""))
	authEnabled := authUsername != "" && authPassword != ""
	if (authUsername != "" && authPassword == "") || (authUsername == "" && authPassword != "") {
		log.Printf("WARNING: Partial authentication credentials configured (AUTH_USERNAME and AUTH_PASSWORD must both be set). Basic Auth remains disabled.")
	}

	return &Config{
		Port:                port,
		HomeboxURL:          hbURL,
		HomeboxPublicURL:    hbPublicURL,
		HomeboxAPIKey:       hbAPIKey,
		HomeboxUsername:     hbUser,
		HomeboxPassword:     hbPass,
		AIProvider:          provider,
		AIModel:             aiModel,
		AIAPIKey:            aiKey,
		AIBaseURL:           aiBaseURL,
		AITimeout:           time.Duration(timeoutSec) * time.Second,
		MaxImageDimension:   maxDim,
		JPEGQuality:         jpegQual,
		AllowClientSettings: allowClient,
		AuthUsername:        authUsername,
		AuthPassword:        authPassword,
		AuthEnabled:         authEnabled,
	}
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok && strings.TrimSpace(val) != "" {
		return strings.TrimSpace(val)
	}
	return fallback
}

func getRawEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if val, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(val)); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if val, ok := os.LookupEnv(key); ok {
		b, err := strconv.ParseBool(strings.TrimSpace(val))
		if err == nil {
			return b
		}
	}
	return fallback
}
