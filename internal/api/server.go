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

func NewHandler(asker Asker, verifier auth.TokenVerifier) (http.Handler, error) {
	if asker == nil {
		return nil, fmt.Errorf("API agent is nil")
	}
	if verifier == nil {
		return nil, fmt.Errorf("API token verifier is nil")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", getHealth)
	mux.Handle("POST /v1/ask", requireAccessToken(verifier, serveAsk(asker)))
	return mux, nil
}

func getHealth(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(response).Encode(HealthResponse{Status: "ok"})
}
