package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hugobrenet/opensvc-ai-agent/internal/auth"
	"github.com/hugobrenet/opensvc-ai-agent/internal/llm"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type MCPSession interface {
	ListTools(context.Context) ([]*mcp.Tool, error)
	CallTool(context.Context, string, map[string]any) (*mcp.CallToolResult, error)
	Close() error
}

type MCPConnectFunc func(context.Context) (MCPSession, error)

type Config struct {
	MaxIterations int
	Timeout       time.Duration
}

type Agent struct {
	llm        llm.Client
	connectMCP MCPConnectFunc
	config     Config
}

// TurnResult contains the provider-neutral messages produced by one completed
// user turn. It excludes the system prompt and the history supplied to
// RunTurn.
type TurnResult struct {
	Messages     []llm.Message
	FinishReason llm.FinishReason
}

func New(llmClient llm.Client, connectMCP MCPConnectFunc, config Config) (*Agent, error) {
	if llmClient == nil {
		return nil, fmt.Errorf("agent LLM client is nil")
	}
	if connectMCP == nil {
		return nil, fmt.Errorf("agent MCP connector is nil")
	}
	if config.MaxIterations <= 0 {
		return nil, fmt.Errorf("agent max iterations must be positive")
	}
	if config.Timeout <= 0 {
		return nil, fmt.Errorf("agent timeout must be positive")
	}
	return &Agent{llm: llmClient, connectMCP: connectMCP, config: config}, nil
}

func (a *Agent) Ask(ctx context.Context, prompt string, emit EmitFunc) (err error) {
	_, err = a.RunTurn(ctx, nil, prompt, emit)
	return err
}

// RunTurn executes one user turn after a complete provider-neutral history.
// It returns only the new messages produced by the turn. A completed result may
// accompany an MCP cleanup error, allowing a future conversation service to
// distinguish completed model output from partial output.
func (a *Agent) RunTurn(ctx context.Context, history []llm.Message, prompt string, emit EmitFunc) (result TurnResult, err error) {
	if strings.TrimSpace(prompt) == "" {
		return result, fmt.Errorf("agent prompt is empty")
	}
	if emit == nil {
		return result, fmt.Errorf("agent event consumer is nil")
	}
	history, err = prepareHistory(history)
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, a.config.Timeout)
	defer cancel()

	session, err := a.connectMCP(ctx)
	if err != nil {
		return result, fmt.Errorf("connect agent to MCP: %w", err)
	}
	defer func() {
		if closeErr := session.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close agent MCP session: %w", closeErr))
		}
	}()

	mcpTools, err := session.ListTools(ctx)
	if err != nil {
		return result, fmt.Errorf("list agent MCP tools: %w", err)
	}
	tools, toolNames, err := convertTools(mcpTools)
	if err != nil {
		return result, err
	}
	userMessage := llm.Message{Role: llm.RoleUser, Text: prompt}
	messages := make([]llm.Message, 0, len(history)+2)
	messages = append(messages, llm.Message{Role: llm.RoleSystem, Text: systemPrompt})
	messages = append(messages, history...)
	messages = append(messages, userMessage)
	turnMessages := []llm.Message{userMessage}
	llmContext := auth.WithoutAuthentication(ctx)
	totalToolCalls := 0

	for iteration := 1; iteration <= a.config.MaxIterations; iteration++ {
		var (
			calls      []llm.ToolCall
			turnText   strings.Builder
			finish     llm.FinishReason
			completion bool
		)
		request := llm.Request{Messages: messages, Tools: tools}
		if err := a.llm.Stream(llmContext, request, func(event llm.Event) error {
			if err := event.Validate(); err != nil {
				return fmt.Errorf("validate LLM event: %w", err)
			}
			switch event.Type {
			case llm.EventTextDelta:
				turnText.WriteString(event.TextDelta)
				return emitAgentEvent(emit, Event{Type: EventTextDelta, TextDelta: event.TextDelta, Iteration: iteration})
			case llm.EventToolCall:
				if len(calls) >= maxToolCallsPerTurn {
					return fmt.Errorf("LLM iteration %d requested %d tools, maximum is %d", iteration, len(calls)+1, maxToolCallsPerTurn)
				}
				call := *event.ToolCall
				call.Arguments = append(json.RawMessage(nil), event.ToolCall.Arguments...)
				calls = append(calls, call)
			case llm.EventUsage:
				usage := *event.Usage
				return emitAgentEvent(emit, Event{Type: EventUsage, Usage: &usage, Iteration: iteration})
			case llm.EventCompleted:
				if completion {
					return fmt.Errorf("LLM emitted multiple completion events")
				}
				completion = true
				finish = event.FinishReason
			}
			return nil
		}); err != nil {
			return result, fmt.Errorf("run LLM iteration %d: %w", iteration, err)
		}
		if !completion {
			return result, fmt.Errorf("LLM iteration %d ended without completion", iteration)
		}
		if len(calls) == 0 {
			if finish == llm.FinishReasonToolCalls {
				return result, fmt.Errorf("LLM iteration %d completed for tool calls without a tool call", iteration)
			}
			if turnText.Len() == 0 {
				return result, fmt.Errorf("LLM iteration %d completed without text or tool calls", iteration)
			}
			assistantMessage := llm.Message{Role: llm.RoleAssistant, Text: turnText.String()}
			if err := emitAgentEvent(emit, Event{Type: EventCompleted, FinishReason: finish, Iteration: iteration}); err != nil {
				return result, err
			}
			turnMessages = append(turnMessages, assistantMessage)
			result = TurnResult{Messages: turnMessages, FinishReason: finish}
			return result, nil
		}
		if finish != llm.FinishReasonToolCalls {
			return result, fmt.Errorf("LLM iteration %d emitted tool calls with finish reason %q", iteration, finish)
		}
		if len(calls) > maxToolCallsPerTurn {
			return result, fmt.Errorf("LLM iteration %d requested %d tools, maximum is %d", iteration, len(calls), maxToolCallsPerTurn)
		}
		if totalToolCalls+len(calls) > maxToolCallsPerAsk {
			return result, fmt.Errorf("agent tool call count would exceed maximum of %d", maxToolCallsPerAsk)
		}
		if iteration == a.config.MaxIterations {
			return result, fmt.Errorf("agent reached maximum of %d iterations before a final answer", a.config.MaxIterations)
		}

		results := make([]llm.ToolResult, 0, len(calls))
		totalToolCalls += len(calls)
		for _, call := range calls {
			if _, ok := toolNames[call.Name]; !ok {
				return result, fmt.Errorf("LLM requested unknown MCP tool %q", call.Name)
			}
			arguments, err := decodeToolArguments(call)
			if err != nil {
				return result, err
			}
			if err := emitAgentEvent(emit, Event{Type: EventToolStarted, ToolName: call.Name, Iteration: iteration}); err != nil {
				return result, err
			}
			toolResult, err := session.CallTool(ctx, call.Name, arguments)
			if err != nil {
				return result, fmt.Errorf("call agent MCP tool %q: %w", call.Name, err)
			}
			content, err := encodeToolResult(toolResult)
			if err != nil {
				return result, fmt.Errorf("process agent MCP tool %q result: %w", call.Name, err)
			}
			if err := emitAgentEvent(emit, Event{Type: EventToolFinished, ToolName: call.Name, ToolError: toolResult.IsError, Iteration: iteration}); err != nil {
				return result, err
			}
			results = append(results, llm.ToolResult{CallID: call.ID, Content: content, IsError: toolResult.IsError})
		}
		assistantMessage := llm.Message{Role: llm.RoleAssistant, Text: turnText.String(), ToolCalls: calls}
		toolMessage := llm.Message{Role: llm.RoleTool, ToolResults: results}
		messages = append(messages, assistantMessage, toolMessage)
		turnMessages = append(turnMessages, assistantMessage, toolMessage)
	}
	return result, fmt.Errorf("agent reached maximum of %d iterations", a.config.MaxIterations)
}

func emitAgentEvent(emit EmitFunc, event Event) error {
	if err := event.Validate(); err != nil {
		return fmt.Errorf("validate agent event: %w", err)
	}
	if err := emit(event); err != nil {
		return fmt.Errorf("consume agent event: %w", err)
	}
	return nil
}
