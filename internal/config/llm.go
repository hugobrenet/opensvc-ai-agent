package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	LLMAPITokenEnv             = "OPENSVC_AI_LLM_API_TOKEN"
	DefaultLLMTimeout          = 2 * time.Minute
	DefaultMaxOutputTokens     = 4096
	maximumMaxOutputTokens     = 131072
	LLMProtocolResponses       = "responses"
	LLMProtocolChatCompletions = "chat_completions"
	LLMAuthModeNone            = "none"
	LLMAuthModeBearer          = "bearer"
)

// LLMConfig contains non-secret process configuration for one LLM backend.
// APITokenEnv stores only the environment variable name, never its value.
type LLMConfig struct {
	Protocol        string
	BaseURL         string
	Model           string
	AuthMode        string
	APITokenEnv     string
	Timeout         time.Duration
	MaxOutputTokens int
}

// LoadLLM loads the LLM backend independently from the HTTP API configuration.
// The daemon will call it when the orchestration layer is wired.
func LoadLLM() (LLMConfig, error) {
	return loadLLM(os.Getenv)
}

func loadLLM(getenv func(string) string) (LLMConfig, error) {
	config := LLMConfig{
		Protocol:        strings.TrimSpace(getenv("OPENSVC_AI_LLM_PROTOCOL")),
		BaseURL:         strings.TrimSpace(getenv("OPENSVC_AI_LLM_BASE_URL")),
		Model:           strings.TrimSpace(getenv("OPENSVC_AI_LLM_MODEL")),
		AuthMode:        strings.TrimSpace(getenv("OPENSVC_AI_LLM_AUTH_MODE")),
		APITokenEnv:     LLMAPITokenEnv,
		Timeout:         DefaultLLMTimeout,
		MaxOutputTokens: DefaultMaxOutputTokens,
	}

	if config.Protocol == "" {
		return LLMConfig{}, fmt.Errorf("OPENSVC_AI_LLM_PROTOCOL is required")
	}
	if config.Protocol != LLMProtocolResponses && config.Protocol != LLMProtocolChatCompletions {
		return LLMConfig{}, fmt.Errorf("OPENSVC_AI_LLM_PROTOCOL %q is unsupported", config.Protocol)
	}
	if config.BaseURL == "" {
		return LLMConfig{}, fmt.Errorf("OPENSVC_AI_LLM_BASE_URL is required")
	}
	if config.Model == "" {
		return LLMConfig{}, fmt.Errorf("OPENSVC_AI_LLM_MODEL is required")
	}
	switch config.AuthMode {
	case LLMAuthModeNone:
	case LLMAuthModeBearer:
		if getenv(LLMAPITokenEnv) == "" {
			return LLMConfig{}, fmt.Errorf("%s is required for bearer authentication", LLMAPITokenEnv)
		}
	default:
		return LLMConfig{}, fmt.Errorf("OPENSVC_AI_LLM_AUTH_MODE must be %q or %q", LLMAuthModeNone, LLMAuthModeBearer)
	}

	if value := strings.TrimSpace(getenv("OPENSVC_AI_LLM_TIMEOUT")); value != "" {
		timeout, err := time.ParseDuration(value)
		if err != nil || timeout <= 0 {
			return LLMConfig{}, fmt.Errorf("parse OPENSVC_AI_LLM_TIMEOUT %q: expected a positive duration", value)
		}
		config.Timeout = timeout
	}
	if value := strings.TrimSpace(getenv("OPENSVC_AI_LLM_MAX_OUTPUT_TOKENS")); value != "" {
		maxOutputTokens, err := strconv.Atoi(value)
		if err != nil || maxOutputTokens <= 0 || maxOutputTokens > maximumMaxOutputTokens {
			return LLMConfig{}, fmt.Errorf("parse OPENSVC_AI_LLM_MAX_OUTPUT_TOKENS %q: expected an integer between 1 and %d", value, maximumMaxOutputTokens)
		}
		config.MaxOutputTokens = maxOutputTokens
	}
	return config, nil
}
