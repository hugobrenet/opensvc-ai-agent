package api

import (
	"context"

	"github.com/opensvc/ai-agent/internal/agent"
	"github.com/opensvc/ai-agent/internal/auth"
	"github.com/opensvc/ai-agent/internal/conversation"
)

type noopConversationService struct{}

func (noopConversationService) Messages(context.Context, auth.Identity, string, conversation.MessageQuery) (conversation.MessagePage, error) {
	return conversation.MessagePage{}, conversation.ErrNotFound
}

func (noopConversationService) Create(context.Context, auth.Identity) (conversation.Conversation, error) {
	return conversation.Conversation{}, nil
}

func (noopConversationService) Get(context.Context, auth.Identity, string) (conversation.Conversation, error) {
	return conversation.Conversation{}, conversation.ErrNotFound
}

func (noopConversationService) List(context.Context, auth.Identity) ([]conversation.Conversation, error) {
	return nil, nil
}

func (noopConversationService) Delete(context.Context, auth.Identity, string) error {
	return conversation.ErrNotFound
}

func (noopConversationService) UpdateTitle(context.Context, auth.Identity, string, string) (conversation.Conversation, error) {
	return conversation.Conversation{}, conversation.ErrNotFound
}

func (noopConversationService) PrepareTurn(context.Context, auth.Identity, string, string) (conversation.TurnExecution, error) {
	return nil, conversation.ErrNotFound
}

func (noopConversationService) PrepareConfirmation(context.Context, auth.Identity, string, string, string, agent.Decision) (conversation.TurnExecution, error) {
	return nil, conversation.ErrNotFound
}

func (noopConversationService) PendingConfirmation(context.Context, auth.Identity, string) (*conversation.PendingConfirmation, error) {
	return nil, conversation.ErrNotFound
}
