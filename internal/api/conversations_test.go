package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hugobrenet/opensvc-ai-agent/internal/agent"
	"github.com/hugobrenet/opensvc-ai-agent/internal/auth"
	"github.com/hugobrenet/opensvc-ai-agent/internal/conversation"
	"github.com/hugobrenet/opensvc-ai-agent/internal/llm"
)

func TestConversationLifecycleRoutes(t *testing.T) {
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	item := conversation.Conversation{ID: "conversation-id", Title: "Cluster health", CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour)}
	deleted := false
	updated := false
	service := conversationServiceFuncs{
		create: func(_ context.Context, identity auth.Identity) (conversation.Conversation, error) {
			if identity.Subject != "test-user" || identity.Issuer != "test-node" {
				t.Fatalf("unexpected identity %+v", identity)
			}
			return item, nil
		},
		list: func(context.Context, auth.Identity) ([]conversation.Conversation, error) {
			return []conversation.Conversation{item}, nil
		},
		get: func(context.Context, auth.Identity, string) (conversation.Conversation, error) { return item, nil },
		update: func(_ context.Context, identity auth.Identity, id string, title string) (conversation.Conversation, error) {
			if identity.Subject != "test-user" || id != item.ID || title != "Renamed conversation" {
				t.Fatalf("unexpected title update identity=%+v id=%q title=%q", identity, id, title)
			}
			updated = true
			item.Title = title
			return item, nil
		},
		delete: func(context.Context, auth.Identity, string) error {
			deleted = true
			return nil
		},
	}
	handler := newConversationTestHandler(t, service)

	create := authenticatedRequest(http.MethodPost, "/v1/conversations", "")
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createResponse.Code, createResponse.Body.String())
	}
	var envelope ConversationEnvelope
	if err := json.NewDecoder(createResponse.Body).Decode(&envelope); err != nil || envelope.Conversation.ID != item.ID || envelope.Conversation.Title != item.Title {
		t.Fatalf("create response=%+v error=%v", envelope, err)
	}

	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, authenticatedRequest(http.MethodGet, "/v1/conversations", ""))
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), item.ID) {
		t.Fatalf("list status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}

	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, authenticatedRequest(http.MethodGet, "/v1/conversations/"+item.ID, ""))
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Body.String(), item.ID) {
		t.Fatalf("get status=%d body=%s", getResponse.Code, getResponse.Body.String())
	}

	updateRequest := authenticatedRequest(http.MethodPatch, "/v1/conversations/"+item.ID, `{"title":"Renamed conversation"}`)
	updateRequest.Header.Set("Content-Type", "application/json")
	updateResponse := httptest.NewRecorder()
	handler.ServeHTTP(updateResponse, updateRequest)
	if updateResponse.Code != http.StatusOK || !updated {
		t.Fatalf("update status=%d updated=%t body=%s", updateResponse.Code, updated, updateResponse.Body.String())
	}
	if err := json.NewDecoder(updateResponse.Body).Decode(&envelope); err != nil || envelope.Conversation.Title != "Renamed conversation" {
		t.Fatalf("update response=%+v error=%v", envelope, err)
	}

	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, authenticatedRequest(http.MethodDelete, "/v1/conversations/"+item.ID, ""))
	if deleteResponse.Code != http.StatusNoContent || !deleted {
		t.Fatalf("delete status=%d deleted=%t", deleteResponse.Code, deleted)
	}
}

func TestConversationTitleUpdateRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
		wantCode    string
	}{
		{name: "content type", body: `{"title":"valid"}`, wantStatus: http.StatusUnsupportedMediaType, wantCode: "unsupported_media_type"},
		{name: "malformed", contentType: "application/json", body: `{"title":`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "unknown field", contentType: "application/json", body: `{"title":"valid","extra":true}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "multiple values", contentType: "application/json", body: `{"title":"valid"}{}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "oversized", contentType: "application/json", body: `{"title":"` + strings.Repeat("a", maxConversationTitleRequestBytes) + `"}`, wantStatus: http.StatusRequestEntityTooLarge, wantCode: "request_too_large"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := newConversationTestHandler(t, conversationServiceFuncs{})
			request := authenticatedRequest(http.MethodPatch, "/v1/conversations/id", test.body)
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var body ErrorResponse
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil || body.Error.Code != test.wantCode {
				t.Fatalf("error response=%+v decode=%v", body, err)
			}
		})
	}
}

func TestConversationTitleUpdateReturnsStableServiceErrors(t *testing.T) {
	for _, test := range []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "not found", err: conversation.ErrNotFound, wantStatus: http.StatusNotFound, wantCode: "conversation_not_found"},
		{name: "expired", err: conversation.ErrExpired, wantStatus: http.StatusGone, wantCode: "conversation_expired"},
		{name: "invalid title", err: conversation.ErrInvalid, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := conversationServiceFuncs{update: func(context.Context, auth.Identity, string, string) (conversation.Conversation, error) {
				return conversation.Conversation{}, test.err
			}}
			handler := newConversationTestHandler(t, service)
			request := authenticatedRequest(http.MethodPatch, "/v1/conversations/id", `{"title":"valid"}`)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var body ErrorResponse
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil || body.Error.Code != test.wantCode {
				t.Fatalf("error response=%+v decode=%v", body, err)
			}
		})
	}
}

func TestConversationTurnStreamsPreparedExecution(t *testing.T) {
	execution := &testTurnExecution{run: func(_ context.Context, emit agent.EmitFunc) error {
		if err := emit(agent.Event{Type: agent.EventTextDelta, TextDelta: "healthy", Iteration: 1}); err != nil {
			return err
		}
		return emit(agent.Event{Type: agent.EventCompleted, FinishReason: llm.FinishReasonCompleted, Iteration: 1})
	}}
	service := conversationServiceFuncs{prepare: func(_ context.Context, identity auth.Identity, id string, prompt string) (conversation.TurnExecution, error) {
		if identity.Subject != "test-user" || id != "conversation-id" || prompt != "health" {
			t.Fatalf("unexpected prepared turn identity=%+v id=%q prompt=%q", identity, id, prompt)
		}
		return execution, nil
	}}
	handler := newConversationTestHandler(t, service)
	request := authenticatedRequest(http.MethodPost, "/v1/conversations/conversation-id/turns", `{"prompt":"health"}`)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("turn status=%d body=%s", response.Code, response.Body.String())
	}
	events := decodeSSEEvents(t, response.Body.String())
	if len(events) != 2 || events[0].Type != "text_delta" || events[1].Type != "completed" {
		t.Fatalf("unexpected turn events %#v", events)
	}
}

func TestConversationTurnReturnsStablePreStreamErrors(t *testing.T) {
	for _, test := range []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "not found", err: conversation.ErrNotFound, wantStatus: http.StatusNotFound, wantCode: "conversation_not_found"},
		{name: "expired", err: conversation.ErrExpired, wantStatus: http.StatusGone, wantCode: "conversation_expired"},
		{name: "busy", err: conversation.ErrBusy, wantStatus: http.StatusConflict, wantCode: "conversation_busy"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := conversationServiceFuncs{prepare: func(context.Context, auth.Identity, string, string) (conversation.TurnExecution, error) {
				return nil, test.err
			}}
			handler := newConversationTestHandler(t, service)
			request := authenticatedRequest(http.MethodPost, "/v1/conversations/id/turns", `{"prompt":"health"}`)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var body ErrorResponse
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil || body.Error.Code != test.wantCode {
				t.Fatalf("error response=%+v decode=%v", body, err)
			}
		})
	}
}

func TestConversationRoutesRequireAuthentication(t *testing.T) {
	handler := newConversationTestHandler(t, conversationServiceFuncs{})
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/v1/conversations"},
		{http.MethodGet, "/v1/conversations"},
		{http.MethodGet, "/v1/conversations/id"},
		{http.MethodPatch, "/v1/conversations/id"},
		{http.MethodDelete, "/v1/conversations/id"},
		{http.MethodPost, "/v1/conversations/id/turns"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(route.method, route.path, nil))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status=%d", route.method, route.path, response.Code)
		}
	}
}

type conversationServiceFuncs struct {
	create  func(context.Context, auth.Identity) (conversation.Conversation, error)
	get     func(context.Context, auth.Identity, string) (conversation.Conversation, error)
	list    func(context.Context, auth.Identity) ([]conversation.Conversation, error)
	update  func(context.Context, auth.Identity, string, string) (conversation.Conversation, error)
	delete  func(context.Context, auth.Identity, string) error
	prepare func(context.Context, auth.Identity, string, string) (conversation.TurnExecution, error)
}

func (s conversationServiceFuncs) Create(ctx context.Context, identity auth.Identity) (conversation.Conversation, error) {
	if s.create == nil {
		return conversation.Conversation{}, errors.New("unexpected create")
	}
	return s.create(ctx, identity)
}
func (s conversationServiceFuncs) Get(ctx context.Context, identity auth.Identity, id string) (conversation.Conversation, error) {
	if s.get == nil {
		return conversation.Conversation{}, errors.New("unexpected get")
	}
	return s.get(ctx, identity, id)
}
func (s conversationServiceFuncs) List(ctx context.Context, identity auth.Identity) ([]conversation.Conversation, error) {
	if s.list == nil {
		return nil, errors.New("unexpected list")
	}
	return s.list(ctx, identity)
}
func (s conversationServiceFuncs) Delete(ctx context.Context, identity auth.Identity, id string) error {
	if s.delete == nil {
		return errors.New("unexpected delete")
	}
	return s.delete(ctx, identity, id)
}
func (s conversationServiceFuncs) UpdateTitle(ctx context.Context, identity auth.Identity, id string, title string) (conversation.Conversation, error) {
	if s.update == nil {
		return conversation.Conversation{}, errors.New("unexpected update")
	}
	return s.update(ctx, identity, id, title)
}
func (s conversationServiceFuncs) PrepareTurn(ctx context.Context, identity auth.Identity, id string, prompt string) (conversation.TurnExecution, error) {
	if s.prepare == nil {
		return nil, errors.New("unexpected prepare")
	}
	return s.prepare(ctx, identity, id, prompt)
}

type testTurnExecution struct {
	run      func(context.Context, agent.EmitFunc) error
	canceled bool
}

func (e *testTurnExecution) Run(ctx context.Context, emit agent.EmitFunc) error {
	return e.run(ctx, emit)
}
func (e *testTurnExecution) Cancel(string) error {
	e.canceled = true
	return nil
}

func newConversationTestHandler(t *testing.T, service ConversationService) http.Handler {
	t.Helper()
	handler, err := NewHandler(
		askerFunc(func(context.Context, string, agent.EmitFunc) error { return nil }),
		service, allowTestTokenVerifier(),
		HandlerConfig{MaxConcurrentAsks: 4, AuditLogger: discardAuditLogger()},
	)
	if err != nil {
		t.Fatalf("create conversation test handler: %v", err)
	}
	return handler
}

func authenticatedRequest(method string, path string, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer token")
	return request
}
