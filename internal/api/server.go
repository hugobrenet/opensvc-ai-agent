package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/hugobrenet/opensvc-ai-agent/internal/auth"
)

type HealthResponse struct {
	Status string `json:"status"`
}

type HandlerConfig struct {
	MaxConcurrentAsks int
}

func NewHandler(asker Asker, verifier auth.TokenVerifier, config HandlerConfig) (http.Handler, error) {
	if asker == nil {
		return nil, fmt.Errorf("API agent is nil")
	}
	if verifier == nil {
		return nil, fmt.Errorf("API token verifier is nil")
	}
	if config.MaxConcurrentAsks <= 0 {
		return nil, fmt.Errorf("API max concurrent asks must be positive")
	}
	limiter := newAskLimiter(config.MaxConcurrentAsks)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", getHealth)
	mux.Handle("POST /v1/ask", requireAccessToken(verifier, serveAsk(asker, limiter)))
	return mux, nil
}

func getHealth(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(response).Encode(HealthResponse{Status: "ok"})
}
