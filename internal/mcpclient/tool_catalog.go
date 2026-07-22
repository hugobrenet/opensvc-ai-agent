package mcpclient

import (
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	maxMCPToolCount           = 128
	maxMCPToolDefinitionBytes = 512 << 10
	maxMCPToolCatalogBytes    = 4 << 20
)

type toolCatalog struct {
	tools        []*mcp.Tool
	encodedBytes int
}

func (c *toolCatalog) add(tool *mcp.Tool) error {
	if tool == nil {
		return fmt.Errorf("MCP tool %d is nil", len(c.tools))
	}
	if len(c.tools) >= maxMCPToolCount {
		return fmt.Errorf("MCP tool count exceeds %d", maxMCPToolCount)
	}
	encoded, err := json.Marshal(tool)
	if err != nil {
		return fmt.Errorf("encode MCP tool %d: %w", len(c.tools), err)
	}
	if len(encoded) > maxMCPToolDefinitionBytes {
		return fmt.Errorf("MCP tool %d definition exceeds %d bytes", len(c.tools), maxMCPToolDefinitionBytes)
	}
	if c.encodedBytes+len(encoded) > maxMCPToolCatalogBytes {
		return fmt.Errorf("MCP tool catalog exceeds %d bytes", maxMCPToolCatalogBytes)
	}
	c.encodedBytes += len(encoded)
	c.tools = append(c.tools, tool)
	return nil
}
