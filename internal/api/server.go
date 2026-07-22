package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type HealthResponse struct {
	Status string `json:"status"`
}

func NewHandler(asker Asker) (http.Handler, error) {
	if asker == nil {
		return nil, fmt.Errorf("API agent is nil")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", getHealth)
	mux.HandleFunc("POST /v1/ask", serveAsk(asker))
	return mux, nil
}

func getHealth(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(response).Encode(HealthResponse{Status: "ok"})
}
