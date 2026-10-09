package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/opensvc/ai-agent/internal/auth"
	"github.com/opensvc/ai-agent/internal/llm"
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
// RunTurn. Suspended is set instead when the turn waits for the user to
// confirm a tool call; ResumeTurn continues it.
type TurnResult struct {
	Messages     []llm.Message
	FinishReason llm.FinishReason
	Suspended    *SuspendedTurn
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

// Ask runs a turn that cannot wait for a confirmation: a tool call requiring
// one is not executed, and the model is told so.
func (a *Agent) Ask(ctx context.Context, prompt string, emit EmitFunc) (err error) {
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("agent prompt is empty")
	}
	_, err = a.run(ctx, nil, &SuspendedTurn{Messages: []llm.Message{{Role: llm.RoleUser, Text: prompt}}}, "", emit, false)
	return err
}

// RunTurn executes one user turn after a complete provider-neutral history.
// It returns only the new messages produced by the turn. A completed result may
// accompany an MCP cleanup error, allowing a future conversation service to
// distinguish completed model output from partial output. A tool call that
// requires the confirmation of the user suspends the turn instead of running.
func (a *Agent) RunTurn(ctx context.Context, history []llm.Message, prompt string, emit EmitFunc) (result TurnResult, err error) {
	if strings.TrimSpace(prompt) == "" {
		return result, fmt.Errorf("agent prompt is empty")
	}
	return a.run(ctx, history, &SuspendedTurn{Messages: []llm.Message{{Role: llm.RoleUser, Text: prompt}}}, "", emit, true)
}

// ResumeTurn continues a suspended turn with the decision of the user on its
// pending tool call: approved, the call runs as it was suspended; rejected,
// the model is told it did not run.
func (a *Agent) ResumeTurn(ctx context.Context, history []llm.Message, suspended SuspendedTurn, decision Decision, emit EmitFunc) (result TurnResult, err error) {
	if !decision.Valid() {
		return result, fmt.Errorf("agent confirmation decision %q is invalid", decision)
	}
	if err := suspended.validate(a.config.MaxIterations); err != nil {
		return result, err
	}
	state := suspended
	if state.Messages, err = prepareSuspendedMessages(suspended.Messages); err != nil {
		return result, fmt.Errorf("suspended turn: %w", err)
	}
	state.Calls = append([]llm.ToolCall(nil), suspended.Calls...)
	state.Results = append([]llm.ToolResult(nil), suspended.Results...)
	return a.run(ctx, history, &state, decision, emit, true)
}

// run drives the turn held by state until the model answers, or, when
// canSuspend is set, until a tool call requires a confirmation. decision
// applies to the first call left to run, the one a suspended turn waits on.
func (a *Agent) run(ctx context.Context, history []llm.Message, state *SuspendedTurn, decision Decision, emit EmitFunc, canSuspend bool) (result TurnResult, err error) {
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
	tools, _, err := convertTools(mcpTools)
	if err != nil {
		return result, err
	}
	toolsByName := make(map[string]*mcp.Tool, len(mcpTools))
	for _, tool := range mcpTools {
		toolsByName[tool.Name] = tool
	}
	messages := make([]llm.Message, 0, len(history)+len(state.Messages)+3)
	messages = append(messages, llm.Message{Role: llm.RoleSystem, Text: systemPrompt})
	messages = append(messages, history...)
	messages = append(messages, state.Messages...)
	llmContext := auth.WithoutAuthentication(ctx)

	// executeCalls runs the calls of the current iteration left to run, and
	// reports whether one waits for a confirmation.
	executeCalls := func() (bool, error) {
		for len(state.Results) < len(state.Calls) {
			call := state.Calls[len(state.Results)]
			tool, ok := toolsByName[call.Name]
			if !ok {
				return false, fmt.Errorf("LLM requested unknown MCP tool %q", call.Name)
			}
			arguments, err := decodeToolArguments(call)
			if err != nil {
				return false, err
			}
			callDecision := decision
			decision = ""
			if callDecision == DecisionReject {
				state.Results = append(state.Results, llm.ToolResult{CallID: call.ID, Content: rejectedToolResult, IsError: true})
				continue
			}
			if callDecision != DecisionApprove && requiresConfirmation(tool) {
				if !canSuspend {
					state.Results = append(state.Results, llm.ToolResult{CallID: call.ID, Content: unconfirmableToolResult, IsError: true})
					continue
				}
				state.ToolTitle = toolTitle(tool)
				return true, nil
			}
			if err := emitAgentEvent(emit, Event{Type: EventToolStarted, ToolName: call.Name, Iteration: state.Iteration}); err != nil {
				return false, err
			}
			toolResult, err := session.CallTool(ctx, call.Name, arguments)
			if err != nil {
				return false, fmt.Errorf("call agent MCP tool %q: %w", call.Name, err)
			}
			content, err := encodeToolResult(toolResult)
			if err != nil {
				return false, fmt.Errorf("process agent MCP tool %q result: %w", call.Name, err)
			}
			if err := emitAgentEvent(emit, Event{Type: EventToolFinished, ToolName: call.Name, ToolError: toolResult.IsError, Iteration: state.Iteration}); err != nil {
				return false, err
			}
			state.Results = append(state.Results, llm.ToolResult{CallID: call.ID, Content: content, IsError: toolResult.IsError})
		}
		return false, nil
	}

	for {
		if len(state.Calls) > 0 {
			suspended, err := executeCalls()
			if err != nil {
				return result, err
			}
			if suspended {
				snapshot := *state
				return TurnResult{Suspended: &snapshot}, nil
			}
			assistantMessage := llm.Message{Role: llm.RoleAssistant, Text: state.Text, ToolCalls: state.Calls}
			toolMessage := llm.Message{Role: llm.RoleTool, ToolResults: state.Results}
			messages = append(messages, assistantMessage, toolMessage)
			state.Messages = append(state.Messages, assistantMessage, toolMessage)
			state.Calls, state.Results, state.Text, state.ToolTitle = nil, nil, "", ""
		}
		if state.Iteration >= a.config.MaxIterations {
			return result, fmt.Errorf("agent reached maximum of %d iterations", a.config.MaxIterations)
		}
		state.Iteration++
		iteration := state.Iteration
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
			if err := emitAgentEvent(emit, Event{Type: EventCompleted, FinishReason: finish, Iteration: iteration}); err != nil {
				return result, err
			}
			state.Messages = append(state.Messages, llm.Message{Role: llm.RoleAssistant, Text: turnText.String()})
			return TurnResult{Messages: state.Messages, FinishReason: finish}, nil
		}
		if finish != llm.FinishReasonToolCalls {
			return result, fmt.Errorf("LLM iteration %d emitted tool calls with finish reason %q", iteration, finish)
		}
		if len(calls) > maxToolCallsPerTurn {
			return result, fmt.Errorf("LLM iteration %d requested %d tools, maximum is %d", iteration, len(calls), maxToolCallsPerTurn)
		}
		if state.TotalToolCalls+len(calls) > maxToolCallsPerAsk {
			return result, fmt.Errorf("agent tool call count would exceed maximum of %d", maxToolCallsPerAsk)
		}
		if iteration == a.config.MaxIterations {
			return result, fmt.Errorf("agent reached maximum of %d iterations before a final answer", a.config.MaxIterations)
		}
		for _, call := range calls {
			if _, ok := toolsByName[call.Name]; !ok {
				return result, fmt.Errorf("LLM requested unknown MCP tool %q", call.Name)
			}
		}
		state.TotalToolCalls += len(calls)
		state.Calls, state.Results, state.Text = calls, nil, turnText.String()
	}
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
