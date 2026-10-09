package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/opensvc/ai-agent/internal/conversation"
)

const (
	maxConfirmationToolBytes      = 256
	maxConfirmationArgumentsBytes = 256 << 10
	maxConfirmationStateBytes     = 4 << 20
	confirmationExpiredCode       = "confirmation_expired"
)

// SuspendTurn records the tool call a running turn waits on and moves the
// turn to awaiting_confirmation, in one transaction.
func (s *Store) SuspendTurn(ctx context.Context, owner conversation.Owner, pending conversation.PendingConfirmation) error {
	if err := validateOwnerAndID(owner, pending.ConversationID); err != nil {
		return err
	}
	if err := validatePendingConfirmation(pending); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin suspend turn transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := requireConversation(ctx, tx, owner, pending.ConversationID); err != nil {
		return err
	}
	turn, err := getTurn(ctx, tx, pending.ConversationID, pending.TurnID)
	if err != nil {
		return err
	}
	if turn.Status != conversation.TurnRunning {
		return fmt.Errorf("%w: turn %q is %s", conversation.ErrConflict, pending.TurnID, turn.Status)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO confirmations(turn_id, confirmation_id, tool_name, tool_title, arguments_json, arguments_hash, state_json, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		pending.TurnID, pending.ConfirmationID, pending.ToolName, pending.ToolTitle, []byte(pending.Arguments),
		pending.ArgumentsHash, pending.State, toUnixNano(pending.CreatedAt), toUnixNano(pending.ExpiresAt),
	); err != nil {
		if isConstraintError(err) {
			return fmt.Errorf("%w: confirmation for turn %q already exists", conversation.ErrConflict, pending.TurnID)
		}
		return fmt.Errorf("insert confirmation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE turns SET status = 'awaiting_confirmation'
WHERE id = ? AND conversation_id = ? AND status = 'running'`, pending.TurnID, pending.ConversationID); err != nil {
		return fmt.Errorf("suspend conversation turn: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE conversations SET updated_at = ? WHERE id = ?", toUnixNano(pending.CreatedAt), pending.ConversationID); err != nil {
		return fmt.Errorf("update suspended conversation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit suspended turn: %w", err)
	}
	return nil
}

// ClaimConfirmation consumes the confirmation a turn waits on: the turn goes
// back to running for the decision to apply, and the confirmation cannot be
// used again. A confirmation past its expiry fails the turn instead.
func (s *Store) ClaimConfirmation(ctx context.Context, owner conversation.Owner, conversationID string, turnID string, confirmationID string, at time.Time) (conversation.PendingConfirmation, error) {
	if err := validateOwnerAndID(owner, conversationID); err != nil {
		return conversation.PendingConfirmation{}, err
	}
	if err := validateIdentifier("turn", turnID); err != nil {
		return conversation.PendingConfirmation{}, err
	}
	if err := validateIdentifier("confirmation", confirmationID); err != nil {
		return conversation.PendingConfirmation{}, err
	}
	if at.IsZero() {
		return conversation.PendingConfirmation{}, fmt.Errorf("%w: confirmation time is zero", conversation.ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return conversation.PendingConfirmation{}, fmt.Errorf("begin claim confirmation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := requireConversation(ctx, tx, owner, conversationID); err != nil {
		return conversation.PendingConfirmation{}, err
	}
	turn, err := getTurn(ctx, tx, conversationID, turnID)
	if err != nil {
		return conversation.PendingConfirmation{}, err
	}
	if turn.Status != conversation.TurnAwaitingConfirmation {
		return conversation.PendingConfirmation{}, conversation.ErrNotPending
	}
	pending, err := getConfirmation(ctx, tx, conversationID, turnID)
	if err != nil {
		return conversation.PendingConfirmation{}, err
	}
	if pending.ConfirmationID != confirmationID {
		return conversation.PendingConfirmation{}, conversation.ErrNotPending
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM confirmations WHERE turn_id = ?", turnID); err != nil {
		return conversation.PendingConfirmation{}, fmt.Errorf("consume confirmation: %w", err)
	}
	if !pending.ExpiresAt.After(at) {
		if err := expireTurn(ctx, tx, conversationID, turnID, at); err != nil {
			return conversation.PendingConfirmation{}, err
		}
		if err := tx.Commit(); err != nil {
			return conversation.PendingConfirmation{}, fmt.Errorf("commit expired confirmation: %w", err)
		}
		return conversation.PendingConfirmation{}, conversation.ErrConfirmationExpired
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE turns SET status = 'running'
WHERE id = ? AND conversation_id = ? AND status = 'awaiting_confirmation'`, turnID, conversationID); err != nil {
		return conversation.PendingConfirmation{}, fmt.Errorf("resume conversation turn: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return conversation.PendingConfirmation{}, fmt.Errorf("commit claimed confirmation: %w", err)
	}
	return pending, nil
}

// GetPendingConfirmation returns the unexpired confirmation the conversation
// waits on, or nil.
func (s *Store) GetPendingConfirmation(ctx context.Context, owner conversation.Owner, conversationID string, at time.Time) (*conversation.PendingConfirmation, error) {
	if err := validateOwnerAndID(owner, conversationID); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin read confirmation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := requireConversation(ctx, tx, owner, conversationID); err != nil {
		return nil, err
	}
	var turnID string
	if err := tx.QueryRowContext(ctx, `
SELECT id FROM turns WHERE conversation_id = ? AND status = 'awaiting_confirmation'`, conversationID).Scan(&turnID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("read awaiting turn: %w", err)
	}
	pending, err := getConfirmation(ctx, tx, conversationID, turnID)
	if err != nil {
		return nil, err
	}
	if !pending.ExpiresAt.After(at) {
		return nil, nil
	}
	return &pending, nil
}

// failExpiredConfirmation fails the turn of a conversation whose confirmation
// expired, so that a new turn can begin.
func failExpiredConfirmation(ctx context.Context, tx *sql.Tx, conversationID string, at time.Time) error {
	var turnID string
	err := tx.QueryRowContext(ctx, `
SELECT t.id FROM turns AS t JOIN confirmations AS c ON c.turn_id = t.id
WHERE t.conversation_id = ? AND t.status = 'awaiting_confirmation' AND c.expires_at <= ?`,
		conversationID, toUnixNano(at)).Scan(&turnID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read expired confirmation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM confirmations WHERE turn_id = ?", turnID); err != nil {
		return fmt.Errorf("delete expired confirmation: %w", err)
	}
	return expireTurn(ctx, tx, conversationID, turnID, at)
}

func expireTurn(ctx context.Context, tx *sql.Tx, conversationID string, turnID string, at time.Time) error {
	if _, err := tx.ExecContext(ctx, `
UPDATE turns SET status = 'failed', error_code = ?, completed_at = ?
WHERE id = ? AND conversation_id = ? AND status = 'awaiting_confirmation'`,
		confirmationExpiredCode, toUnixNano(at), turnID, conversationID); err != nil {
		return fmt.Errorf("expire conversation turn: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE conversations SET updated_at = ? WHERE id = ?", toUnixNano(at), conversationID); err != nil {
		return fmt.Errorf("update expired conversation: %w", err)
	}
	return nil
}

func getConfirmation(ctx context.Context, tx *sql.Tx, conversationID string, turnID string) (conversation.PendingConfirmation, error) {
	item := conversation.PendingConfirmation{ConversationID: conversationID, TurnID: turnID}
	var arguments, state []byte
	var createdAt, expiresAt int64
	if err := tx.QueryRowContext(ctx, `
SELECT confirmation_id, tool_name, tool_title, arguments_json, arguments_hash, state_json, created_at, expires_at
FROM confirmations WHERE turn_id = ?`, turnID).Scan(
		&item.ConfirmationID, &item.ToolName, &item.ToolTitle, &arguments, &item.ArgumentsHash, &state, &createdAt, &expiresAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return conversation.PendingConfirmation{}, conversation.ErrNotPending
		}
		return conversation.PendingConfirmation{}, fmt.Errorf("read confirmation: %w", err)
	}
	item.Arguments = arguments
	item.State = state
	item.CreatedAt = fromUnixNano(createdAt)
	item.ExpiresAt = fromUnixNano(expiresAt)
	return item, nil
}

func validatePendingConfirmation(pending conversation.PendingConfirmation) error {
	if err := validateIdentifier("turn", pending.TurnID); err != nil {
		return err
	}
	if err := validateIdentifier("confirmation", pending.ConfirmationID); err != nil {
		return err
	}
	if pending.ToolName == "" || len(pending.ToolName) > maxConfirmationToolBytes || len(pending.ToolTitle) > maxConfirmationToolBytes {
		return fmt.Errorf("%w: invalid confirmation tool", conversation.ErrInvalid)
	}
	if len(pending.Arguments) == 0 || len(pending.Arguments) > maxConfirmationArgumentsBytes || pending.ArgumentsHash == "" {
		return fmt.Errorf("%w: invalid confirmation arguments", conversation.ErrInvalid)
	}
	if len(pending.State) == 0 || len(pending.State) > maxConfirmationStateBytes {
		return fmt.Errorf("%w: confirmation state is empty or exceeds %d bytes", conversation.ErrInvalid, maxConfirmationStateBytes)
	}
	if pending.CreatedAt.IsZero() || !pending.ExpiresAt.After(pending.CreatedAt) {
		return fmt.Errorf("%w: invalid confirmation times", conversation.ErrInvalid)
	}
	return nil
}
