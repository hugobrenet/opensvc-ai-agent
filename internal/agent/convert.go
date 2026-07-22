package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/hugobrenet/opensvc-ai-agent/internal/llm"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	maxToolCallsPerTurn = 4
	maxToolArguments    = 256 << 10
	maxToolResult       = 1 << 20
)

func convertTools(tools []*mcp.Tool) ([]llm.Tool, map[string]struct{}, error) {
	converted := make([]llm.Tool, 0, len(tools))
	names := make(map[string]struct{}, len(tools))
	for index, tool := range tools {
		if tool == nil {
			return nil, nil, fmt.Errorf("MCP tool %d is nil", index)
		}
		if _, exists := names[tool.Name]; exists {
			return nil, nil, fmt.Errorf("MCP tool name %q is duplicated", tool.Name)
		}
		schema, err := json.Marshal(tool.InputSchema)
		if err != nil {
			return nil, nil, fmt.Errorf("encode MCP tool %q input schema: %w", tool.Name, err)
		}
		names[tool.Name] = struct{}{}
		converted = append(converted, llm.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: schema,
		})
	}
	validationRequest := llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Text: "validate MCP tools"}},
		Tools:    converted,
	}
	if err := validationRequest.Validate(); err != nil {
		return nil, nil, fmt.Errorf("validate MCP tools: %w", err)
	}
	return converted, names, nil
}

func decodeToolArguments(call llm.ToolCall) (map[string]any, error) {
	if len(call.Arguments) > maxToolArguments {
		return nil, fmt.Errorf("tool %q arguments exceed %d bytes", call.Name, maxToolArguments)
	}
	decoder := json.NewDecoder(bytes.NewReader(call.Arguments))
	decoder.UseNumber()
	var arguments map[string]any
	if err := decoder.Decode(&arguments); err != nil {
		return nil, fmt.Errorf("decode tool %q arguments: %w", call.Name, err)
	}
	if arguments == nil {
		return nil, fmt.Errorf("decode tool %q arguments: expected a JSON object", call.Name)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("decode tool %q arguments: trailing JSON data", call.Name)
	}
	return arguments, nil
}

func encodeToolResult(result *mcp.CallToolResult) (json.RawMessage, error) {
	if result == nil {
		return nil, fmt.Errorf("MCP tool returned a nil result")
	}
	content := result.Content
	if content == nil {
		content = []mcp.Content{}
	}
	payload := struct {
		Content           []mcp.Content `json:"content"`
		StructuredContent any           `json:"structuredContent,omitempty"`
	}{
		Content:           content,
		StructuredContent: result.StructuredContent,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode MCP tool result: %w", err)
	}
	if len(data) > maxToolResult {
		return nil, fmt.Errorf("MCP tool result exceeds %d bytes", maxToolResult)
	}
	return data, nil
}
