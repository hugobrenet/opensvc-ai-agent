package llmfactory

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hugobrenet/opensvc-ai-agent/internal/config"
	"github.com/hugobrenet/opensvc-ai-agent/internal/llm/responses"
)

func TestNewSelectsResponsesProtocol(t *testing.T) {
	client, err := New(config.LLMConfig{
		Protocol:        config.LLMProtocolResponses,
		BaseURL:         "https://llm.example.test/v1",
		Model:           "test-model",
		AuthMode:        config.LLMAuthModeNone,
		Timeout:         time.Minute,
		MaxOutputTokens: 1024,
	}, http.DefaultClient)
	if err != nil {
		t.Fatalf("create LLM client: %v", err)
	}
	if _, ok := client.(*responses.Client); !ok {
		t.Fatalf("got client type %T, want Responses client", client)
	}
}

func TestNewRejectsUnsupportedProtocol(t *testing.T) {
	_, err := New(config.LLMConfig{Protocol: "unknown"}, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("New() error = %v, want unsupported protocol", err)
	}
}

func TestEnvironmentTokenSource(t *testing.T) {
	const name = "OPENSVC_AI_TEST_TOKEN_SOURCE"
	t.Setenv(name, "placeholder")
	token, err := environmentTokenSource(name)()
	if err != nil {
		t.Fatalf("load environment token: %v", err)
	}
	if token != "placeholder" {
		t.Fatalf("got token %q", token)
	}

	t.Setenv(name, "")
	if _, err := environmentTokenSource(name)(); err == nil {
		t.Fatal("empty environment token accepted")
	}
}
