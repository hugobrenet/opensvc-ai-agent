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

	"github.com/hugobrenet/opensvc-ai-agent/internal/agent"
	"github.com/hugobrenet/opensvc-ai-agent/internal/auth"
	"github.com/hugobrenet/opensvc-ai-agent/internal/llm"
)

const (
	DefaultLifetime          = 7 * 24 * time.Hour
	DefaultListLimit         = 100
	DefaultFinalizeTimeout   = 5 * time.Second
	DefaultHistoryMessages   = 128
	DefaultHistoryBytes      = 1 << 20
	DefaultExpiryDeleteBatch = 100
	maxServicePromptBytes    = 32 << 10
)

type TurnRunner interface {
	RunTurn(context.Context, []llm.Message, string, agent.EmitFunc) (agent.TurnResult, error)
}

type ServiceConfig struct {
	Lifetime           time.Duration
	ListLimit          int
	FinalizeTimeout    time.Duration
	MaxHistoryMessages int
	MaxHistoryBytes    int
	ExpiryDeleteBatch  int
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
		config.MaxHistoryMessages <= 0 || config.MaxHistoryBytes <= 0 || config.ExpiryDeleteBatch <= 0 {
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
	result, runErr := t.service.runner.RunTurn(ctx, t.history, t.prompt, func(event agent.Event) error {
		if event.Type == agent.EventCompleted {
			if completedEvent != nil {
				return errors.New("conversation runner emitted multiple completion events")
			}
			copy := event
			completedEvent = &copy
			return nil
		}
		return emit(event)
	})
	if runErr != nil {
		status, code := turnFailure(runErr, ctx.Err())
		return errors.Join(runErr, t.service.failTurn(t.owner, t.conversationID, t.turnID, status, code))
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
	owner := Owner{Issuer: strings.TrimSpace(identity.Issuer), Subject: strings.TrimSpace(identity.Subject)}
	if owner.Issuer == "" || owner.Subject == "" {
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
