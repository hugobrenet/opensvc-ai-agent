package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hugobrenet/opensvc-ai-agent/internal/conversation"
)

const DefaultConversationDatabasePath = "/var/lib/opensvc-ai-agent/conversations.db"

type ConversationConfig struct {
	DatabasePath string
	Lifetime     time.Duration
}

func LoadConversation() (ConversationConfig, error) {
	return loadConversation(os.Getenv)
}

func loadConversation(getenv func(string) string) (ConversationConfig, error) {
	databasePath := strings.TrimSpace(getenv("OPENSVC_AI_CONVERSATION_DB_PATH"))
	if databasePath == "" {
		databasePath = DefaultConversationDatabasePath
	}
	lifetime := conversation.DefaultLifetime
	if value := strings.TrimSpace(getenv("OPENSVC_AI_CONVERSATION_LIFETIME")); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed < time.Hour || parsed > 365*24*time.Hour {
			return ConversationConfig{}, fmt.Errorf("parse OPENSVC_AI_CONVERSATION_LIFETIME %q: expected a duration between 1h and 8760h", value)
		}
		lifetime = parsed
	}
	return ConversationConfig{DatabasePath: databasePath, Lifetime: lifetime}, nil
}
