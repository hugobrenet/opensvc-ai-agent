package conversation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/opensvc/ai-agent/internal/agent"
	"github.com/opensvc/ai-agent/internal/auth"
	"github.com/opensvc/ai-agent/internal/llm"
)

const (
	DefaultLifetime          = 7 * 24 * time.Hour
	DefaultListLimit         = 100
	DefaultFinalizeTimeout   = 5 * time.Second
	DefaultHistoryMessages   = 128
	DefaultHistoryBytes      = 1 << 20
	DefaultExpiryDeleteBatch = 100
	// DefaultConfirmationLifetime is how long a tool call waits for the
	// decision of the user before its turn fails without running it.
	DefaultConfirmationLifetime = 10 * time.Minute
	maxServicePromptBytes       = 32 << 10
)

type TurnRunner interface {
	RunTurn(context.Context, []llm.Message, string, agent.EmitFunc) (agent.TurnResult, error)
	ResumeTurn(context.Context, []llm.Message, agent.SuspendedTurn, agent.Decision, agent.EmitFunc) (agent.TurnResult, error)
}

type ServiceConfig struct {
	Lifetime           time.Duration
	ListLimit          int
	FinalizeTimeout    time.Duration
	MaxHistoryMessages int
	MaxHistoryBytes    int
	ExpiryDeleteBatch  int
	// ConfirmationLifetime bounds the wait for the decision of the user on
	// a tool call requiring confirmation.
	ConfirmationLifetime time.Duration
}

type Service struct {
	store  Store
	runner TurnRunner
	config ServiceConfig
	now    func() time.Time
	newID  func() (string, error)
}

func NewService(store Store, runner TurnRunner, config ServiceConfig) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("conversation store is nil")
	}
	if runner == nil {
		return nil, fmt.Errorf("conversation turn runner is nil")
	}
	config = withServiceDefaults(config)
	if config.Lifetime <= 0 || config.ListLimit <= 0 || config.FinalizeTimeout <= 0 ||
		config.MaxHistoryMessages <= 0 || config.MaxHistoryBytes <= 0 || config.ExpiryDeleteBatch <= 0 || config.ConfirmationLifetime <= 0 {
		return nil, fmt.Errorf("conversation service limits must be positive")
	}
	return &Service{store: store, runner: runner, config: config, now: time.Now, newID: randomID}, nil
}

func withServiceDefaults(config ServiceConfig) ServiceConfig {
	if config.Lifetime == 0 {
		config.Lifetime = DefaultLifetime
	}
	if config.ListLimit == 0 {
		config.ListLimit = DefaultListLimit
	}
	if config.FinalizeTimeout == 0 {
		config.FinalizeTimeout = DefaultFinalizeTimeout
	}
	if config.MaxHistoryMessages == 0 {
		config.MaxHistoryMessages = DefaultHistoryMessages
	}
	if config.MaxHistoryBytes == 0 {
		config.MaxHistoryBytes = DefaultHistoryBytes
	}
	if config.ExpiryDeleteBatch == 0 {
		config.ExpiryDeleteBatch = DefaultExpiryDeleteBatch
	}
	if config.ConfirmationLifetime == 0 {
		config.ConfirmationLifetime = DefaultConfirmationLifetime
	}
	return config
}

func (s *Service) Create(ctx context.Context, identity auth.Identity) (Conversation, error) {
	owner, err := ownerFromIdentity(identity)
	if err != nil {
		return Conversation{}, err
	}
	now := s.now().UTC()
	if _, err := s.purgeExpired(ctx, now); err != nil {
		return Conversation{}, fmt.Errorf("delete expired conversations before creation: %w", err)
	}
	id, err := s.newID()
	if err != nil {
		return Conversation{}, fmt.Errorf("generate conversation ID: %w", err)
	}
	item := Conversation{
		ID: id, Owner: owner, CreatedAt: now, UpdatedAt: now,
		ExpiresAt: now.Add(s.config.Lifetime),
	}
	if err := s.store.CreateConversation(ctx, item); err != nil {
		return Conversation{}, err
	}
	return item, nil
}

func (s *Service) Get(ctx context.Context, identity auth.Identity, id string) (Conversation, error) {
	owner, err := ownerFromIdentity(identity)
	if err != nil {
		return Conversation{}, err
	}
	item, err := s.store.GetConversation(ctx, owner, id)
	if err != nil {
		return Conversation{}, err
	}
	if !item.ExpiresAt.After(s.now().UTC()) {
		return Conversation{}, ErrExpired
	}
	return item, nil
}

func (s *Service) List(ctx context.Context, identity auth.Identity) ([]Conversation, error) {
	owner, err := ownerFromIdentity(identity)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	if _, err := s.purgeExpired(ctx, now); err != nil {
		return nil, fmt.Errorf("delete expired conversations before listing: %w", err)
	}
	items, err := s.store.ListConversations(ctx, owner, s.config.ListLimit)
	if err != nil {
		return nil, err
	}
	active := make([]Conversation, 0, len(items))
	for _, item := range items {
		if item.ExpiresAt.After(now) {
			active = append(active, item)
		}
	}
	return active, nil
}

func (s *Service) Delete(ctx context.Context, identity auth.Identity, id string) error {
	item, err := s.Get(ctx, identity, id)
	if err != nil {
		return err
	}
	return s.store.DeleteConversation(ctx, item.Owner, item.ID)
}

func (s *Service) UpdateTitle(ctx context.Context, identity auth.Identity, id string, title string) (Conversation, error) {
	title, err := NormalizeTitle(title)
	if err != nil {
		return Conversation{}, err
	}
	item, err := s.Get(ctx, identity, id)
	if err != nil {
		return Conversation{}, err
	}
	return s.store.UpdateConversationTitle(ctx, item.Owner, item.ID, title, s.now().UTC())
}

type TurnExecution interface {
	Run(context.Context, agent.EmitFunc) error
	Cancel(string) error
}

func (s *Service) PrepareTurn(ctx context.Context, identity auth.Identity, conversationID string, prompt string) (TurnExecution, error) {
	if strings.TrimSpace(prompt) == "" || len(prompt) > maxServicePromptBytes {
		return nil, fmt.Errorf("%w: invalid conversation prompt", ErrInvalid)
	}
	item, err := s.Get(ctx, identity, conversationID)
	if err != nil {
		return nil, err
	}
	turnID, err := s.newID()
	if err != nil {
		return nil, fmt.Errorf("generate conversation turn ID: %w", err)
	}
	startedAt := s.now().UTC()
	turn, err := s.store.BeginTurn(ctx, item.Owner, item.ID, turnID, startedAt)
	if err != nil {
		return nil, err
	}
	history, err := s.store.LoadHistory(ctx, item.Owner, item.ID)
	if err != nil {
		return nil, errors.Join(err, s.failTurn(item.Owner, item.ID, turn.ID, TurnFailed, "history_failed"))
	}
	history, err = boundHistory(history, s.config.MaxHistoryMessages, s.config.MaxHistoryBytes)
	if err != nil {
		return nil, errors.Join(err, s.failTurn(item.Owner, item.ID, turn.ID, TurnFailed, "history_invalid"))
	}
	return &PreparedTurn{
		service: s, owner: item.Owner, conversationID: item.ID, turnID: turn.ID,
		prompt: prompt, history: history,
	}, nil
}

// PrepareConfirmation consumes the confirmation a suspended turn waits on and
// returns the execution that resumes the turn with the decision of the user.
// The decision applies to the call stored with the confirmation, never to
// arguments supplied with the decision.
func (s *Service) PrepareConfirmation(ctx context.Context, identity auth.Identity, conversationID string, turnID string, confirmationID string, decision agent.Decision) (TurnExecution, error) {
	if !decision.Valid() {
		return nil, fmt.Errorf("%w: invalid confirmation decision", ErrInvalid)
	}
	item, err := s.Get(ctx, identity, conversationID)
	if err != nil {
		return nil, err
	}
	pending, err := s.store.ClaimConfirmation(ctx, item.Owner, item.ID, turnID, confirmationID, s.now().UTC())
	if err != nil {
		return nil, err
	}
	var state agent.SuspendedTurn
	if err := json.Unmarshal(pending.State, &state); err != nil {
		return nil, errors.Join(fmt.Errorf("decode suspended turn: %w", err), s.failTurn(item.Owner, item.ID, turnID, TurnFailed, "confirmation_invalid"))
	}
	call, err := state.PendingCall()
	if err != nil || call.Name != pending.ToolName || agent.ArgumentsHash(call.Arguments) != pending.ArgumentsHash {
		return nil, errors.Join(fmt.Errorf("suspended turn does not hold the confirmed call"), s.failTurn(item.Owner, item.ID, turnID, TurnFailed, "confirmation_invalid"))
	}
	history, err := s.store.LoadHistory(ctx, item.Owner, item.ID)
	if err != nil {
		return nil, errors.Join(err, s.failTurn(item.Owner, item.ID, turnID, TurnFailed, "history_failed"))
	}
	history, err = boundHistory(history, s.config.MaxHistoryMessages, s.config.MaxHistoryBytes)
	if err != nil {
		return nil, errors.Join(err, s.failTurn(item.Owner, item.ID, turnID, TurnFailed, "history_invalid"))
	}
	return &PreparedTurn{
		service: s, owner: item.Owner, conversationID: item.ID, turnID: turnID,
		history: history, resume: &state, decision: decision,
	}, nil
}

// PendingConfirmation returns the unexpired confirmation the conversation
// waits on, or nil, for a client to show it again.
func (s *Service) PendingConfirmation(ctx context.Context, identity auth.Identity, conversationID string) (*PendingConfirmation, error) {
	item, err := s.Get(ctx, identity, conversationID)
	if err != nil {
		return nil, err
	}
	return s.store.GetPendingConfirmation(ctx, item.Owner, item.ID, s.now().UTC())
}

func (s *Service) Recover(ctx context.Context) (int64, error) {
	return s.store.RecoverInterrupted(ctx, s.now().UTC())
}

func (s *Service) DeleteExpired(ctx context.Context) (int64, error) {
	return s.purgeExpired(ctx, s.now().UTC())
}

func (s *Service) purgeExpired(ctx context.Context, at time.Time) (int64, error) {
	var total int64
	for {
		deleted, err := s.store.DeleteExpired(ctx, at, s.config.ExpiryDeleteBatch)
		if err != nil {
			return total, err
		}
		total += deleted
		if deleted < int64(s.config.ExpiryDeleteBatch) {
			return total, nil
		}
	}
}

type PreparedTurn struct {
	service        *Service
	owner          Owner
	conversationID string
	turnID         string
	prompt         string
	history        []llm.Message
	resume         *agent.SuspendedTurn
	decision       agent.Decision
	mu             sync.Mutex
	claimed        bool
}

func (t *PreparedTurn) Run(ctx context.Context, emit agent.EmitFunc) error {
	if err := t.claim(); err != nil {
		return err
	}
	if emit == nil {
		err := errors.New("conversation event consumer is nil")
		return errors.Join(err, t.service.failTurn(t.owner, t.conversationID, t.turnID, TurnFailed, "consumer_invalid"))
	}
	var completedEvent *agent.Event
	forward := func(event agent.Event) error {
		if event.Type == agent.EventCompleted {
			if completedEvent != nil {
				return errors.New("conversation runner emitted multiple completion events")
			}
			copy := event
			completedEvent = &copy
			return nil
		}
		if event.Type == agent.EventConfirmationRequired {
			return errors.New("conversation runner emitted a confirmation request")
		}
		return emit(event)
	}
	var result agent.TurnResult
	var runErr error
	if t.resume != nil {
		result, runErr = t.service.runner.ResumeTurn(ctx, t.history, *t.resume, t.decision, forward)
	} else {
		result, runErr = t.service.runner.RunTurn(ctx, t.history, t.prompt, forward)
	}
	if runErr != nil {
		status, code := turnFailure(runErr, ctx.Err())
		return errors.Join(runErr, t.service.failTurn(t.owner, t.conversationID, t.turnID, status, code))
	}
	if result.Suspended != nil {
		if completedEvent != nil {
			err := errors.New("conversation turn both completed and suspended")
			return errors.Join(err, t.service.failTurn(t.owner, t.conversationID, t.turnID, TurnFailed, "agent_incomplete"))
		}
		return t.suspend(*result.Suspended, emit)
	}
	if completedEvent == nil {
		err := errors.New("conversation turn completed without a completion event")
		return errors.Join(err, t.service.failTurn(t.owner, t.conversationID, t.turnID, TurnFailed, "agent_incomplete"))
	}
	if result.FinishReason != completedEvent.FinishReason {
		err := errors.New("conversation result finish reason does not match completion event")
		return errors.Join(err, t.service.failTurn(t.owner, t.conversationID, t.turnID, TurnFailed, "agent_incomplete"))
	}
	completedAt := t.service.now().UTC()
	finalizeCtx, cancel := context.WithTimeout(context.Background(), t.service.config.FinalizeTimeout)
	completeErr := t.service.store.CompleteTurn(
		finalizeCtx, t.owner, t.conversationID, t.turnID, completedAt,
		completedAt.Add(t.service.config.Lifetime), result.Messages,
	)
	cancel()
	if completeErr != nil {
		return errors.Join(completeErr, t.service.failTurn(t.owner, t.conversationID, t.turnID, TurnFailed, "persistence_failed"))
	}
	if err := emit(*completedEvent); err != nil {
		return fmt.Errorf("emit committed conversation completion: %w", err)
	}
	return nil
}

// suspend stores the turn waiting for a confirmation, then asks the client for
// the decision of the user. The request is emitted only once stored, so a
// client can always answer it.
func (t *PreparedTurn) suspend(state agent.SuspendedTurn, emit agent.EmitFunc) error {
	call, err := state.PendingCall()
	if err != nil {
		return errors.Join(err, t.service.failTurn(t.owner, t.conversationID, t.turnID, TurnFailed, "agent_incomplete"))
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return errors.Join(fmt.Errorf("encode suspended turn: %w", err), t.service.failTurn(t.owner, t.conversationID, t.turnID, TurnFailed, "agent_incomplete"))
	}
	confirmationID, err := t.service.newID()
	if err != nil {
		return errors.Join(fmt.Errorf("generate confirmation ID: %w", err), t.service.failTurn(t.owner, t.conversationID, t.turnID, TurnFailed, "agent_incomplete"))
	}
	createdAt := t.service.now().UTC()
	pending := PendingConfirmation{
		ConversationID: t.conversationID, TurnID: t.turnID, ConfirmationID: confirmationID,
		ToolName: call.Name, ToolTitle: state.ToolTitle, Arguments: call.Arguments,
		ArgumentsHash: agent.ArgumentsHash(call.Arguments), State: encoded,
		CreatedAt: createdAt, ExpiresAt: createdAt.Add(t.service.config.ConfirmationLifetime),
	}
	suspendCtx, cancel := context.WithTimeout(context.Background(), t.service.config.FinalizeTimeout)
	suspendErr := t.service.store.SuspendTurn(suspendCtx, t.owner, pending)
	cancel()
	if suspendErr != nil {
		return errors.Join(suspendErr, t.service.failTurn(t.owner, t.conversationID, t.turnID, TurnFailed, "persistence_failed"))
	}
	if err := emit(agent.Event{Type: agent.EventConfirmationRequired, Iteration: state.Iteration, Confirmation: &agent.Confirmation{
		ID: confirmationID, TurnID: t.turnID, ToolName: call.Name, ToolTitle: state.ToolTitle, Destructive: true,
		Arguments: call.Arguments, ExpiresAt: pending.ExpiresAt,
	}}); err != nil {
		return fmt.Errorf("emit stored confirmation request: %w", err)
	}
	return nil
}

func (t *PreparedTurn) Cancel(code string) error {
	if strings.TrimSpace(code) == "" {
		code = "turn_canceled"
	}
	if err := t.claim(); err != nil {
		return err
	}
	return t.service.failTurn(t.owner, t.conversationID, t.turnID, TurnCanceled, code)
}

func (t *PreparedTurn) claim() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.claimed {
		return fmt.Errorf("%w: conversation turn already consumed", ErrConflict)
	}
	t.claimed = true
	return nil
}

func (s *Service) failTurn(owner Owner, conversationID string, turnID string, status TurnStatus, code string) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.config.FinalizeTimeout)
	defer cancel()
	if err := s.store.FailTurn(ctx, owner, conversationID, turnID, status, code, s.now().UTC()); err != nil {
		return fmt.Errorf("record conversation turn failure: %w", err)
	}
	return nil
}

func turnFailure(err error, contextErr error) (TurnStatus, string) {
	if errors.Is(contextErr, context.Canceled) || errors.Is(err, context.Canceled) {
		return TurnCanceled, "request_canceled"
	}
	if errors.Is(contextErr, context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return TurnFailed, "request_timeout"
	}
	return TurnFailed, "agent_failed"
}

func ownerFromIdentity(identity auth.Identity) (Owner, error) {
	owner := Owner{ClusterID: strings.TrimSpace(identity.ClusterID), Issuer: strings.TrimSpace(identity.Issuer), Subject: strings.TrimSpace(identity.Subject)}
	if owner.ClusterID == "" || owner.Issuer == "" || owner.Subject == "" {
		return Owner{}, fmt.Errorf("%w: authenticated conversation owner is invalid", ErrInvalid)
	}
	return owner, nil
}

func randomID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func boundHistory(history []llm.Message, maxMessages int, maxBytes int) ([]llm.Message, error) {
	if len(history) == 0 {
		return nil, nil
	}
	starts := make([]int, 0)
	for index, message := range history {
		if message.Role == llm.RoleUser {
			starts = append(starts, index)
		}
	}
	if len(starts) == 0 || starts[0] != 0 {
		return nil, fmt.Errorf("conversation history has no complete user turn")
	}
	for _, start := range starts {
		candidate := history[start:]
		if len(candidate) > maxMessages {
			continue
		}
		encoded, err := json.Marshal(candidate)
		if err != nil {
			return nil, fmt.Errorf("encode bounded conversation history: %w", err)
		}
		if len(encoded) <= maxBytes {
			return candidate, nil
		}
	}
	return nil, nil
}
