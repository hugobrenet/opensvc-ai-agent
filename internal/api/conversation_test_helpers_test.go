package api

import (
	"context"

	"github.com/hugobrenet/opensvc-ai-agent/internal/auth"
	"github.com/hugobrenet/opensvc-ai-agent/internal/conversation"
)

type noopConversationService struct{}

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

func (noopConversationService) PrepareTurn(context.Context, auth.Identity, string, string) (conversation.TurnExecution, error) {
	return nil, conversation.ErrNotFound
}
