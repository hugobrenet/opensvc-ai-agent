package llmfactory

import (
	"fmt"
	"net/http"
	"os"

	"github.com/hugobrenet/opensvc-ai-agent/internal/config"
	"github.com/hugobrenet/opensvc-ai-agent/internal/llm"
	"github.com/hugobrenet/opensvc-ai-agent/internal/llm/chatcompletions"
	"github.com/hugobrenet/opensvc-ai-agent/internal/llm/responses"
)

// New creates an LLM client selected by wire protocol, never by provider name.
func New(processConfig config.LLMConfig, httpClient *http.Client) (llm.Client, error) {
	switch processConfig.Protocol {
	case config.LLMProtocolResponses:
		var tokenSource responses.TokenSource
		if processConfig.AuthMode == config.LLMAuthModeBearer {
			tokenSource = environmentTokenSource(processConfig.APITokenEnv)
		}
		return responses.New(responses.Config{
			BaseURL:         processConfig.BaseURL,
			Model:           processConfig.Model,
			AuthMode:        processConfig.AuthMode,
			TokenSource:     tokenSource,
			Timeout:         processConfig.Timeout,
			MaxOutputTokens: processConfig.MaxOutputTokens,
		}, httpClient)
	case config.LLMProtocolChatCompletions:
		var tokenSource chatcompletions.TokenSource
		if processConfig.AuthMode == config.LLMAuthModeBearer {
			tokenSource = environmentTokenSource(processConfig.APITokenEnv)
		}
		return chatcompletions.New(chatcompletions.Config{
			BaseURL:         processConfig.BaseURL,
			Model:           processConfig.Model,
			AuthMode:        processConfig.AuthMode,
			TokenSource:     tokenSource,
			Timeout:         processConfig.Timeout,
			MaxOutputTokens: processConfig.MaxOutputTokens,
		}, httpClient)
	default:
		return nil, fmt.Errorf("unsupported LLM protocol %q", processConfig.Protocol)
	}
}

func environmentTokenSource(name string) func() (string, error) {
	return func() (string, error) {
		if name == "" {
			return "", fmt.Errorf("LLM API token environment variable name is empty")
		}
		token := os.Getenv(name)
		if token == "" {
			return "", fmt.Errorf("LLM API token environment variable %s is empty", name)
		}
		return token, nil
	}
}
