package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hugobrenet/opensvc-ai-agent/internal/agent"
	"github.com/hugobrenet/opensvc-ai-agent/internal/auth"
	"github.com/hugobrenet/opensvc-ai-agent/internal/conversation"
	conversationsqlite "github.com/hugobrenet/opensvc-ai-agent/internal/conversation/sqlite"
	"github.com/hugobrenet/opensvc-ai-agent/internal/llm"
)

func TestConversationFlowPersistsHistoryAndIsolatesOwner(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := conversationsqlite.Open(t.Context(), conversationsqlite.Config{Path: filepath.Join(directory, "conversations.db")})
	if err != nil {
		t.Fatalf("open conversation store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	turnNumber := 0
	runner := apiTurnRunnerFunc(func(_ context.Context, history []llm.Message, prompt string, emit agent.EmitFunc) (agent.TurnResult, error) {
		turnNumber++
		if turnNumber == 1 && len(history) != 0 {
			t.Fatalf("first turn history=%#v", history)
		}
		if turnNumber == 2 && (len(history) != 2 || history[0].Text != "first") {
			t.Fatalf("second turn history=%#v", history)
		}
		answer := "answer " + prompt
		if err := emit(agent.Event{Type: agent.EventTextDelta, TextDelta: answer, Iteration: 1}); err != nil {
			return agent.TurnResult{}, err
		}
		if err := emit(agent.Event{Type: agent.EventCompleted, FinishReason: llm.FinishReasonCompleted, Iteration: 1}); err != nil {
			return agent.TurnResult{}, err
		}
		return agent.TurnResult{Messages: []llm.Message{
			{Role: llm.RoleUser, Text: prompt}, {Role: llm.RoleAssistant, Text: answer},
		}, FinishReason: llm.FinishReasonCompleted}, nil
	})
	service, err := conversation.NewService(store, runner, conversation.ServiceConfig{})
	if err != nil {
		t.Fatalf("create conversation service: %v", err)
	}
	verifier := tokenVerifierFunc(func(_ context.Context, token string) (auth.Identity, error) {
		return auth.Identity{Issuer: "cluster", Subject: token}, nil
	})
	handler, err := NewHandler(
		askerFunc(func(context.Context, string, agent.EmitFunc) error { return nil }),
		service, verifier,
		HandlerConfig{MaxConcurrentAsks: 4, AuditLogger: slog.New(slog.NewTextHandler(io.Discard, nil))},
	)
	if err != nil {
		t.Fatalf("create handler: %v", err)
	}

	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, requestWithToken(http.MethodPost, "/v1/conversations", "alice", ""))
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createResponse.Code, createResponse.Body.String())
	}
	var created ConversationEnvelope
	if err := json.NewDecoder(createResponse.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	id := created.Conversation.ID

	otherOwnerResponse := httptest.NewRecorder()
	handler.ServeHTTP(otherOwnerResponse, requestWithToken(http.MethodGet, "/v1/conversations/"+id, "bob", ""))
	if otherOwnerResponse.Code != http.StatusNotFound {
		t.Fatalf("other owner status=%d body=%s", otherOwnerResponse.Code, otherOwnerResponse.Body.String())
	}

	for _, prompt := range []string{"first", "second"} {
		response := httptest.NewRecorder()
		request := requestWithToken(http.MethodPost, "/v1/conversations/"+id+"/turns", "alice", `{"prompt":"`+prompt+`"}`)
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"type":"completed"`) {
			t.Fatalf("turn %q status=%d body=%s", prompt, response.Code, response.Body.String())
		}
	}
	history, err := store.LoadHistory(t.Context(), conversation.Owner{Issuer: "cluster", Subject: "alice"}, id)
	if err != nil || len(history) != 4 {
		t.Fatalf("stored history=%#v error=%v", history, err)
	}
}

type apiTurnRunnerFunc func(context.Context, []llm.Message, string, agent.EmitFunc) (agent.TurnResult, error)

func (f apiTurnRunnerFunc) RunTurn(ctx context.Context, history []llm.Message, prompt string, emit agent.EmitFunc) (agent.TurnResult, error) {
	return f(ctx, history, prompt, emit)
}

func requestWithToken(method string, path string, token string, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	return request
}
