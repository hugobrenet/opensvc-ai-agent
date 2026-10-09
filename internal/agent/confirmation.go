package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/opensvc/ai-agent/internal/llm"
)

// Decision is the answer of the user to a confirmation request.
type Decision string

const (
	DecisionApprove Decision = "approve"
	DecisionReject  Decision = "reject"
)

func (d Decision) Valid() bool {
	return d == DecisionApprove || d == DecisionReject
}

// rejectedToolResult is the tool result the model receives for an action the
// user rejected: the call was not executed.
var rejectedToolResult = json.RawMessage(`{"content":[{"type":"text","text":"The user rejected this action. It was not executed."}]}`)

// unconfirmableToolResult is the tool result the model receives for an action
// requiring confirmation in a request that cannot wait for one.
var unconfirmableToolResult = json.RawMessage(`{"content":[{"type":"text","text":"This action requires the confirmation of the user, which only a conversation can request. It was not executed."}]}`)

// SuspendedTurn is the state of a turn waiting for the user to confirm a tool
// call. It holds everything RunTurn needs to resume: the messages of the turn
// so far, and the calls of the current model iteration with the results
// already obtained. The pending call is Calls[len(Results)].
type SuspendedTurn struct {
	Messages       []llm.Message    `json:"messages"`
	Iteration      int              `json:"iteration"`
	TotalToolCalls int              `json:"total_tool_calls"`
	Text           string           `json:"text"`
	Calls          []llm.ToolCall   `json:"calls"`
	Results        []llm.ToolResult `json:"results"`
	ToolTitle      string           `json:"tool_title"`
}

// PendingCall returns the call waiting for confirmation.
func (s SuspendedTurn) PendingCall() (llm.ToolCall, error) {
	if len(s.Results) >= len(s.Calls) {
		return llm.ToolCall{}, fmt.Errorf("suspended turn has no pending tool call")
	}
	return s.Calls[len(s.Results)], nil
}

func (s SuspendedTurn) validate(maxIterations int) error {
	if len(s.Messages) == 0 || s.Messages[0].Role != llm.RoleUser {
		return fmt.Errorf("suspended turn does not start with a user message")
	}
	if s.Iteration <= 0 || s.Iteration >= maxIterations {
		return fmt.Errorf("suspended turn iteration %d is out of range", s.Iteration)
	}
	if len(s.Calls) == 0 || len(s.Calls) > maxToolCallsPerTurn || len(s.Results) >= len(s.Calls) {
		return fmt.Errorf("suspended turn has no pending tool call")
	}
	if s.TotalToolCalls < len(s.Calls) || s.TotalToolCalls > maxToolCallsPerAsk {
		return fmt.Errorf("suspended turn tool call count %d is out of range", s.TotalToolCalls)
	}
	for index, result := range s.Results {
		if result.CallID != s.Calls[index].ID {
			return fmt.Errorf("suspended turn result %d does not match its call", index)
		}
	}
	return nil
}

// ArgumentsHash binds a confirmation to the exact arguments shown to the user.
func ArgumentsHash(arguments json.RawMessage) string {
	sum := sha256.Sum256(arguments)
	return hex.EncodeToString(sum[:])
}

// Confirmation describes a tool call waiting for the decision of the user.
type Confirmation struct {
	ID          string
	TurnID      string
	ToolName    string
	ToolTitle   string
	Destructive bool
	Arguments   json.RawMessage
	ExpiresAt   time.Time
}

// requiresConfirmation tells whether a tool call must be confirmed by the
// user before it runs. MCP annotations are hints: a missing destructiveHint
// on a tool that is not read-only means destructive, so the absence of
// annotations requires a confirmation.
func requiresConfirmation(tool *mcp.Tool) bool {
	annotations := tool.Annotations
	if annotations != nil && annotations.ReadOnlyHint {
		return false
	}
	if annotations == nil || annotations.DestructiveHint == nil {
		return true
	}
	return *annotations.DestructiveHint
}

// toolTitle is the human title of a tool, its name when it declares none.
func toolTitle(tool *mcp.Tool) string {
	if tool.Title != "" {
		return tool.Title
	}
	if tool.Annotations != nil && tool.Annotations.Title != "" {
		return tool.Annotations.Title
	}
	return tool.Name
}
