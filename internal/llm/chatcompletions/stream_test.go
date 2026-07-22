package chatcompletions

import (
	"strings"
	"testing"

	"github.com/hugobrenet/opensvc-ai-agent/internal/llm"
)

func TestStreamStateBoundsPendingToolCalls(t *testing.T) {
	state := &streamState{emit: func(llm.Event) error { return nil }, calls: make(map[int]*pendingCall)}
	for index := 0; index < maxPendingToolCallCount; index++ {
		callIndex := index
		delta := wireToolDelta{Index: &callIndex, ID: "call", Type: "function"}
		delta.Function.Name = "tool"
		delta.Function.Arguments = "{}"
		if err := state.consumeToolDelta(delta); err != nil {
			t.Fatalf("consume tool call %d: %v", index, err)
		}
	}
	extraIndex := maxPendingToolCallCount
	extra := wireToolDelta{Index: &extraIndex, ID: "extra", Type: "function"}
	extra.Function.Name = "tool"
	extra.Function.Arguments = "{}"
	if err := state.consumeToolDelta(extra); err == nil || !strings.Contains(err.Error(), "count exceeds") {
		t.Fatalf("extra tool call error = %v, want pending count limit", err)
	}
}
