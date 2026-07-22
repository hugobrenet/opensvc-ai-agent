package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hugobrenet/opensvc-ai-agent/internal/agent"
	"github.com/hugobrenet/opensvc-ai-agent/internal/auth"
	"github.com/hugobrenet/opensvc-ai-agent/internal/llm"
)

type askerFunc func(context.Context, string, agent.EmitFunc) error

func (f askerFunc) Ask(ctx context.Context, prompt string, emit agent.EmitFunc) error {
	return f(ctx, prompt, emit)
}

func TestAskStreamsAgentEventsWithDelegatedJWT(t *testing.T) {
	const token = "test-jwt-marker"
	var called bool
	handler := newTestHandler(t, askerFunc(func(ctx context.Context, prompt string, emit agent.EmitFunc) error {
		called = true
		if got, ok := auth.BearerTokenFromContext(ctx); !ok || got != token {
			t.Fatalf("delegated JWT = %q, %v", got, ok)
		}
		if prompt != "health of my cluster" {
			t.Fatalf("got prompt %q", prompt)
		}
		for _, event := range []agent.Event{
			{Type: agent.EventTextDelta, TextDelta: "checking", Iteration: 1},
			{Type: agent.EventToolStarted, ToolName: "get_cluster_health", Iteration: 1},
			{Type: agent.EventToolFinished, ToolName: "get_cluster_health", Iteration: 1},
			{Type: agent.EventUsage, Usage: &llm.Usage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12}, Iteration: 1},
			{Type: agent.EventCompleted, FinishReason: llm.FinishReasonCompleted, Iteration: 2},
		} {
			if err := emit(event); err != nil {
				return err
			}
		}
		return nil
	}))

	request := httptest.NewRequest(http.MethodPost, "/v1/ask", strings.NewReader(`{"prompt":"health of my cluster"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if !called {
		t.Fatal("agent was not called")
	}
	if response.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Content-Type"); got != "text/event-stream; charset=utf-8" {
		t.Fatalf("got Content-Type %q", got)
	}
	events := decodeSSEEvents(t, response.Body.String())
	if len(events) != 5 {
		t.Fatalf("got %d events: %#v", len(events), events)
	}
	if events[0].Type != "text_delta" || events[0].TextDelta != "checking" || events[0].Iteration != 1 {
		t.Fatalf("unexpected text event %#v", events[0])
	}
	if events[1].Type != "tool_started" || events[1].ToolName != "get_cluster_health" {
		t.Fatalf("unexpected tool started event %#v", events[1])
	}
	if events[2].ToolError == nil || *events[2].ToolError {
		t.Fatalf("unexpected tool finished event %#v", events[2])
	}
	if events[3].Usage == nil || events[3].Usage.TotalTokens != 12 {
		t.Fatalf("unexpected usage event %#v", events[3])
	}
	if events[4].FinishReason != string(llm.FinishReasonCompleted) {
		t.Fatalf("unexpected completed event %#v", events[4])
	}
	if strings.Contains(response.Body.String(), token) {
		t.Fatal("response body exposes delegated JWT")
	}
}

func TestAskRejectsInvalidRequestsBeforeCallingAgent(t *testing.T) {
	for _, test := range []struct {
		name          string
		authorization string
		contentType   string
		body          string
		wantStatus    int
		wantCode      string
	}{
		{name: "missing authorization", contentType: "application/json", body: `{"prompt":"health"}`, wantStatus: http.StatusUnauthorized, wantCode: "unauthorized"},
		{name: "wrong scheme", authorization: "Basic value", contentType: "application/json", body: `{"prompt":"health"}`, wantStatus: http.StatusUnauthorized, wantCode: "unauthorized"},
		{name: "missing content type", authorization: "Bearer token", body: `{"prompt":"health"}`, wantStatus: http.StatusUnsupportedMediaType, wantCode: "unsupported_media_type"},
		{name: "invalid JSON", authorization: "Bearer token", contentType: "application/json", body: `{`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "unknown field", authorization: "Bearer token", contentType: "application/json", body: `{"prompt":"health","extra":true}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "multiple values", authorization: "Bearer token", contentType: "application/json", body: `{"prompt":"health"}{}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "empty prompt", authorization: "Bearer token", contentType: "application/json", body: `{"prompt":"  "}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_prompt"},
		{name: "large prompt", authorization: "Bearer token", contentType: "application/json", body: `{"prompt":"` + strings.Repeat("x", maxPromptBytes+1) + `"}`, wantStatus: http.StatusRequestEntityTooLarge, wantCode: "prompt_too_large"},
		{name: "large request", authorization: "Bearer token", contentType: "application/json", body: `{"prompt":"health","padding":"` + strings.Repeat("x", maxAskRequestBytes) + `"}`, wantStatus: http.StatusRequestEntityTooLarge, wantCode: "request_too_large"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			handler := newTestHandler(t, askerFunc(func(context.Context, string, agent.EmitFunc) error {
				calls.Add(1)
				return nil
			}))
			request := httptest.NewRequest(http.MethodPost, "/v1/ask", strings.NewReader(test.body))
			if test.authorization != "" {
				request.Header.Set("Authorization", test.authorization)
			}
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("got status %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			var errorResponse ErrorResponse
			if err := json.NewDecoder(response.Body).Decode(&errorResponse); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if errorResponse.Error.Code != test.wantCode {
				t.Fatalf("got error code %q, want %q", errorResponse.Error.Code, test.wantCode)
			}
			if calls.Load() != 0 {
				t.Fatalf("agent called %d times", calls.Load())
			}
		})
	}
}

func TestAskStreamsGenericRuntimeError(t *testing.T) {
	const sensitiveDetail = "provider failed with secret detail"
	handler := newTestHandler(t, askerFunc(func(context.Context, string, agent.EmitFunc) error {
		return errors.New(sensitiveDetail)
	}))
	request := httptest.NewRequest(http.MethodPost, "/v1/ask", strings.NewReader(`{"prompt":"health"}`))
	request.Header.Set("Authorization", "Bearer token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("got status %d, want streaming status 200", response.Code)
	}
	events := decodeSSEEvents(t, response.Body.String())
	if len(events) != 1 || events[0].Type != "error" || events[0].Code != "agent_failed" {
		t.Fatalf("unexpected error events %#v", events)
	}
	if strings.Contains(response.Body.String(), sensitiveDetail) {
		t.Fatal("stream exposes internal agent error")
	}
}

func TestAskDoesNotEmitAfterCompleted(t *testing.T) {
	handler := newTestHandler(t, askerFunc(func(_ context.Context, _ string, emit agent.EmitFunc) error {
		if err := emit(agent.Event{Type: agent.EventCompleted, FinishReason: llm.FinishReasonCompleted, Iteration: 1}); err != nil {
			return err
		}
		return errors.New("session close failed")
	}))
	request := httptest.NewRequest(http.MethodPost, "/v1/ask", strings.NewReader(`{"prompt":"health"}`))
	request.Header.Set("Authorization", "Bearer token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	events := decodeSSEEvents(t, response.Body.String())
	if len(events) != 1 || events[0].Type != "completed" {
		t.Fatalf("unexpected terminal events %#v", events)
	}
}

func TestAskDoesNotStreamErrorAfterCancellation(t *testing.T) {
	handler := newTestHandler(t, askerFunc(func(ctx context.Context, _ string, _ agent.EmitFunc) error {
		return ctx.Err()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodPost, "/v1/ask", strings.NewReader(`{"prompt":"health"}`)).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if events := decodeSSEEvents(t, response.Body.String()); len(events) != 0 {
		t.Fatalf("got events after request cancellation: %#v", events)
	}
}

func TestWriteSSEEnforcesStreamBound(t *testing.T) {
	response := httptest.NewRecorder()
	written := int64(maxAskStreamBytes)
	err := writeSSE(response, response, &written, AskEvent{Type: "error", Code: "test"})
	if err == nil || !strings.Contains(err.Error(), "stream exceeds") {
		t.Fatalf("writeSSE() error = %v", err)
	}
	if response.Body.Len() != 0 {
		t.Fatalf("writeSSE() wrote %d bytes beyond limit", response.Body.Len())
	}
}

func TestNewHandlerRejectsNilAgent(t *testing.T) {
	if _, err := NewHandler(nil); err == nil {
		t.Fatal("NewHandler(nil) succeeded")
	}
}

func newTestHandler(t *testing.T, asker Asker) http.Handler {
	t.Helper()
	if asker == nil {
		asker = askerFunc(func(context.Context, string, agent.EmitFunc) error { return nil })
	}
	handler, err := NewHandler(asker)
	if err != nil {
		t.Fatalf("create API handler: %v", err)
	}
	return handler
}

func decodeSSEEvents(t *testing.T, body string) []AskEvent {
	t.Helper()
	var events []AskEvent
	var eventName string
	var data strings.Builder
	dispatch := func() {
		if data.Len() == 0 {
			return
		}
		var event AskEvent
		if err := json.Unmarshal([]byte(data.String()), &event); err != nil {
			t.Fatalf("decode SSE data %q: %v", data.String(), err)
		}
		if event.Type != eventName {
			t.Fatalf("SSE event name %q differs from payload type %q", eventName, event.Type)
		}
		events = append(events, event)
		eventName = ""
		data.Reset()
	}
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			dispatch()
			continue
		}
		if value, ok := strings.CutPrefix(line, "event: "); ok {
			eventName = value
		}
		if value, ok := strings.CutPrefix(line, "data: "); ok {
			data.WriteString(value)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan SSE body: %v", err)
	}
	dispatch()
	return events
}
