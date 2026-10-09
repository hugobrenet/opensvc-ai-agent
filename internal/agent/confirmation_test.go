package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/opensvc/ai-agent/internal/auth"
	"github.com/opensvc/ai-agent/internal/llm"
)

func boolPointer(value bool) *bool { return &value }

func destructiveTool(name string) *mcp.Tool {
	return &mcp.Tool{Name: name, Title: "Stop object", InputSchema: objectSchema(), Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPointer(true)}}
}

func callsStep(calls ...llm.ToolCall) llmStep {
	return func(_ llm.Request, emit llm.EmitFunc) error {
		events := make([]llm.Event, 0, len(calls)+1)
		for index := range calls {
			events = append(events, llm.Event{Type: llm.EventToolCall, ToolCall: &calls[index]})
		}
		return emitEvents(emit, append(events, llm.Event{Type: llm.EventCompleted, FinishReason: llm.FinishReasonToolCalls})...)
	}
}

func answerStep(t *testing.T, inspect func(llm.Request)) llmStep {
	return func(request llm.Request, emit llm.EmitFunc) error {
		if inspect != nil {
			inspect(request)
		}
		return emitEvents(emit,
			llm.Event{Type: llm.EventTextDelta, TextDelta: "done"},
			llm.Event{Type: llm.EventCompleted, FinishReason: llm.FinishReasonCompleted},
		)
	}
}

func lastToolResults(request llm.Request) []llm.ToolResult {
	return request.Messages[len(request.Messages)-1].ToolResults
}

func TestRequiresConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name        string
		annotations *mcp.ToolAnnotations
		want        bool
	}{
		{"no annotations is destructive by default", nil, true},
		{"read-only", &mcp.ToolAnnotations{ReadOnlyHint: true}, false},
		{"not read-only without destructive hint", &mcp.ToolAnnotations{}, true},
		{"explicitly not destructive", &mcp.ToolAnnotations{DestructiveHint: boolPointer(false)}, false},
		{"destructive", &mcp.ToolAnnotations{DestructiveHint: boolPointer(true)}, true},
	} {
		if got := requiresConfirmation(&mcp.Tool{Name: "t", Annotations: tc.annotations}); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRunTurnSuspendsOnADestructiveCallAfterTheCallsBeforeIt(t *testing.T) {
	session := &fakeSession{tools: []*mcp.Tool{
		{Name: "get_object_status", InputSchema: objectSchema()},
		destructiveTool("stop_object"),
	}}
	model := &scriptedLLM{t: t, steps: []llmStep{callsStep(
		llm.ToolCall{ID: "call-1", Name: "get_object_status", Arguments: json.RawMessage(`{"path":"web"}`)},
		llm.ToolCall{ID: "call-2", Name: "stop_object", Arguments: json.RawMessage(`{"path":"web"}`)},
	)}}
	agent := newTestAgent(t, model, session, 4)
	var events []Event
	result, err := agent.RunTurn(auth.WithBearerToken(t.Context(), "jwt-marker"), nil, "stop web", func(event Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Suspended == nil || result.Messages != nil {
		t.Fatalf("turn not suspended: %+v", result)
	}
	if len(session.calls) != 1 || session.calls[0].name != "get_object_status" {
		t.Fatalf("the destructive call ran or the call before it did not: %#v", session.calls)
	}
	pending, err := result.Suspended.PendingCall()
	if err != nil || pending.ID != "call-2" || string(pending.Arguments) != `{"path":"web"}` || result.Suspended.ToolTitle != "Stop object" {
		t.Fatalf("unexpected pending call %+v %v", result.Suspended, err)
	}
	for _, event := range events {
		if event.Type == EventCompleted || event.ToolName == "stop_object" {
			t.Fatalf("a suspended turn emitted %+v", event)
		}
	}
	if !session.closed {
		t.Fatal("MCP session not closed on suspension")
	}
}

func TestResumeTurnApprovedRunsTheExactPendingCallThenContinues(t *testing.T) {
	session := &fakeSession{tools: []*mcp.Tool{destructiveTool("stop_object")}}
	model := &scriptedLLM{t: t, steps: []llmStep{
		callsStep(llm.ToolCall{ID: "call-1", Name: "stop_object", Arguments: json.RawMessage(`{"path":"web"}`)}),
		answerStep(t, func(request llm.Request) {
			results := lastToolResults(request)
			if len(results) != 1 || results[0].CallID != "call-1" || results[0].IsError {
				t.Fatalf("the model did not receive the executed result: %#v", results)
			}
		}),
	}}
	agent := newTestAgent(t, model, session, 4)
	first, err := agent.RunTurn(t.Context(), nil, "stop web", func(Event) error { return nil })
	if err != nil || first.Suspended == nil {
		t.Fatalf("got %+v %v", first, err)
	}
	encoded, err := json.Marshal(first.Suspended)
	if err != nil {
		t.Fatal(err)
	}
	var stored SuspendedTurn
	if err := json.Unmarshal(encoded, &stored); err != nil {
		t.Fatal(err)
	}
	var events []Event
	result, err := agent.ResumeTurn(t.Context(), nil, stored, DecisionApprove, func(event Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(session.calls) != 1 || session.calls[0].name != "stop_object" || session.calls[0].arguments["path"] != "web" {
		t.Fatalf("unexpected MCP calls: %#v", session.calls)
	}
	if result.Suspended != nil || result.FinishReason != llm.FinishReasonCompleted || len(result.Messages) != 4 ||
		result.Messages[0].Text != "stop web" || result.Messages[3].Text != "done" {
		t.Fatalf("unexpected resumed result: %+v", result)
	}
	if len(events) == 0 || events[0].Type != EventToolStarted || events[len(events)-1].Type != EventCompleted {
		t.Fatalf("unexpected resumed events: %#v", events)
	}
}

func TestResumeTurnRejectedTellsTheModelWithoutRunning(t *testing.T) {
	session := &fakeSession{tools: []*mcp.Tool{destructiveTool("stop_object")}}
	model := &scriptedLLM{t: t, steps: []llmStep{
		callsStep(llm.ToolCall{ID: "call-1", Name: "stop_object", Arguments: json.RawMessage(`{"path":"web"}`)}),
		answerStep(t, func(request llm.Request) {
			results := lastToolResults(request)
			if len(results) != 1 || !results[0].IsError || !strings.Contains(string(results[0].Content), "rejected") {
				t.Fatalf("the model was not told of the rejection: %#v", results)
			}
		}),
	}}
	agent := newTestAgent(t, model, session, 4)
	first, err := agent.RunTurn(t.Context(), nil, "stop web", func(Event) error { return nil })
	if err != nil || first.Suspended == nil {
		t.Fatalf("got %+v %v", first, err)
	}
	result, err := agent.ResumeTurn(t.Context(), nil, *first.Suspended, DecisionReject, func(Event) error { return nil })
	if err != nil || result.FinishReason != llm.FinishReasonCompleted {
		t.Fatalf("got %+v %v", result, err)
	}
	if len(session.calls) != 0 {
		t.Fatalf("a rejected call ran: %#v", session.calls)
	}
}

func TestResumeTurnSuspendsAgainOnTheNextDestructiveCall(t *testing.T) {
	session := &fakeSession{tools: []*mcp.Tool{destructiveTool("stop_object")}}
	model := &scriptedLLM{t: t, steps: []llmStep{callsStep(
		llm.ToolCall{ID: "call-1", Name: "stop_object", Arguments: json.RawMessage(`{"path":"a"}`)},
		llm.ToolCall{ID: "call-2", Name: "stop_object", Arguments: json.RawMessage(`{"path":"b"}`)},
	)}}
	agent := newTestAgent(t, model, session, 4)
	first, err := agent.RunTurn(t.Context(), nil, "stop a and b", func(Event) error { return nil })
	if err != nil || first.Suspended == nil {
		t.Fatalf("got %+v %v", first, err)
	}
	second, err := agent.ResumeTurn(t.Context(), nil, *first.Suspended, DecisionApprove, func(Event) error { return nil })
	if err != nil || second.Suspended == nil {
		t.Fatalf("got %+v %v", second, err)
	}
	pending, _ := second.Suspended.PendingCall()
	if len(session.calls) != 1 || session.calls[0].arguments["path"] != "a" || pending.ID != "call-2" {
		t.Fatalf("approval did not cover exactly the first call: calls=%#v pending=%+v", session.calls, pending)
	}
}

func TestAskDoesNotRunACallRequiringConfirmation(t *testing.T) {
	session := &fakeSession{tools: []*mcp.Tool{destructiveTool("stop_object")}}
	model := &scriptedLLM{t: t, steps: []llmStep{
		callsStep(llm.ToolCall{ID: "call-1", Name: "stop_object", Arguments: json.RawMessage(`{"path":"web"}`)}),
		answerStep(t, func(request llm.Request) {
			results := lastToolResults(request)
			if len(results) != 1 || !results[0].IsError || !strings.Contains(string(results[0].Content), "conversation") {
				t.Fatalf("the model was not told the action needs a conversation: %#v", results)
			}
		}),
	}}
	agent := newTestAgent(t, model, session, 4)
	if err := agent.Ask(t.Context(), "stop web", func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(session.calls) != 0 {
		t.Fatalf("a call requiring confirmation ran from ask: %#v", session.calls)
	}
}

func TestRunTurnRunsANonDestructiveActionWithoutConfirmation(t *testing.T) {
	session := &fakeSession{tools: []*mcp.Tool{{Name: "freeze_object", InputSchema: objectSchema(), Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPointer(false)}}}}
	model := &scriptedLLM{t: t, steps: []llmStep{
		callsStep(llm.ToolCall{ID: "call-1", Name: "freeze_object", Arguments: json.RawMessage(`{"path":"web"}`)}),
		answerStep(t, nil),
	}}
	agent := newTestAgent(t, model, session, 4)
	result, err := agent.RunTurn(t.Context(), nil, "freeze web", func(Event) error { return nil })
	if err != nil || result.Suspended != nil || len(session.calls) != 1 {
		t.Fatalf("got %+v %v calls=%#v", result, err, session.calls)
	}
}

func TestResumeTurnRejectsAnInvalidDecisionOrStateBeforeMCP(t *testing.T) {
	session := &fakeSession{}
	agent := newTestAgent(t, &scriptedLLM{t: t}, session, 4)
	valid := SuspendedTurn{
		Messages: []llm.Message{{Role: llm.RoleUser, Text: "stop web"}}, Iteration: 1, TotalToolCalls: 1,
		Calls: []llm.ToolCall{{ID: "call-1", Name: "stop_object", Arguments: json.RawMessage(`{}`)}},
	}
	for name, tc := range map[string]struct {
		state    SuspendedTurn
		decision Decision
	}{
		"unknown decision":    {valid, Decision("maybe")},
		"no pending call":     {SuspendedTurn{Messages: valid.Messages, Iteration: 1, TotalToolCalls: 1, Calls: valid.Calls, Results: []llm.ToolResult{{CallID: "call-1"}}}, DecisionApprove},
		"no user message":     {SuspendedTurn{Iteration: 1, TotalToolCalls: 1, Calls: valid.Calls}, DecisionApprove},
		"iteration exhausted": {SuspendedTurn{Messages: valid.Messages, Iteration: 4, TotalToolCalls: 1, Calls: valid.Calls}, DecisionApprove},
	} {
		if _, err := agent.ResumeTurn(t.Context(), nil, tc.state, tc.decision, func(Event) error { return nil }); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	if session.closed || len(session.calls) != 0 {
		t.Fatal("an invalid resume reached MCP")
	}
}
