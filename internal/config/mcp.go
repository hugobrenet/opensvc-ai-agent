package config

import (
	"fmt"
	"os"
	"strings"
)

type MCPConfig struct {
	Endpoint string
}

func LoadMCP() (MCPConfig, error) {
	return loadMCP(os.Getenv)
}

func loadMCP(getenv func(string) string) (MCPConfig, error) {
	endpoint := strings.TrimSpace(getenv("OPENSVC_AI_MCP_ENDPOINT"))
	if endpoint == "" {
		return MCPConfig{}, fmt.Errorf("OPENSVC_AI_MCP_ENDPOINT is required")
	}
	return MCPConfig{Endpoint: endpoint}, nil
}
