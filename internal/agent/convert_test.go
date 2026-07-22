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

func TestConvertToolsBoundsModelVisibleDefinitions(t *testing.T) {
	for _, test := range []struct {
		name  string
		tools []*mcp.Tool
		want  string
	}{
		{
			name:  "name",
			tools: []*mcp.Tool{{Name: strings.Repeat("n", maxToolNameBytes+1), InputSchema: objectSchema()}},
			want:  "name exceeds",
		},
		{
			name:  "description",
			tools: []*mcp.Tool{{Name: "tool", Description: strings.Repeat("d", maxToolDescriptionBytes+1), InputSchema: objectSchema()}},
			want:  "description exceeds",
		},
		{
			name: "input schema",
			tools: []*mcp.Tool{{
				Name:        "tool",
				InputSchema: map[string]any{"type": "object", "description": strings.Repeat("s", maxToolInputSchemaBytes)},
			}},
			want: "input schema exceeds",
		},
		{
			name:  "aggregate catalog",
			tools: modelCatalogTools(),
			want:  "model tool catalog exceeds",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := convertTools(test.tools); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("convertTools() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func modelCatalogTools() []*mcp.Tool {
	const schemaPadding = 220 << 10
	tools := make([]*mcp.Tool, 0, 5)
	for index := 0; index < 5; index++ {
		tools = append(tools, &mcp.Tool{
			Name:        string(rune('a' + index)),
			InputSchema: map[string]any{"type": "object", "description": strings.Repeat("s", schemaPadding)},
		})
	}
	return tools
}
