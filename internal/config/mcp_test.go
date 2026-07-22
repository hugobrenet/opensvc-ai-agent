package config

import (
	"strings"
	"testing"
)

func TestLoadMCP(t *testing.T) {
	config, err := loadMCP(func(key string) string {
		if key != "OPENSVC_AI_MCP_ENDPOINT" {
			t.Fatalf("unexpected environment key %q", key)
		}
		return "  http://127.0.0.1:8082/mcp  "
	})
	if err != nil {
		t.Fatalf("load MCP config: %v", err)
	}
	if config.Endpoint != "http://127.0.0.1:8082/mcp" {
		t.Fatalf("got endpoint %q", config.Endpoint)
	}
}

func TestLoadMCPRequiresEndpoint(t *testing.T) {
	_, err := loadMCP(func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "OPENSVC_AI_MCP_ENDPOINT is required") {
		t.Fatalf("loadMCP() error = %v", err)
	}
}
