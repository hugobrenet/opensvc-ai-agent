package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/opensvc/ai-agent/internal/agent"
	"github.com/opensvc/ai-agent/internal/llm"
)

// confirmationRunner suspends the first turn on a stop_object call, then
// records the decision it is resumed with.
type confirmationRunner struct {
	resumedWith agent.Decision
	resumedCall llm.ToolCall
}

func suspendedStopTurn() agent.SuspendedTurn {
	return agent.SuspendedTurn{
		Messages: []llm.Message{{Role: llm.RoleUser, Text: "stop web"}}, Iteration: 1, TotalToolCalls: 1,
		Calls:     []llm.ToolCall{{ID: "call-1", Name: "stop_object", Arguments: json.RawMessage(`{"path":"web"}`)}},
		ToolTitle: "Stop object",
	}
}

func (r *confirmationRunner) RunTurn(context.Context, []llm.Message, string, agent.EmitFunc) (agent.TurnResult, error) {
	state := suspendedStopTurn()
	return agent.TurnResult{Suspended: &state}, nil
}

func (r *confirmationRunner) ResumeTurn(_ context.Context, _ []llm.Message, state agent.SuspendedTurn, decision agent.Decision, emit agent.EmitFunc) (agent.TurnResult, error) {
	r.resumedWith = decision
	r.resumedCall, _ = state.PendingCall()
	if err := emit(agent.Event{Type: agent.EventCompleted, FinishReason: llm.FinishReasonCompleted, Iteration: 2}); err != nil {
		return agent.TurnResult{}, err
	}
	return agent.TurnResult{Messages: append(state.Messages, llm.Message{Role: llm.RoleAssistant, Text: "stopped"}), FinishReason: llm.FinishReasonCompleted}, nil
}

func TestServiceStoresASuspendedTurnBeforeAskingForConfirmation(t *testing.T) {
	store := newServiceTestStore()
	runner := &confirmationRunner{}
	service := newTestService(t, store, runner)
	execution, err := service.PrepareTurn(t.Context(), serviceTestIdentity(), store.item.ID, "stop web")
	if err != nil {
		t.Fatal(err)
	}
	var request *agent.Confirmation
	if err := execution.Run(t.Context(), func(event agent.Event) error {
		if event.Type != agent.EventConfirmationRequired {
			t.Fatalf("unexpected event %+v", event)
		}
		if store.suspended == nil {
			t.Fatal("confirmation requested before the turn was stored")
		}
		request = event.Confirmation
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if request == nil || request.TurnID != "turn-id" || request.ToolName != "stop_object" || request.ToolTitle != "Stop object" ||
		string(request.Arguments) != `{"path":"web"}` || !request.ExpiresAt.Equal(serviceTestNow.Add(DefaultConfirmationLifetime)) {
		t.Fatalf("unexpected confirmation request %+v", request)
	}
	if store.completed || store.failed || store.suspended.ArgumentsHash != agent.ArgumentsHash(json.RawMessage(`{"path":"web"}`)) {
		t.Fatalf("unexpected stored state completed=%v failed=%v pending=%+v", store.completed, store.failed, store.suspended)
	}

	resumed, err := service.PrepareConfirmation(t.Context(), serviceTestIdentity(), store.item.ID, request.TurnID, request.ID, agent.DecisionApprove)
	if err != nil {
		t.Fatal(err)
	}
	if err := resumed.Run(t.Context(), func(agent.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if runner.resumedWith != agent.DecisionApprove || runner.resumedCall.ID != "call-1" || !store.completed || len(store.completedMessages) != 2 {
		t.Fatalf("resumed with %q call %+v completed=%v messages=%#v", runner.resumedWith, runner.resumedCall, store.completed, store.completedMessages)
	}
	if _, err := service.PrepareConfirmation(t.Context(), serviceTestIdentity(), store.item.ID, request.TurnID, request.ID, agent.DecisionApprove); !errors.Is(err, ErrNotPending) {
		t.Fatalf("a confirmation was used twice: %v", err)
	}
}

func TestServiceRefusesAnInvalidDecisionOrATamperedCall(t *testing.T) {
	store := newServiceTestStore()
	service := newTestService(t, store, &confirmationRunner{})
	if _, err := service.PrepareConfirmation(t.Context(), serviceTestIdentity(), store.item.ID, "turn-id", "turn-id", agent.Decision("yes")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an invalid decision was accepted: %v", err)
	}
	state, _ := json.Marshal(suspendedStopTurn())
	store.suspended = &PendingConfirmation{
		ConversationID: store.item.ID, TurnID: "turn-id", ConfirmationID: "confirmation-id", ToolName: "stop_object",
		Arguments: json.RawMessage(`{"path":"web"}`), ArgumentsHash: agent.ArgumentsHash(json.RawMessage(`{"path":"other"}`)),
		State: state, CreatedAt: serviceTestNow, ExpiresAt: serviceTestNow.Add(DefaultConfirmationLifetime),
	}
	if _, err := service.PrepareConfirmation(t.Context(), serviceTestIdentity(), store.item.ID, "turn-id", "confirmation-id", agent.DecisionApprove); err == nil || !store.failed || store.failureCode != "confirmation_invalid" {
		t.Fatalf("a call not matching its confirmation resumed: %v failed=%v code=%q", err, store.failed, store.failureCode)
	}
}
