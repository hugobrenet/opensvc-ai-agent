package main

import (
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/hugobrenet/opensvc-ai-agent/internal/api"
	"github.com/hugobrenet/opensvc-ai-agent/internal/config"
)

func main() {
	processConfig, err := config.Load()
	if err != nil {
		log.Fatalf("load configuration: %v", err)
	}

	server := &http.Server{
		Addr:              processConfig.ListenAddress,
		Handler:           api.NewHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("opensvc-ai-agentd listening on http://%s", processConfig.ListenAddress)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve HTTP API: %v", err)
	}
}
