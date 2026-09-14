//go:build integration

package mcpclient

import (
	"os"
	"testing"

	"github.com/hugobrenet/opensvc-ai-agent/internal/auth"
)

func TestLiveMCPListsTools(t *testing.T) {
	socketPath := os.Getenv("OPENSVC_AI_TEST_MCP_SOCKET_PATH")
	token := os.Getenv("OPENSVC_AI_TEST_MCP_JWT")
	if socketPath == "" || token == "" {
		t.Skip("OPENSVC_AI_TEST_MCP_SOCKET_PATH and OPENSVC_AI_TEST_MCP_JWT are required")
	}

	client, err := New(socketPath)
	if err != nil {
		t.Fatalf("create MCP client: %v", err)
	}
	ctx := auth.WithBearerToken(t.Context(), token)
	session, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("close MCP session: %v", err)
		}
	})

	tools, err := session.ListTools(ctx)
	if err != nil {
		t.Fatalf("list MCP tools: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("MCP server returned no tools")
	}
	t.Logf("MCP server exposed %d tools", len(tools))
}
