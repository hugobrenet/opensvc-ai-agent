package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opensvc/ai-agent/internal/agent"
	"github.com/opensvc/ai-agent/internal/auth"
	"github.com/opensvc/ai-agent/internal/conversation"
	"github.com/opensvc/ai-agent/internal/llm"
)

var confirmationExpiry = time.Date(2026, 10, 9, 17, 45, 0, 0, time.UTC)

func TestConversationTurnStreamsAConfirmationRequest(t *testing.T) {
	execution := &testTurnExecution{run: func(_ context.Context, emit agent.EmitFunc) error {
		return emit(agent.Event{Type: agent.EventConfirmationRequired, Iteration: 1, Confirmation: &agent.Confirmation{
			ID: "confirmation-id", TurnID: "turn-id", ToolName: "stop_object", ToolTitle: "Stop object", Destructive: true,
			Arguments: json.RawMessage(`{"path": "prod/svc/web"}`), ExpiresAt: confirmationExpiry,
		}})
	}}
	service := conversationServiceFuncs{prepare: func(context.Context, auth.Identity, string, string) (conversation.TurnExecution, error) {
		return execution, nil
	}}
	request := authenticatedRequest(http.MethodPost, "/v1/conversations/conversation-id/turns", `{"prompt":"stop web"}`)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	newConversationTestHandler(t, service).ServeHTTP(response, request)
	body := response.Body.String()
	want := `event: confirmation_required` + "\n" +
		`data: {"type":"confirmation_required","iteration":1,"confirmation":{"id":"confirmation-id","turn_id":"turn-id","tool":{"name":"stop_object","title":"Stop object","destructive":true},"arguments":{"path":"prod/svc/web"},"expires_at":"2026-10-09T17:45:00Z"}}`
	if response.Code != http.StatusOK || !strings.Contains(body, want) || strings.Contains(body, "event: error") {
		t.Fatalf("got %d %s", response.Code, body)
	}
}

func TestConversationConfirmationResumesTheTurnWithTheDecision(t *testing.T) {
	for _, decision := range []agent.Decision{agent.DecisionApprove, agent.DecisionReject} {
		execution := &testTurnExecution{run: func(_ context.Context, emit agent.EmitFunc) error {
			return emit(agent.Event{Type: agent.EventCompleted, FinishReason: llm.FinishReasonCompleted, Iteration: 2})
		}}
		service := conversationServiceFuncs{confirm: func(_ context.Context, identity auth.Identity, id string, turnID string, confirmationID string, got agent.Decision) (conversation.TurnExecution, error) {
			if identity.Subject != "test-user" || id != "conversation-id" || turnID != "turn-id" || confirmationID != "confirmation-id" || got != decision {
				t.Fatalf("unexpected confirmation identity=%+v id=%q turn=%q confirmation=%q decision=%q", identity, id, turnID, confirmationID, got)
			}
			return execution, nil
		}}
		request := authenticatedRequest(http.MethodPost, "/v1/conversations/conversation-id/turns/turn-id/confirmation",
			`{"confirmation_id":"confirmation-id","decision":"`+string(decision)+`"}`)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		newConversationTestHandler(t, service).ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "event: completed") {
			t.Fatalf("%s: got %d %s", decision, response.Code, response.Body.String())
		}
	}
}

func TestConversationConfirmationRejectsAnyOtherBody(t *testing.T) {
	for name, body := range map[string]string{
		"unknown decision":   `{"confirmation_id":"confirmation-id","decision":"yes"}`,
		"wrong case":         `{"confirmation_id":"confirmation-id","decision":"Approve"}`,
		"missing id":         `{"decision":"approve"}`,
		"modified arguments": `{"confirmation_id":"confirmation-id","decision":"approve","arguments":{"path":"other"}}`,
		"free text":          `{"confirmation_id":"confirmation-id","decision":"reject","comment":"do it anyway"}`,
		"two objects":        `{"confirmation_id":"confirmation-id","decision":"approve"}{}`,
		"not an object":      `"approve"`,
	} {
		service := conversationServiceFuncs{confirm: func(context.Context, auth.Identity, string, string, string, agent.Decision) (conversation.TurnExecution, error) {
			t.Fatalf("%s reached the service", name)
			return nil, nil
		}}
		request := authenticatedRequest(http.MethodPost, "/v1/conversations/conversation-id/turns/turn-id/confirmation", body)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		newConversationTestHandler(t, service).ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s: got %d %s", name, response.Code, response.Body.String())
		}
	}
}

func TestConversationConfirmationReturnsStableErrors(t *testing.T) {
	for _, test := range []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		{conversation.ErrNotPending, http.StatusConflict, "confirmation_not_pending"},
		{conversation.ErrConfirmationExpired, http.StatusGone, "confirmation_expired"},
		{conversation.ErrNotFound, http.StatusNotFound, "conversation_not_found"},
	} {
		service := conversationServiceFuncs{confirm: func(context.Context, auth.Identity, string, string, string, agent.Decision) (conversation.TurnExecution, error) {
			return nil, test.err
		}}
		request := authenticatedRequest(http.MethodPost, "/v1/conversations/conversation-id/turns/turn-id/confirmation", `{"confirmation_id":"confirmation-id","decision":"approve"}`)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		newConversationTestHandler(t, service).ServeHTTP(response, request)
		if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), `"code":"`+test.wantCode+`"`) {
			t.Fatalf("%v: got %d %s", test.err, response.Code, response.Body.String())
		}
	}
}

func TestConversationReadReturnsThePendingConfirmation(t *testing.T) {
	service := conversationServiceFuncs{
		get: func(context.Context, auth.Identity, string) (conversation.Conversation, error) {
			return conversation.Conversation{ID: "conversation-id"}, nil
		},
		pending: func(context.Context, auth.Identity, string) (*conversation.PendingConfirmation, error) {
			return &conversation.PendingConfirmation{
				TurnID: "turn-id", ConfirmationID: "confirmation-id", ToolName: "stop_object", ToolTitle: "Stop object",
				Arguments: json.RawMessage(`{"path":"prod/svc/web"}`), ExpiresAt: confirmationExpiry,
			}, nil
		},
	}
	response := httptest.NewRecorder()
	newConversationTestHandler(t, service).ServeHTTP(response, authenticatedRequest(http.MethodGet, "/v1/conversations/conversation-id", ""))
	want := `"pending_confirmation":{"id":"confirmation-id","turn_id":"turn-id","tool":{"name":"stop_object","title":"Stop object","destructive":true},"arguments":{"path":"prod/svc/web"},"expires_at":"2026-10-09T17:45:00Z"}`
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), want) {
		t.Fatalf("got %d %s", response.Code, response.Body.String())
	}
}
