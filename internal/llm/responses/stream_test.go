package responses

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hugobrenet/opensvc-ai-agent/internal/llm"
)

func TestStreamStateBoundsPendingToolCalls(t *testing.T) {
	state := &streamState{emit: func(llm.Event) error { return nil }, calls: make(map[int]*pendingCall)}
	for index := 0; index < maxPendingToolCallCount; index++ {
		data := []byte(fmt.Sprintf(
			`{"type":"response.output_item.added","output_index":%d,"item":{"type":"function_call","call_id":"call-%d","name":"tool","arguments":"{}"}}`,
			index,
			index,
		))
		if err := state.consume(data); err != nil {
			t.Fatalf("consume tool call %d: %v", index, err)
		}
	}
	extra := []byte(fmt.Sprintf(
		`{"type":"response.output_item.added","output_index":%d,"item":{"type":"function_call","call_id":"extra","name":"tool","arguments":"{}"}}`,
		maxPendingToolCallCount,
	))
	if err := state.consume(extra); err == nil || !strings.Contains(err.Error(), "count exceeds") {
		t.Fatalf("extra tool call error = %v, want pending count limit", err)
	}
}
