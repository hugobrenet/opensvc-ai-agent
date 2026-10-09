package sqlite

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/opensvc/ai-agent/internal/conversation"
	"github.com/opensvc/ai-agent/internal/llm"
)

func suspendTestTurn(t *testing.T, store *Store, conversationID string, turnID string, confirmationID string, at time.Time) conversation.PendingConfirmation {
	t.Helper()
	if _, err := store.BeginTurn(t.Context(), testOwner, conversationID, turnID, at); err != nil {
		t.Fatalf("BeginTurn() error: %v", err)
	}
	pending := conversation.PendingConfirmation{
		ConversationID: conversationID, TurnID: turnID, ConfirmationID: confirmationID,
		ToolName: "stop_object", ToolTitle: "Stop object", Arguments: json.RawMessage(`{"path":"web"}`),
		ArgumentsHash: "hash", State: []byte(`{"messages":[]}`),
		CreatedAt: at, ExpiresAt: at.Add(10 * time.Minute),
	}
	if err := store.SuspendTurn(t.Context(), testOwner, pending); err != nil {
		t.Fatalf("SuspendTurn() error: %v", err)
	}
	return pending
}

func TestStoreSuspendsAndResumesATurnOnce(t *testing.T) {
	store, _ := openTestStore(t, Config{})
	item := testConversation("conversation-1", testOwner, testNow)
	if err := store.CreateConversation(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	pending := suspendTestTurn(t, store, item.ID, "turn-1", "confirmation-1", testNow.Add(time.Second))

	got, err := store.GetPendingConfirmation(t.Context(), testOwner, item.ID, testNow.Add(2*time.Second))
	if err != nil || got == nil || got.ConfirmationID != "confirmation-1" || string(got.Arguments) != `{"path":"web"}` || got.ToolTitle != "Stop object" {
		t.Fatalf("GetPendingConfirmation() = %+v, %v", got, err)
	}
	if _, err := store.BeginTurn(t.Context(), testOwner, item.ID, "turn-2", testNow.Add(3*time.Second)); !errors.Is(err, conversation.ErrBusy) {
		t.Fatalf("a new turn began while a confirmation waits: %v", err)
	}
	if _, err := store.ClaimConfirmation(t.Context(), testOwner, item.ID, "turn-1", "confirmation-other", testNow.Add(3*time.Second)); !errors.Is(err, conversation.ErrNotPending) {
		t.Fatalf("a wrong confirmation ID was accepted: %v", err)
	}
	other := conversation.Owner{ClusterID: "cluster-id", Issuer: "node-a", Subject: "bob"}
	if _, err := store.ClaimConfirmation(t.Context(), other, item.ID, "turn-1", "confirmation-1", testNow.Add(3*time.Second)); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("another owner claimed the confirmation: %v", err)
	}
	claimed, err := store.ClaimConfirmation(t.Context(), testOwner, item.ID, "turn-1", "confirmation-1", testNow.Add(4*time.Second))
	if err != nil || string(claimed.State) != string(pending.State) || claimed.ArgumentsHash != "hash" {
		t.Fatalf("ClaimConfirmation() = %+v, %v", claimed, err)
	}
	if _, err := store.ClaimConfirmation(t.Context(), testOwner, item.ID, "turn-1", "confirmation-1", testNow.Add(5*time.Second)); !errors.Is(err, conversation.ErrNotPending) {
		t.Fatalf("a confirmation was used twice: %v", err)
	}
	if got, _ := store.GetPendingConfirmation(t.Context(), testOwner, item.ID, testNow.Add(5*time.Second)); got != nil {
		t.Fatalf("a claimed confirmation is still pending: %+v", got)
	}
	messages := []llm.Message{{Role: llm.RoleUser, Text: "stop web"}, {Role: llm.RoleAssistant, Text: "stopped"}}
	if err := store.CompleteTurn(t.Context(), testOwner, item.ID, "turn-1", testNow.Add(6*time.Second), testNow.Add(time.Hour), messages); err != nil {
		t.Fatalf("the resumed turn did not complete: %v", err)
	}
}

func TestStoreExpiresAConfirmationWithoutResumingItsTurn(t *testing.T) {
	store, _ := openTestStore(t, Config{})
	item := testConversation("conversation-1", testOwner, testNow)
	if err := store.CreateConversation(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	pending := suspendTestTurn(t, store, item.ID, "turn-1", "confirmation-1", testNow.Add(time.Second))
	late := pending.ExpiresAt.Add(time.Second)
	if got, _ := store.GetPendingConfirmation(t.Context(), testOwner, item.ID, late); got != nil {
		t.Fatalf("an expired confirmation is reported pending: %+v", got)
	}
	if _, err := store.ClaimConfirmation(t.Context(), testOwner, item.ID, "turn-1", "confirmation-1", late); !errors.Is(err, conversation.ErrConfirmationExpired) {
		t.Fatalf("an expired confirmation was accepted: %v", err)
	}
	if _, err := store.ClaimConfirmation(t.Context(), testOwner, item.ID, "turn-1", "confirmation-1", late); !errors.Is(err, conversation.ErrNotPending) {
		t.Fatalf("an expired confirmation stays claimable: %v", err)
	}
	if _, err := store.BeginTurn(t.Context(), testOwner, item.ID, "turn-2", late); err != nil {
		t.Fatalf("the conversation stays blocked after the expiry: %v", err)
	}
}

func TestStoreLetsANewTurnReplaceAnExpiredConfirmation(t *testing.T) {
	store, _ := openTestStore(t, Config{})
	item := testConversation("conversation-1", testOwner, testNow)
	if err := store.CreateConversation(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	pending := suspendTestTurn(t, store, item.ID, "turn-1", "confirmation-1", testNow.Add(time.Second))
	if _, err := store.BeginTurn(t.Context(), testOwner, item.ID, "turn-2", pending.ExpiresAt.Add(time.Second)); err != nil {
		t.Fatalf("a new turn did not replace the expired confirmation: %v", err)
	}
	if _, err := store.ClaimConfirmation(t.Context(), testOwner, item.ID, "turn-1", "confirmation-1", pending.ExpiresAt.Add(time.Minute)); !errors.Is(err, conversation.ErrNotPending) {
		t.Fatalf("the replaced confirmation stays claimable: %v", err)
	}
}

func TestStoreKeepsAwaitingTurnsAcrossRecoveryAndDeletesThemWithTheConversation(t *testing.T) {
	store, _ := openTestStore(t, Config{})
	item := testConversation("conversation-1", testOwner, testNow)
	if err := store.CreateConversation(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	suspendTestTurn(t, store, item.ID, "turn-1", "confirmation-1", testNow.Add(time.Second))
	if recovered, err := store.RecoverInterrupted(t.Context(), testNow.Add(2*time.Second)); err != nil || recovered != 0 {
		t.Fatalf("recovery interrupted an awaiting turn: %d %v", recovered, err)
	}
	if got, _ := store.GetPendingConfirmation(t.Context(), testOwner, item.ID, testNow.Add(3*time.Second)); got == nil {
		t.Fatal("a restart lost the pending confirmation")
	}
	if err := store.DeleteConversation(t.Context(), testOwner, item.ID); err != nil {
		t.Fatalf("an awaiting conversation cannot be deleted: %v", err)
	}
	var count int
	if err := store.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM confirmations").Scan(&count); err != nil || count != 0 {
		t.Fatalf("confirmations left after deletion: %d %v", count, err)
	}
}

func TestStoreRejectsInvalidConfirmations(t *testing.T) {
	store, _ := openTestStore(t, Config{})
	item := testConversation("conversation-1", testOwner, testNow)
	if err := store.CreateConversation(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginTurn(t.Context(), testOwner, item.ID, "turn-1", testNow); err != nil {
		t.Fatal(err)
	}
	valid := conversation.PendingConfirmation{
		ConversationID: item.ID, TurnID: "turn-1", ConfirmationID: "confirmation-1", ToolName: "stop_object",
		Arguments: json.RawMessage(`{}`), ArgumentsHash: "hash", State: []byte(`{}`), CreatedAt: testNow, ExpiresAt: testNow.Add(time.Minute),
	}
	for name, mutate := range map[string]func(*conversation.PendingConfirmation){
		"no tool":         func(p *conversation.PendingConfirmation) { p.ToolName = "" },
		"no arguments":    func(p *conversation.PendingConfirmation) { p.Arguments = nil },
		"no state":        func(p *conversation.PendingConfirmation) { p.State = nil },
		"already expired": func(p *conversation.PendingConfirmation) { p.ExpiresAt = p.CreatedAt },
		"unknown turn":    func(p *conversation.PendingConfirmation) { p.TurnID = "turn-2" },
	} {
		pending := valid
		mutate(&pending)
		if err := store.SuspendTurn(t.Context(), testOwner, pending); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}
