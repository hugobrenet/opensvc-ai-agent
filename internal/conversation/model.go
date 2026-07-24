package conversation

import "time"

type Owner struct {
	Issuer  string
	Subject string
}

type Conversation struct {
	ID          string
	Owner       Owner
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ExpiresAt   time.Time
	StoredBytes int64
}

type TurnStatus string

const (
	TurnRunning     TurnStatus = "running"
	TurnCompleted   TurnStatus = "completed"
	TurnFailed      TurnStatus = "failed"
	TurnCanceled    TurnStatus = "canceled"
	TurnInterrupted TurnStatus = "interrupted"
)

type Turn struct {
	ID             string
	ConversationID string
	Sequence       int64
	Status         TurnStatus
	ErrorCode      string
	StartedAt      time.Time
	CompletedAt    *time.Time
}
