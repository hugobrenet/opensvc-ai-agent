package config

import (
	"fmt"
	"os"
	"strings"
)

const DefaultMCPSocketPath = "/run/opensvc-daemon-mcp/mcp.sock"

type MCPConfig struct {
	SocketPath string
}

func LoadMCP() (MCPConfig, error) {
	return loadMCP(os.Getenv)
}

func loadMCP(getenv func(string) string) (MCPConfig, error) {
	socketPath := strings.TrimSpace(getenv("OPENSVC_AI_MCP_SOCKET_PATH"))
	if socketPath == "" {
		socketPath = DefaultMCPSocketPath
	}
	socketPath, err := cleanUnixSocketPath(socketPath)
	if err != nil {
		return MCPConfig{}, fmt.Errorf("parse OPENSVC_AI_MCP_SOCKET_PATH: %w", err)
	}
	return MCPConfig{SocketPath: socketPath}, nil
}
