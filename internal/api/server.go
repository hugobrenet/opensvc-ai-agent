package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/hugobrenet/opensvc-ai-agent/internal/auth"
)

type HealthResponse struct {
	Status string `json:"status"`
}

type HandlerConfig struct {
	MaxConcurrentAsks int
	AuditLogger       *slog.Logger
}

func NewHandler(asker Asker, conversations ConversationService, verifier auth.TokenVerifier, config HandlerConfig) (http.Handler, error) {
	if asker == nil {
		return nil, fmt.Errorf("API agent is nil")
	}
	if verifier == nil {
		return nil, fmt.Errorf("API token verifier is nil")
	}
	if conversations == nil {
		return nil, fmt.Errorf("API conversation service is nil")
	}
	if config.MaxConcurrentAsks <= 0 {
		return nil, fmt.Errorf("API max concurrent asks must be positive")
	}
	if config.AuditLogger == nil {
		return nil, fmt.Errorf("API audit logger is nil")
	}
	audit := auditLogger{logger: config.AuditLogger}
	limiter := newAskLimiter(config.MaxConcurrentAsks)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", getHealth)
	mux.Handle("POST /v1/ask", requireAccessToken(verifier, audit, serveAsk(asker, limiter, audit)))
	mux.Handle("POST /v1/conversations", requireAccessToken(verifier, audit, serveCreateConversation(conversations, audit)))
	mux.Handle("GET /v1/conversations", requireAccessToken(verifier, audit, serveListConversations(conversations, audit)))
	mux.Handle("GET /v1/conversations/{id}", requireAccessToken(verifier, audit, serveGetConversation(conversations, audit)))
	mux.Handle("DELETE /v1/conversations/{id}", requireAccessToken(verifier, audit, serveDeleteConversation(conversations, audit)))
	mux.Handle("POST /v1/conversations/{id}/turns", requireAccessToken(verifier, audit, serveConversationTurn(conversations, limiter, audit)))
	return withRequestID(mux), nil
}

func getHealth(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(response).Encode(HealthResponse{Status: "ok"})
}
