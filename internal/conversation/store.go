package conversation

import (
	"context"
	"errors"
	"time"

	"github.com/hugobrenet/opensvc-ai-agent/internal/llm"
)

var (
	ErrNotFound = errors.New("conversation not found")
	ErrBusy     = errors.New("conversation has a running turn")
	ErrConflict = errors.New("conversation store conflict")
	ErrLimit    = errors.New("conversation store limit exceeded")
	ErrInvalid  = errors.New("invalid conversation store input")
)

type Store interface {
	CreateConversation(context.Context, Conversation) error
	GetConversation(context.Context, Owner, string) (Conversation, error)
	ListConversations(context.Context, Owner, int) ([]Conversation, error)
	DeleteConversation(context.Context, Owner, string) error

	BeginTurn(context.Context, Owner, string, string, time.Time) (Turn, error)
	CompleteTurn(context.Context, Owner, string, string, time.Time, []llm.Message) error
	FailTurn(context.Context, Owner, string, string, TurnStatus, string, time.Time) error
	LoadHistory(context.Context, Owner, string) ([]llm.Message, error)

	RecoverInterrupted(context.Context, time.Time) (int64, error)
	DeleteExpired(context.Context, time.Time, int) (int64, error)
	Close() error
}
