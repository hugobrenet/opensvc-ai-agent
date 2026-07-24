package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/hugobrenet/opensvc-ai-agent/internal/agent"
	"github.com/hugobrenet/opensvc-ai-agent/internal/auth"
	"github.com/hugobrenet/opensvc-ai-agent/internal/conversation"
)

type ConversationService interface {
	Create(context.Context, auth.Identity) (conversation.Conversation, error)
	Get(context.Context, auth.Identity, string) (conversation.Conversation, error)
	List(context.Context, auth.Identity) ([]conversation.Conversation, error)
	Delete(context.Context, auth.Identity, string) error
	PrepareTurn(context.Context, auth.Identity, string, string) (conversation.TurnExecution, error)
}

type ConversationResponse struct {
	ID          string    `json:"id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	StoredBytes int64     `json:"stored_bytes"`
}

type ConversationEnvelope struct {
	Conversation ConversationResponse `json:"conversation"`
}

type ConversationListResponse struct {
	Conversations []ConversationResponse `json:"conversations"`
}

func serveCreateConversation(service ConversationService, audit auditLogger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if request.ContentLength != 0 {
			audit.event(request.Context(), "conversation_create_rejected",
				slog.Int("status", http.StatusBadRequest), slog.String("code", "invalid_request"),
			)
			writeJSONError(response, http.StatusBadRequest, "invalid_request", "conversation creation does not accept a request body")
			return
		}
		identity, ok := auth.IdentityFromContext(request.Context())
		if !ok {
			writeUnauthorized(response)
			return
		}
		item, err := service.Create(request.Context(), identity)
		if err != nil {
			writeConversationError(response, request, audit, "conversation_create_rejected", err, "")
			return
		}
		audit.event(request.Context(), "conversation_created", slog.String("conversation_id", boundedAuditID(item.ID)))
		writeJSON(response, http.StatusCreated, ConversationEnvelope{Conversation: newConversationResponse(item)})
	}
}

func serveListConversations(service ConversationService, audit auditLogger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := auth.IdentityFromContext(request.Context())
		if !ok {
			writeUnauthorized(response)
			return
		}
		items, err := service.List(request.Context(), identity)
		if err != nil {
			writeConversationError(response, request, audit, "conversation_list_rejected", err, "")
			return
		}
		result := make([]ConversationResponse, 0, len(items))
		for _, item := range items {
			result = append(result, newConversationResponse(item))
		}
		audit.event(request.Context(), "conversations_listed", slog.Int("count", len(result)))
		writeJSON(response, http.StatusOK, ConversationListResponse{Conversations: result})
	}
}

func serveGetConversation(service ConversationService, audit auditLogger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := auth.IdentityFromContext(request.Context())
		if !ok {
			writeUnauthorized(response)
			return
		}
		id := request.PathValue("id")
		item, err := service.Get(request.Context(), identity, id)
		if err != nil {
			writeConversationError(response, request, audit, "conversation_get_rejected", err, id)
			return
		}
		audit.event(request.Context(), "conversation_read", slog.String("conversation_id", boundedAuditID(id)))
		writeJSON(response, http.StatusOK, ConversationEnvelope{Conversation: newConversationResponse(item)})
	}
}

func serveDeleteConversation(service ConversationService, audit auditLogger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := auth.IdentityFromContext(request.Context())
		if !ok {
			writeUnauthorized(response)
			return
		}
		id := request.PathValue("id")
		if err := service.Delete(request.Context(), identity, id); err != nil {
			writeConversationError(response, request, audit, "conversation_delete_rejected", err, id)
			return
		}
		audit.event(request.Context(), "conversation_deleted", slog.String("conversation_id", boundedAuditID(id)))
		response.WriteHeader(http.StatusNoContent)
	}
}

func serveConversationTurn(service ConversationService, limiter *askLimiter, audit auditLogger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		turnRequest, status, apiError := decodeAskRequest(response, request)
		id := request.PathValue("id")
		attributes := []slog.Attr{slog.String("conversation_id", boundedAuditID(id))}
		if apiError != nil {
			audit.event(request.Context(), "conversation_turn_rejected", appendAttributes(attributes,
				slog.Int("status", status), slog.String("code", apiError.Code),
			)...)
			writeJSONError(response, status, apiError.Code, apiError.Message)
			return
		}
		if !acquireAgentSlot(response, request, limiter, audit, "conversation_turn", attributes) {
			return
		}
		defer limiter.release()
		identity, ok := auth.IdentityFromContext(request.Context())
		if !ok {
			writeUnauthorized(response)
			return
		}
		execution, err := service.PrepareTurn(request.Context(), identity, id, turnRequest.Prompt)
		if err != nil {
			writeConversationError(response, request, audit, "conversation_turn_rejected", err, id)
			return
		}
		started := streamAgent(response, request, audit, "conversation_turn", attributes, func(emit agent.EmitFunc) error {
			return execution.Run(request.Context(), emit)
		})
		if !started {
			_ = execution.Cancel("streaming_unavailable")
		}
	}
}

func newConversationResponse(item conversation.Conversation) ConversationResponse {
	return ConversationResponse{
		ID: item.ID, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		ExpiresAt: item.ExpiresAt, StoredBytes: item.StoredBytes,
	}
}

func writeConversationError(response http.ResponseWriter, request *http.Request, audit auditLogger, event string, err error, id string) {
	status, code, message := conversationError(err)
	attributes := []slog.Attr{slog.Int("status", status), slog.String("code", code)}
	if id != "" {
		attributes = append(attributes, slog.String("conversation_id", boundedAuditID(id)))
	}
	audit.event(request.Context(), event, attributes...)
	writeJSONError(response, status, code, message)
}

func conversationError(err error) (int, string, string) {
	switch {
	case errors.Is(err, conversation.ErrExpired):
		return http.StatusGone, "conversation_expired", "the conversation has expired"
	case errors.Is(err, conversation.ErrNotFound):
		return http.StatusNotFound, "conversation_not_found", "the conversation was not found"
	case errors.Is(err, conversation.ErrBusy):
		return http.StatusConflict, "conversation_busy", "the conversation already has an active turn"
	case errors.Is(err, conversation.ErrLimit):
		return http.StatusConflict, "conversation_limit", "the conversation limit has been reached"
	case errors.Is(err, conversation.ErrInvalid):
		return http.StatusBadRequest, "invalid_request", "the conversation request is invalid"
	default:
		return http.StatusInternalServerError, "conversation_failed", "the conversation operation failed"
	}
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
