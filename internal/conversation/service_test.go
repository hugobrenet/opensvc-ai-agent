package conversation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hugobrenet/opensvc-ai-agent/internal/agent"
	"github.com/hugobrenet/opensvc-ai-agent/internal/auth"
	"github.com/hugobrenet/opensvc-ai-agent/internal/llm"
)

var serviceTestNow = time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)

func TestServiceCompletesTurnBeforeEmittingCompletion(t *testing.T) {
	store := newServiceTestStore()
	runner := turnRunnerFunc(func(_ context.Context, history []llm.Message, prompt string, emit agent.EmitFunc) (agent.TurnResult, error) {
		if len(history) != 2 || prompt != "status now" {
			t.Fatalf("unexpected runner input history=%#v prompt=%q", history, prompt)
		}
		if err := emit(agent.Event{Type: agent.EventTextDelta, TextDelta: "healthy", Iteration: 1}); err != nil {
			return agent.TurnResult{}, err
		}
		if err := emit(agent.Event{Type: agent.EventCompleted, FinishReason: llm.FinishReasonCompleted, Iteration: 1}); err != nil {
			return agent.TurnResult{}, err
		}
		return agent.TurnResult{Messages: []llm.Message{
			{Role: llm.RoleUser, Text: prompt},
			{Role: llm.RoleAssistant, Text: "healthy"},
		}, FinishReason: llm.FinishReasonCompleted}, nil
	})
	service := newTestService(t, store, runner)
	store.history = []llm.Message{{Role: llm.RoleUser, Text: "previous"}, {Role: llm.RoleAssistant, Text: "answer"}}

	execution, err := service.PrepareTurn(t.Context(), serviceTestIdentity(), store.item.ID, "status now")
	if err != nil {
		t.Fatalf("prepare turn: %v", err)
	}
	var eventTypes []agent.EventType
	if err := execution.Run(t.Context(), func(event agent.Event) error {
		if event.Type == agent.EventCompleted && !store.completed {
			t.Fatal("completion emitted before messages were committed")
		}
		eventTypes = append(eventTypes, event.Type)
		return nil
	}); err != nil {
		t.Fatalf("run turn: %v", err)
	}
	if !store.completed || store.failed {
		t.Fatalf("completed=%t failed=%t", store.completed, store.failed)
	}
	if len(eventTypes) != 2 || eventTypes[0] != agent.EventTextDelta || eventTypes[1] != agent.EventCompleted {
		t.Fatalf("unexpected events %v", eventTypes)
	}
	if got := store.item.ExpiresAt; !got.Equal(serviceTestNow.Add(DefaultLifetime)) {
		t.Fatalf("got sliding expiry %s, want %s", got, serviceTestNow.Add(DefaultLifetime))
	}
}

func TestServiceFailedTurnDoesNotPersistPartialMessages(t *testing.T) {
	store := newServiceTestStore()
	runner := turnRunnerFunc(func(_ context.Context, _ []llm.Message, _ string, emit agent.EmitFunc) (agent.TurnResult, error) {
		if err := emit(agent.Event{Type: agent.EventTextDelta, TextDelta: "partial", Iteration: 1}); err != nil {
			return agent.TurnResult{}, err
		}
		return agent.TurnResult{}, errors.New("provider unavailable")
	})
	service := newTestService(t, store, runner)
	execution, err := service.PrepareTurn(t.Context(), serviceTestIdentity(), store.item.ID, "status")
	if err != nil {
		t.Fatalf("prepare turn: %v", err)
	}
	if err := execution.Run(t.Context(), func(agent.Event) error { return nil }); err == nil {
		t.Fatal("run turn succeeded")
	}
	if store.completed || !store.failed || store.failureCode != "agent_failed" || len(store.completedMessages) != 0 {
		t.Fatalf("unexpected terminal state completed=%t failed=%t code=%q messages=%#v", store.completed, store.failed, store.failureCode, store.completedMessages)
	}
}

func TestServiceRejectsExpiredAndBusyConversation(t *testing.T) {
	store := newServiceTestStore()
	service := newTestService(t, store, turnRunnerFunc(func(context.Context, []llm.Message, string, agent.EmitFunc) (agent.TurnResult, error) {
		return agent.TurnResult{}, nil
	}))
	store.item.ExpiresAt = serviceTestNow
	if _, err := service.PrepareTurn(t.Context(), serviceTestIdentity(), store.item.ID, "status"); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired turn error=%v", err)
	}
	store.item.ExpiresAt = serviceTestNow.Add(time.Hour)
	store.beginErr = ErrBusy
	if _, err := service.PrepareTurn(t.Context(), serviceTestIdentity(), store.item.ID, "status"); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy turn error=%v", err)
	}
}

func TestBoundHistoryKeepsCompleteNewestTurns(t *testing.T) {
	history := []llm.Message{
		{Role: llm.RoleUser, Text: "old"}, {Role: llm.RoleAssistant, Text: "old answer"},
		{Role: llm.RoleUser, Text: "new"}, {Role: llm.RoleAssistant, Text: "new answer"},
	}
	bounded, err := boundHistory(history, 2, 1024)
	if err != nil {
		t.Fatalf("bound history: %v", err)
	}
	if len(bounded) != 2 || bounded[0].Text != "new" || bounded[1].Text != "new answer" {
		t.Fatalf("unexpected bounded history %#v", bounded)
	}
}

type turnRunnerFunc func(context.Context, []llm.Message, string, agent.EmitFunc) (agent.TurnResult, error)

func (f turnRunnerFunc) RunTurn(ctx context.Context, history []llm.Message, prompt string, emit agent.EmitFunc) (agent.TurnResult, error) {
	return f(ctx, history, prompt, emit)
}

type serviceTestStore struct {
	item              Conversation
	history           []llm.Message
	beginErr          error
	completed         bool
	failed            bool
	failureCode       string
	completedMessages []llm.Message
}

func newServiceTestStore() *serviceTestStore {
	return &serviceTestStore{item: Conversation{
		ID: "conversation-id", Owner: Owner{Issuer: "cluster", Subject: "user"},
		CreatedAt: serviceTestNow.Add(-time.Hour), UpdatedAt: serviceTestNow.Add(-time.Hour),
		ExpiresAt: serviceTestNow.Add(time.Hour),
	}}
}

func newTestService(t *testing.T, store *serviceTestStore, runner TurnRunner) *Service {
	t.Helper()
	service, err := NewService(store, runner, ServiceConfig{})
	if err != nil {
		t.Fatalf("create service: %v", err)
	}
	service.now = func() time.Time { return serviceTestNow }
	service.newID = func() (string, error) { return "turn-id", nil }
	return service
}

func serviceTestIdentity() auth.Identity {
	return auth.Identity{Issuer: "cluster", Subject: "user"}
}

func (s *serviceTestStore) CreateConversation(context.Context, Conversation) error { return nil }
func (s *serviceTestStore) GetConversation(_ context.Context, owner Owner, id string) (Conversation, error) {
	if owner != s.item.Owner || id != s.item.ID {
		return Conversation{}, ErrNotFound
	}
	return s.item, nil
}
func (s *serviceTestStore) ListConversations(context.Context, Owner, int) ([]Conversation, error) {
	return []Conversation{s.item}, nil
}
func (s *serviceTestStore) DeleteConversation(context.Context, Owner, string) error { return nil }
func (s *serviceTestStore) BeginTurn(context.Context, Owner, string, string, time.Time) (Turn, error) {
	if s.beginErr != nil {
		return Turn{}, s.beginErr
	}
	return Turn{ID: "turn-id", ConversationID: s.item.ID, Sequence: 1, Status: TurnRunning, StartedAt: serviceTestNow}, nil
}
func (s *serviceTestStore) CompleteTurn(_ context.Context, _ Owner, _ string, _ string, completedAt time.Time, expiresAt time.Time, messages []llm.Message) error {
	s.completed = true
	s.completedMessages = append([]llm.Message(nil), messages...)
	s.item.UpdatedAt = completedAt
	s.item.ExpiresAt = expiresAt
	return nil
}
func (s *serviceTestStore) FailTurn(_ context.Context, _ Owner, _ string, _ string, _ TurnStatus, code string, _ time.Time) error {
	s.failed = true
	s.failureCode = code
	return nil
}
func (s *serviceTestStore) LoadHistory(context.Context, Owner, string) ([]llm.Message, error) {
	return append([]llm.Message(nil), s.history...), nil
}
func (s *serviceTestStore) RecoverInterrupted(context.Context, time.Time) (int64, error) {
	return 0, nil
}
func (s *serviceTestStore) DeleteExpired(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}
func (s *serviceTestStore) Close() error { return nil }
