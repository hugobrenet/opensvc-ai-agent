package agent

import (
	"fmt"

	"github.com/hugobrenet/opensvc-ai-agent/internal/llm"
)

type EventType string

const (
	EventTextDelta    EventType = "text_delta"
	EventToolStarted  EventType = "tool_started"
	EventToolFinished EventType = "tool_finished"
	EventUsage        EventType = "usage"
	EventCompleted    EventType = "completed"
)

type Event struct {
	Type         EventType
	TextDelta    string
	ToolName     string
	ToolError    bool
	Usage        *llm.Usage
	FinishReason llm.FinishReason
	Iteration    int
}

type EmitFunc func(Event) error

func (e Event) Validate() error {
	if e.Iteration <= 0 {
		return fmt.Errorf("agent event iteration must be positive")
	}
	switch e.Type {
	case EventTextDelta:
		if e.TextDelta == "" || e.ToolName != "" || e.Usage != nil || e.FinishReason != "" {
			return fmt.Errorf("invalid text delta event")
		}
	case EventToolStarted:
		if e.ToolName == "" || e.TextDelta != "" || e.Usage != nil || e.FinishReason != "" || e.ToolError {
			return fmt.Errorf("invalid tool started event")
		}
	case EventToolFinished:
		if e.ToolName == "" || e.TextDelta != "" || e.Usage != nil || e.FinishReason != "" {
			return fmt.Errorf("invalid tool finished event")
		}
	case EventUsage:
		if e.Usage == nil || e.TextDelta != "" || e.ToolName != "" || e.FinishReason != "" {
			return fmt.Errorf("invalid usage event")
		}
	case EventCompleted:
		if e.FinishReason == "" || e.TextDelta != "" || e.ToolName != "" || e.Usage != nil || e.ToolError {
			return fmt.Errorf("invalid completed event")
		}
	default:
		return fmt.Errorf("unsupported agent event type %q", e.Type)
	}
	return nil
}
