package main

import (
	"log"
	"net/http"
	"time"

	"github.com/scubadam/homebox-ai-inventory/internal/config"
	"github.com/scubadam/homebox-ai-inventory/internal/homebox"
	"github.com/scubadam/homebox-ai-inventory/internal/providers"
	"github.com/scubadam/homebox-ai-inventory/internal/server"
)

func main() {
	cfg := config.Load()

	provider, err := providers.NewFromConfig(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize AI provider: %v", err)
	}

	hbClient := homebox.NewClient(
		cfg.HomeboxURL,
		cfg.HomeboxPublicURL,
		cfg.HomeboxAPIKey,
		cfg.HomeboxUsername,
		cfg.HomeboxPassword,
	)

	srv := server.New(cfg, hbClient, provider)

	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       90 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	log.Printf("Starting homebox-ai-inventory on :%s (provider=%s, model=%s, homebox=%s)",
		cfg.Port, provider.Name(), cfg.AIModel, cfg.HomeboxURL)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed: %v", err)
	}
}
