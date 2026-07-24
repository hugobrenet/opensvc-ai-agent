package agent

import (
	"encoding/json"
	"fmt"

	"github.com/hugobrenet/opensvc-ai-agent/internal/llm"
)

const (
	maxHistoryMessages = 256
	maxHistoryBytes    = 2 << 20
)

func prepareHistory(history []llm.Message) ([]llm.Message, error) {
	if len(history) == 0 {
		return nil, nil
	}
	if len(history) > maxHistoryMessages {
		return nil, fmt.Errorf("agent history contains %d messages, maximum is %d", len(history), maxHistoryMessages)
	}

	cloned := make([]llm.Message, len(history))
	rawBytes := 0
	for index, message := range history {
		if message.Role == llm.RoleSystem {
			return nil, fmt.Errorf("agent history message %d contains a system prompt", index)
		}
		if len(message.ToolCalls) > maxToolCallsPerTurn {
			return nil, fmt.Errorf("agent history message %d contains %d tool calls, maximum is %d", index, len(message.ToolCalls), maxToolCallsPerTurn)
		}
		if len(message.ToolResults) > maxToolCallsPerTurn {
			return nil, fmt.Errorf("agent history message %d contains %d tool results, maximum is %d", index, len(message.ToolResults), maxToolCallsPerTurn)
		}

		if err := addHistoryBytes(&rawBytes, len(message.Role)+len(message.Text)+64); err != nil {
			return nil, err
		}
		clone := llm.Message{Role: message.Role, Text: message.Text}
		if len(message.ToolCalls) > 0 {
			clone.ToolCalls = make([]llm.ToolCall, len(message.ToolCalls))
		}
		for callIndex, call := range message.ToolCalls {
			if len(call.Arguments) > maxToolArguments {
				return nil, fmt.Errorf("agent history message %d tool call %d arguments exceed %d bytes", index, callIndex, maxToolArguments)
			}
			if err := addHistoryBytes(&rawBytes, len(call.ID)+len(call.Name)+len(call.Arguments)+64); err != nil {
				return nil, err
			}
			clone.ToolCalls[callIndex] = llm.ToolCall{
				ID:        call.ID,
				Name:      call.Name,
				Arguments: append(json.RawMessage(nil), call.Arguments...),
			}
		}
		if len(message.ToolResults) > 0 {
			clone.ToolResults = make([]llm.ToolResult, len(message.ToolResults))
		}
		for resultIndex, toolResult := range message.ToolResults {
			if len(toolResult.Content) > maxToolResult {
				return nil, fmt.Errorf("agent history message %d tool result %d exceeds %d bytes", index, resultIndex, maxToolResult)
			}
			if err := addHistoryBytes(&rawBytes, len(toolResult.CallID)+len(toolResult.Content)+64); err != nil {
				return nil, err
			}
			clone.ToolResults[resultIndex] = llm.ToolResult{
				CallID:  toolResult.CallID,
				Content: append(json.RawMessage(nil), toolResult.Content...),
				IsError: toolResult.IsError,
			}
		}
		cloned[index] = clone
	}

	if err := (llm.Request{Messages: cloned}).Validate(); err != nil {
		return nil, fmt.Errorf("validate agent history: %w", err)
	}
	if err := validateHistorySequence(cloned); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(cloned)
	if err != nil {
		return nil, fmt.Errorf("encode agent history: %w", err)
	}
	if len(encoded) > maxHistoryBytes {
		return nil, fmt.Errorf("agent history exceeds %d bytes", maxHistoryBytes)
	}
	return cloned, nil
}

func addHistoryBytes(total *int, size int) error {
	if size > maxHistoryBytes-*total {
		return fmt.Errorf("agent history exceeds %d bytes", maxHistoryBytes)
	}
	*total += size
	return nil
}

func validateHistorySequence(history []llm.Message) error {
	expectedRole := llm.RoleUser
	var pendingCalls []llm.ToolCall
	for index, message := range history {
		if message.Role != expectedRole {
			return fmt.Errorf("agent history message %d has role %q, expected %q", index, message.Role, expectedRole)
		}
		switch message.Role {
		case llm.RoleUser:
			expectedRole = llm.RoleAssistant
		case llm.RoleAssistant:
			if len(message.ToolCalls) == 0 {
				expectedRole = llm.RoleUser
				pendingCalls = nil
			} else {
				expectedRole = llm.RoleTool
				pendingCalls = message.ToolCalls
			}
		case llm.RoleTool:
			if err := validateToolResultSequence(pendingCalls, message.ToolResults); err != nil {
				return fmt.Errorf("agent history message %d: %w", index, err)
			}
			expectedRole = llm.RoleAssistant
			pendingCalls = nil
		}
	}
	if expectedRole != llm.RoleUser {
		return fmt.Errorf("agent history ends before a final assistant message")
	}
	return nil
}

func validateToolResultSequence(calls []llm.ToolCall, results []llm.ToolResult) error {
	if len(results) != len(calls) {
		return fmt.Errorf("tool result count %d does not match tool call count %d", len(results), len(calls))
	}
	pending := make(map[string]struct{}, len(calls))
	for _, call := range calls {
		if _, exists := pending[call.ID]; exists {
			return fmt.Errorf("tool call ID %q is duplicated", call.ID)
		}
		pending[call.ID] = struct{}{}
	}
	for _, result := range results {
		if _, exists := pending[result.CallID]; !exists {
			return fmt.Errorf("tool result references unexpected call ID %q", result.CallID)
		}
		delete(pending, result.CallID)
	}
	return nil
}
