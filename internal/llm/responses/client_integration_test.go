//go:build integration

package responses_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/hugobrenet/opensvc-ai-agent/internal/config"
	"github.com/hugobrenet/opensvc-ai-agent/internal/llm"
	"github.com/hugobrenet/opensvc-ai-agent/internal/llmfactory"
)

func TestLiveResponsesText(t *testing.T) {
	client := liveClient(t)
	var text strings.Builder
	var completed bool
	err := client.Stream(t.Context(), llm.Request{Messages: []llm.Message{{
		Role: llm.RoleUser,
		Text: "Reply exactly with OK and nothing else.",
	}}}, func(event llm.Event) error {
		switch event.Type {
		case llm.EventTextDelta:
			text.WriteString(event.TextDelta)
		case llm.EventCompleted:
			completed = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("stream live text response: %v", err)
	}
	if !completed || strings.TrimSpace(text.String()) == "" {
		t.Fatalf("incomplete live response, completed=%v text=%q", completed, text.String())
	}
}

func TestLiveResponsesToolCall(t *testing.T) {
	client := liveClient(t)
	userMessage := llm.Message{
		Role: llm.RoleUser,
		Text: "Call report_health exactly once with status set to ok. After receiving the tool result, reply exactly with DONE.",
	}
	tool := llm.Tool{
		Name:        "report_health",
		Description: "Report a synthetic health status for an integration test.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"status":{"type":"string","enum":["ok"]}},"required":["status"],"additionalProperties":false}`),
	}
	request := llm.Request{
		Messages: []llm.Message{userMessage},
		Tools:    []llm.Tool{tool},
	}
	var calls []llm.ToolCall
	if err := client.Stream(t.Context(), request, func(event llm.Event) error {
		if event.Type == llm.EventToolCall {
			calls = append(calls, *event.ToolCall)
		}
		return nil
	}); err != nil {
		t.Fatalf("stream live tool response: %v", err)
	}
	if len(calls) != 1 || calls[0].Name != "report_health" || !json.Valid(calls[0].Arguments) {
		t.Fatalf("unexpected live tool calls: %#v", calls)
	}

	followUp := llm.Request{
		Messages: []llm.Message{
			userMessage,
			{Role: llm.RoleAssistant, ToolCalls: calls},
			{Role: llm.RoleTool, ToolResults: []llm.ToolResult{{CallID: calls[0].ID, Content: json.RawMessage(`{"status":"ok"}`)}}},
		},
		Tools: []llm.Tool{tool},
	}
	var text strings.Builder
	if err := client.Stream(t.Context(), followUp, func(event llm.Event) error {
		if event.Type == llm.EventTextDelta {
			text.WriteString(event.TextDelta)
		}
		return nil
	}); err != nil {
		t.Fatalf("stream live tool result response: %v", err)
	}
	if strings.TrimSpace(text.String()) == "" {
		t.Fatal("live tool result produced no final text")
	}
}

func liveClient(t *testing.T) llm.Client {
	t.Helper()
	if os.Getenv("OPENSVC_AI_LLM_PROTOCOL") == "" {
		t.Skip("live LLM configuration is unavailable")
	}
	processConfig, err := config.LoadLLM()
	if err != nil {
		t.Fatalf("load live LLM configuration: %v", err)
	}
	client, err := llmfactory.New(processConfig, nil)
	if err != nil {
		t.Fatalf("create live LLM client: %v", err)
	}
	return client
}
