package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hugobrenet/opensvc-ai-agent/internal/auth"
	"github.com/hugobrenet/opensvc-ai-agent/internal/llm"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAskReturnsDirectAnswerAndExposesAllTools(t *testing.T) {
	session := &fakeSession{tools: []*mcp.Tool{
		{Name: "get_cluster_health", Description: "Get health", InputSchema: objectSchema()},
		{Name: "refresh_instance_status", Description: "Refresh status", InputSchema: objectSchema()},
	}}
	model := &scriptedLLM{t: t, inspectContext: func(ctx context.Context) {
		if _, ok := auth.BearerTokenFromContext(ctx); ok {
			t.Fatal("LLM context contains delegated JWT")
		}
	}, steps: []llmStep{func(request llm.Request, emit llm.EmitFunc) error {
		if len(request.Messages) != 2 || request.Messages[0].Role != llm.RoleSystem || request.Messages[0].Text != systemPrompt {
			t.Fatalf("unexpected initial messages: %#v", request.Messages)
		}
		if len(request.Tools) != 2 || request.Tools[1].Name != "refresh_instance_status" {
			t.Fatalf("all MCP tools were not exposed: %#v", request.Tools)
		}
		if strings.Contains(fmt.Sprintf("%#v", request), "jwt-marker") {
			t.Fatal("LLM request contains delegated JWT")
		}
		return emitEvents(emit,
			llm.Event{Type: llm.EventTextDelta, TextDelta: "cluster healthy"},
			llm.Event{Type: llm.EventUsage, Usage: &llm.Usage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12}},
			llm.Event{Type: llm.EventCompleted, FinishReason: llm.FinishReasonCompleted},
		)
	}}}
	agent := newTestAgent(t, model, session, 4)
	ctx := auth.WithBearerToken(t.Context(), "jwt-marker")
	var events []Event
	if err := agent.Ask(ctx, "health of my cluster", func(event Event) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatalf("Ask() error: %v", err)
	}
	if !session.connectedWithJWT || !session.closed {
		t.Fatalf("session JWT=%v closed=%v", session.connectedWithJWT, session.closed)
	}
	if len(session.calls) != 0 {
		t.Fatalf("unexpected MCP calls: %#v", session.calls)
	}
	if len(events) != 3 || events[0].TextDelta != "cluster healthy" || events[2].Type != EventCompleted {
		t.Fatalf("unexpected agent events: %#v", events)
	}
}

func TestAskExecutesToolAndReturnsResultToLLM(t *testing.T) {
	session := &fakeSession{
		tools: []*mcp.Tool{{Name: "get_cluster_health", Description: "Get health", InputSchema: objectSchema()}},
		results: map[string]*mcp.CallToolResult{
			"get_cluster_health": {StructuredContent: map[string]any{"status": "healthy"}},
		},
	}
	model := &scriptedLLM{t: t, steps: []llmStep{
		func(_ llm.Request, emit llm.EmitFunc) error {
			return emitEvents(emit,
				llm.Event{Type: llm.EventToolCall, ToolCall: &llm.ToolCall{ID: "call-1", Name: "get_cluster_health", Arguments: json.RawMessage(`{"node":"node1"}`)}},
				llm.Event{Type: llm.EventCompleted, FinishReason: llm.FinishReasonToolCalls},
			)
		},
		func(request llm.Request, emit llm.EmitFunc) error {
			if len(request.Messages) != 4 {
				t.Fatalf("got %d messages, want system, user, assistant and tool", len(request.Messages))
			}
			assistant := request.Messages[2]
			result := request.Messages[3]
			if len(assistant.ToolCalls) != 1 || len(result.ToolResults) != 1 || result.ToolResults[0].CallID != "call-1" {
				t.Fatalf("unexpected tool history: assistant=%#v result=%#v", assistant, result)
			}
			if !strings.Contains(string(result.ToolResults[0].Content), `"status":"healthy"`) {
				t.Fatalf("tool result not preserved: %s", result.ToolResults[0].Content)
			}
			return emitEvents(emit,
				llm.Event{Type: llm.EventTextDelta, TextDelta: "The cluster is healthy."},
				llm.Event{Type: llm.EventCompleted, FinishReason: llm.FinishReasonCompleted},
			)
		},
	}}
	agent := newTestAgent(t, model, session, 4)
	var events []Event
	if err := agent.Ask(auth.WithBearerToken(t.Context(), "jwt-marker"), "check health", func(event Event) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatalf("Ask() error: %v", err)
	}
	if len(session.calls) != 1 || session.calls[0].name != "get_cluster_health" || session.calls[0].arguments["node"] != "node1" {
		t.Fatalf("unexpected MCP calls: %#v", session.calls)
	}
	wantTypes := []EventType{EventToolStarted, EventToolFinished, EventTextDelta, EventCompleted}
	if len(events) != len(wantTypes) {
		t.Fatalf("got events %#v", events)
	}
	for index, want := range wantTypes {
		if events[index].Type != want {
			t.Fatalf("event %d type=%q, want %q", index, events[index].Type, want)
		}
	}
}

func TestAskReturnsFunctionalToolErrorToLLM(t *testing.T) {
	session := &fakeSession{
		tools: []*mcp.Tool{{Name: "get_cluster_health", InputSchema: objectSchema()}},
		results: map[string]*mcp.CallToolResult{
			"get_cluster_health": {IsError: true, StructuredContent: map[string]any{"error": "unavailable"}},
		},
	}
	model := &scriptedLLM{t: t, steps: []llmStep{
		toolCallStep("get_cluster_health", "call-1"),
		func(request llm.Request, emit llm.EmitFunc) error {
			result := request.Messages[len(request.Messages)-1].ToolResults[0]
			if !result.IsError {
				t.Fatal("functional MCP error was not returned to LLM")
			}
			return emitEvents(emit,
				llm.Event{Type: llm.EventTextDelta, TextDelta: "Health is unavailable."},
				llm.Event{Type: llm.EventCompleted, FinishReason: llm.FinishReasonCompleted},
			)
		},
	}}
	agent := newTestAgent(t, model, session, 3)
	var finished Event
	if err := agent.Ask(t.Context(), "health", func(event Event) error {
		if event.Type == EventToolFinished {
			finished = event
		}
		return nil
	}); err != nil {
		t.Fatalf("Ask() error: %v", err)
	}
	if !finished.ToolError {
		t.Fatalf("tool finished event did not report functional error: %#v", finished)
	}
}

func TestAskRejectsUnsafeToolRequests(t *testing.T) {
	for _, test := range []struct {
		name  string
		calls []llm.ToolCall
		want  string
	}{
		{name: "unknown tool", calls: []llm.ToolCall{{ID: "call-1", Name: "unknown", Arguments: json.RawMessage(`{}`)}}, want: "unknown MCP tool"},
		{name: "too many tools", calls: []llm.ToolCall{
			{ID: "1", Name: "get_cluster_health", Arguments: json.RawMessage(`{}`)},
			{ID: "2", Name: "get_cluster_health", Arguments: json.RawMessage(`{}`)},
			{ID: "3", Name: "get_cluster_health", Arguments: json.RawMessage(`{}`)},
			{ID: "4", Name: "get_cluster_health", Arguments: json.RawMessage(`{}`)},
			{ID: "5", Name: "get_cluster_health", Arguments: json.RawMessage(`{}`)},
		}, want: "maximum is 4"},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := &fakeSession{tools: []*mcp.Tool{{Name: "get_cluster_health", InputSchema: objectSchema()}}}
			model := &scriptedLLM{t: t, steps: []llmStep{func(_ llm.Request, emit llm.EmitFunc) error {
				for _, call := range test.calls {
					callCopy := call
					if err := emit(llm.Event{Type: llm.EventToolCall, ToolCall: &callCopy}); err != nil {
						return err
					}
				}
				return emit(llm.Event{Type: llm.EventCompleted, FinishReason: llm.FinishReasonToolCalls})
			}}}
			agent := newTestAgent(t, model, session, 3)
			err := agent.Ask(t.Context(), "health", func(Event) error { return nil })
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Ask() error = %v, want containing %q", err, test.want)
			}
			if len(session.calls) != 0 {
				t.Fatalf("unsafe request called MCP: %#v", session.calls)
			}
			if !session.closed {
				t.Fatal("MCP session was not closed")
			}
		})
	}
}

func TestAskEnforcesMaxIterationsBeforeCallingTool(t *testing.T) {
	session := &fakeSession{tools: []*mcp.Tool{{Name: "get_cluster_health", InputSchema: objectSchema()}}}
	model := &scriptedLLM{t: t, steps: []llmStep{toolCallStep("get_cluster_health", "call-1")}}
	agent := newTestAgent(t, model, session, 1)
	err := agent.Ask(t.Context(), "health", func(Event) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "maximum of 1 iterations") {
		t.Fatalf("Ask() error = %v", err)
	}
	if len(session.calls) != 0 {
		t.Fatalf("tool called without a remaining LLM iteration: %#v", session.calls)
	}
}

func TestAskPropagatesMCPAndConsumerErrorsAndClosesSession(t *testing.T) {
	consumerError := errors.New("stop")
	for _, test := range []struct {
		name       string
		model      *scriptedLLM
		session    *fakeSession
		emit       EmitFunc
		wantTarget error
	}{
		{
			name:    "MCP transport",
			model:   &scriptedLLM{t: t, steps: []llmStep{toolCallStep("get_cluster_health", "call-1")}},
			session: &fakeSession{tools: []*mcp.Tool{{Name: "get_cluster_health", InputSchema: objectSchema()}}, callErr: errors.New("transport failed")},
			emit:    func(Event) error { return nil },
		},
		{
			name: "consumer",
			model: &scriptedLLM{t: t, steps: []llmStep{func(_ llm.Request, emit llm.EmitFunc) error {
				return emit(llm.Event{Type: llm.EventTextDelta, TextDelta: "partial"})
			}}},
			session:    &fakeSession{},
			emit:       func(Event) error { return consumerError },
			wantTarget: consumerError,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			agent := newTestAgent(t, test.model, test.session, 3)
			err := agent.Ask(t.Context(), "health", test.emit)
			if err == nil {
				t.Fatal("Ask() succeeded, want error")
			}
			if test.wantTarget != nil && !errors.Is(err, test.wantTarget) {
				t.Fatalf("Ask() error = %v, want wrapping %v", err, test.wantTarget)
			}
			if !test.session.closed {
				t.Fatal("MCP session was not closed")
			}
		})
	}
}

type llmStep func(llm.Request, llm.EmitFunc) error

type scriptedLLM struct {
	t              *testing.T
	steps          []llmStep
	inspectContext func(context.Context)
	position       int
}

func (c *scriptedLLM) Stream(ctx context.Context, request llm.Request, emit llm.EmitFunc) error {
	c.t.Helper()
	if c.inspectContext != nil {
		c.inspectContext(ctx)
	}
	if c.position >= len(c.steps) {
		c.t.Fatalf("unexpected LLM iteration %d", c.position+1)
	}
	step := c.steps[c.position]
	c.position++
	return step(request, emit)
}

type recordedCall struct {
	name      string
	arguments map[string]any
}

type fakeSession struct {
	tools            []*mcp.Tool
	results          map[string]*mcp.CallToolResult
	callErr          error
	closeErr         error
	calls            []recordedCall
	closed           bool
	connectedWithJWT bool
}

func (s *fakeSession) ListTools(context.Context) ([]*mcp.Tool, error) {
	return s.tools, nil
}

func (s *fakeSession) CallTool(_ context.Context, name string, arguments map[string]any) (*mcp.CallToolResult, error) {
	s.calls = append(s.calls, recordedCall{name: name, arguments: arguments})
	if s.callErr != nil {
		return nil, s.callErr
	}
	if result := s.results[name]; result != nil {
		return result, nil
	}
	return &mcp.CallToolResult{}, nil
}

func (s *fakeSession) Close() error {
	s.closed = true
	return s.closeErr
}

func newTestAgent(t *testing.T, model llm.Client, session *fakeSession, maxIterations int) *Agent {
	t.Helper()
	agent, err := New(model, func(ctx context.Context) (MCPSession, error) {
		if token, ok := auth.BearerTokenFromContext(ctx); ok && token == "jwt-marker" {
			session.connectedWithJWT = true
		}
		return session, nil
	}, Config{MaxIterations: maxIterations})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return agent
}

func objectSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func toolCallStep(name string, callID string) llmStep {
	return func(_ llm.Request, emit llm.EmitFunc) error {
		return emitEvents(emit,
			llm.Event{Type: llm.EventToolCall, ToolCall: &llm.ToolCall{ID: callID, Name: name, Arguments: json.RawMessage(`{}`)}},
			llm.Event{Type: llm.EventCompleted, FinishReason: llm.FinishReasonToolCalls},
		)
	}
}

func emitEvents(emit llm.EmitFunc, events ...llm.Event) error {
	for _, event := range events {
		if err := emit(event); err != nil {
			return err
		}
	}
	return nil
}
