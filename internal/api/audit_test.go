package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hugobrenet/opensvc-ai-agent/internal/agent"
	"github.com/hugobrenet/opensvc-ai-agent/internal/auth"
	"github.com/hugobrenet/opensvc-ai-agent/internal/llm"
)

func TestAuditRecordsAskLifecycleWithoutSensitiveData(t *testing.T) {
	const (
		tokenMarker  = "sensitive-jwt-marker"
		promptMarker = "sensitive-prompt-marker"
		textMarker   = "sensitive-model-text-marker"
		callerID     = "caller-controlled-request-id"
	)
	var output bytes.Buffer
	handler := newAuditTestHandler(t, &output, askerFunc(func(_ context.Context, prompt string, emit agent.EmitFunc) error {
		if prompt != promptMarker {
			t.Fatalf("prompt = %q", prompt)
		}
		for _, event := range []agent.Event{
			{Type: agent.EventTextDelta, TextDelta: textMarker, Iteration: 1},
			{Type: agent.EventToolStarted, ToolName: "get_cluster_health", Iteration: 1},
			{Type: agent.EventToolFinished, ToolName: "get_cluster_health", ToolError: true, Iteration: 1},
			{Type: agent.EventUsage, Usage: &llm.Usage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12}, Iteration: 1},
			{Type: agent.EventCompleted, FinishReason: llm.FinishReasonCompleted, Iteration: 2},
		} {
			if err := emit(event); err != nil {
				return err
			}
		}
		return nil
	}), tokenVerifierFunc(func(context.Context, string) (auth.Identity, error) {
		return auth.Identity{Subject: "alice\noperator", Issuer: "node-a"}, nil
	}))

	request := httptest.NewRequest(http.MethodPost, "/v1/ask", strings.NewReader(`{"prompt":"`+promptMarker+`"}`))
	request.Header.Set("Authorization", "Bearer "+tokenMarker)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(requestIDHeader, callerID)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	requestID := response.Header().Get(requestIDHeader)
	if len(requestID) != requestIDBytes*2 || requestID == callerID {
		t.Fatalf("server request ID = %q", requestID)
	}
	for _, marker := range []string{tokenMarker, promptMarker, textMarker, callerID} {
		if strings.Contains(output.String(), marker) {
			t.Fatalf("audit log exposes marker %q: %s", marker, output.String())
		}
	}

	events := decodeAuditEvents(t, output.String())
	wantEvents := []string{"ask_started", "tool_started", "tool_finished", "llm_usage", "ask_completed"}
	if len(events) != len(wantEvents) {
		t.Fatalf("audit events = %#v", events)
	}
	for index, want := range wantEvents {
		if events[index]["event"] != want {
			t.Fatalf("audit event %d = %v, want %q", index, events[index]["event"], want)
		}
		if events[index]["request_id"] != requestID {
			t.Fatalf("audit request ID = %v, want %q", events[index]["request_id"], requestID)
		}
		if events[index]["subject"] != "alice operator" || events[index]["issuer"] != "node-a" {
			t.Fatalf("audit identity = %#v", events[index])
		}
	}
	if events[2]["tool_name"] != "get_cluster_health" || events[2]["tool_error"] != true {
		t.Fatalf("tool audit = %#v", events[2])
	}
	if events[3]["total_tokens"] != float64(12) {
		t.Fatalf("usage audit = %#v", events[3])
	}
	completed := events[4]
	if completed["iterations"] != float64(2) || completed["tool_calls"] != float64(1) || completed["total_tokens"] != float64(12) {
		t.Fatalf("completed audit = %#v", completed)
	}
}

func TestAuditRecordsAuthenticationRejectionWithoutTokenOrVerifierError(t *testing.T) {
	const (
		tokenMarker = "rejected-jwt-marker"
		errorMarker = "sensitive-verifier-error"
	)
	var output bytes.Buffer
	handler := newAuditTestHandler(t, &output, askerFunc(func(context.Context, string, agent.EmitFunc) error {
		t.Fatal("agent was called")
		return nil
	}), tokenVerifierFunc(func(context.Context, string) (auth.Identity, error) {
		return auth.Identity{}, errors.New(errorMarker)
	}))
	request := httptest.NewRequest(http.MethodPost, "/v1/ask", nil)
	request.Header.Set("Authorization", "Bearer "+tokenMarker)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
	events := decodeAuditEvents(t, output.String())
	if len(events) != 1 || events[0]["event"] != "auth_rejected" || events[0]["code"] != "unauthorized" {
		t.Fatalf("audit events = %#v", events)
	}
	if events[0]["request_id"] != response.Header().Get(requestIDHeader) {
		t.Fatalf("audit request ID = %v", events[0]["request_id"])
	}
	for _, marker := range []string{tokenMarker, errorMarker} {
		if strings.Contains(output.String(), marker) {
			t.Fatalf("audit log exposes marker %q: %s", marker, output.String())
		}
	}
}

func TestAuditRecordsStableAgentFailureCodeWithoutRawError(t *testing.T) {
	const errorMarker = "sensitive-provider-error"
	var output bytes.Buffer
	handler := newAuditTestHandler(t, &output, askerFunc(func(context.Context, string, agent.EmitFunc) error {
		return errors.New(errorMarker)
	}), allowTestTokenVerifier())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, newAuthenticatedAskRequest(`{"prompt":"health"}`))

	events := decodeAuditEvents(t, output.String())
	if len(events) != 2 || events[1]["event"] != "ask_failed" || events[1]["code"] != "agent_failed" {
		t.Fatalf("audit events = %#v", events)
	}
	if strings.Contains(output.String(), errorMarker) {
		t.Fatalf("audit log exposes raw agent error: %s", output.String())
	}
}

func TestAuditRecordsValidatedRequestRejection(t *testing.T) {
	var output bytes.Buffer
	handler := newAuditTestHandler(t, &output, askerFunc(func(context.Context, string, agent.EmitFunc) error {
		t.Fatal("agent was called")
		return nil
	}), allowTestTokenVerifier())
	request := httptest.NewRequest(http.MethodPost, "/v1/ask", strings.NewReader(`{"prompt":"health"}`))
	request.Header.Set("Authorization", "Bearer token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	events := decodeAuditEvents(t, output.String())
	if len(events) != 1 || events[0]["event"] != "ask_rejected" || events[0]["code"] != "unsupported_media_type" {
		t.Fatalf("audit events = %#v", events)
	}
	if events[0]["subject"] != "test-user" || events[0]["status"] != float64(http.StatusUnsupportedMediaType) {
		t.Fatalf("rejection audit = %#v", events[0])
	}
}

func TestAskFailureCode(t *testing.T) {
	for _, test := range []struct {
		name       string
		err        error
		completed  bool
		writeError bool
		contextErr error
		want       string
	}{
		{name: "completed", completed: true},
		{name: "stream write", writeError: true, want: "stream_write_failed"},
		{name: "deadline error", err: context.DeadlineExceeded, want: "request_timeout"},
		{name: "deadline context", contextErr: context.DeadlineExceeded, want: "request_timeout"},
		{name: "canceled error", err: context.Canceled, want: "request_canceled"},
		{name: "canceled context", contextErr: context.Canceled, want: "request_canceled"},
		{name: "cleanup", err: errors.New("close"), completed: true, want: "agent_cleanup_failed"},
		{name: "agent", err: errors.New("failed"), want: "agent_failed"},
		{name: "incomplete", want: "agent_incomplete"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := askFailureCode(test.err, test.completed, test.writeError, test.contextErr); got != test.want {
				t.Fatalf("askFailureCode() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestBoundedAuditIdentityNormalizesAndTruncates(t *testing.T) {
	value := " alice\n\t" + strings.Repeat("é", maxAuditIdentityRunes+1)
	got := boundedAuditIdentity(value)
	if strings.ContainsAny(got, "\n\t") || !strings.HasPrefix(got, "alice ") || !strings.HasSuffix(got, "…") {
		t.Fatalf("bounded identity = %q", got)
	}
}

func newAuditTestHandler(t *testing.T, output *bytes.Buffer, asker Asker, verifier auth.TokenVerifier) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(output, nil))
	handler, err := NewHandler(asker, verifier, HandlerConfig{MaxConcurrentAsks: 4, AuditLogger: logger})
	if err != nil {
		t.Fatalf("create audited API handler: %v", err)
	}
	return handler
}

func decodeAuditEvents(t *testing.T, output string) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	events := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode audit log %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}
