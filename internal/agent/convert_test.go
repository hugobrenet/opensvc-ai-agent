package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hugobrenet/opensvc-ai-agent/internal/llm"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDecodeToolArgumentsPreservesNumbers(t *testing.T) {
	arguments, err := decodeToolArguments(llm.ToolCall{Name: "tool", Arguments: json.RawMessage(`{"value":9007199254740993}`)})
	if err != nil {
		t.Fatalf("decode arguments: %v", err)
	}
	if got := arguments["value"].(json.Number).String(); got != "9007199254740993" {
		t.Fatalf("got number %s", got)
	}
}

func TestEncodeToolResultRejectsOversizedResult(t *testing.T) {
	result := &mcp.CallToolResult{StructuredContent: map[string]any{"value": strings.Repeat("x", maxToolResult)}}
	if _, err := encodeToolResult(result); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("encodeToolResult() error = %v", err)
	}
}

func TestConvertToolsRejectsInvalidTools(t *testing.T) {
	for _, tools := range [][]*mcp.Tool{
		{nil},
		{{Name: "same", InputSchema: objectSchema()}, {Name: "same", InputSchema: objectSchema()}},
		{{Name: "bad", InputSchema: []any{}}},
	} {
		if _, _, err := convertTools(tools); err == nil {
			t.Fatalf("convertTools(%#v) succeeded", tools)
		}
	}
}
