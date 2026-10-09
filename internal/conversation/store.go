package conversation

import (
	"context"
	"errors"
	"time"

	"github.com/opensvc/ai-agent/internal/llm"
)

var (
	ErrNotFound        = errors.New("conversation not found")
	ErrBusy            = errors.New("conversation has a running turn or a turn awaiting confirmation")
	ErrExpired         = errors.New("conversation expired")
	ErrConflict        = errors.New("conversation store conflict")
	ErrLimit           = errors.New("conversation store limit exceeded")
	ErrInvalid         = errors.New("invalid conversation store input")
	ErrMessageTooLarge = errors.New("conversation display message exceeds page size")
	// ErrNotPending refuses a decision on a turn that waits on no such
	// confirmation: unknown, already decided or for another turn.
	ErrNotPending = errors.New("no such pending confirmation")
	// ErrConfirmationExpired refuses a decision arriving after the expiry of
	// its confirmation; the turn has failed without running the call.
	ErrConfirmationExpired = errors.New("confirmation expired")
)

type Store interface {
	CreateConversation(context.Context, Conversation) error
	GetConversation(context.Context, Owner, string) (Conversation, error)
	ListConversations(context.Context, Owner, int) ([]Conversation, error)
	UpdateConversationTitle(context.Context, Owner, string, string, time.Time) (Conversation, error)
	DeleteConversation(context.Context, Owner, string) error

	BeginTurn(context.Context, Owner, string, string, time.Time) (Turn, error)
	CompleteTurn(context.Context, Owner, string, string, time.Time, time.Time, []llm.Message) error
	FailTurn(context.Context, Owner, string, string, TurnStatus, string, time.Time) error
	SuspendTurn(context.Context, Owner, PendingConfirmation) error
	ClaimConfirmation(context.Context, Owner, string, string, string, time.Time) (PendingConfirmation, error)
	GetPendingConfirmation(context.Context, Owner, string, time.Time) (*PendingConfirmation, error)
	LoadHistory(context.Context, Owner, string) ([]llm.Message, error)
	ListMessages(context.Context, Owner, string, MessageQuery) (MessagePage, error)

	RecoverInterrupted(context.Context, time.Time) (int64, error)
	DeleteExpired(context.Context, time.Time, int) (int64, error)
	Close() error
}
