//go:build integration

package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/hugobrenet/opensvc-ai-agent/internal/agent"
	"github.com/hugobrenet/opensvc-ai-agent/internal/api"
	"github.com/hugobrenet/opensvc-ai-agent/internal/auth"
	"github.com/hugobrenet/opensvc-ai-agent/internal/config"
	"github.com/hugobrenet/opensvc-ai-agent/internal/llmfactory"
	"github.com/hugobrenet/opensvc-ai-agent/internal/mcpclient"
)

func TestLiveAskStreamsClusterHealth(t *testing.T) {
	mcpEndpoint := os.Getenv("OPENSVC_AI_TEST_MCP_ENDPOINT")
	mcpJWT := os.Getenv("OPENSVC_AI_TEST_MCP_JWT")
	if mcpEndpoint == "" || mcpJWT == "" || os.Getenv("OPENSVC_AI_LLM_PROTOCOL") == "" {
		t.Skip("live MCP and LLM configuration is unavailable")
	}
	llmConfig, err := config.LoadLLM()
	if err != nil {
		t.Fatalf("load live LLM configuration: %v", err)
	}
	agentConfig, err := config.LoadAgent()
	if err != nil {
		t.Fatalf("load live agent configuration: %v", err)
	}
	model, err := llmfactory.New(llmConfig, nil)
	if err != nil {
		t.Fatalf("create live LLM client: %v", err)
	}
	mcpClient, err := mcpclient.New(mcpEndpoint, nil)
	if err != nil {
		t.Fatalf("create live MCP client: %v", err)
	}
	orchestrator, err := agent.New(model, func(ctx context.Context) (agent.MCPSession, error) {
		return mcpClient.Connect(ctx)
	}, agent.Config{MaxIterations: agentConfig.MaxIterations, Timeout: agentConfig.Timeout})
	if err != nil {
		t.Fatalf("create live agent: %v", err)
	}
	jwtConfig := config.LoadJWT()
	verifier, err := auth.NewJWTVerifier(jwtConfig.VerifyKeyFile)
	if err != nil {
		t.Fatalf("create live JWT verifier: %v", err)
	}
	handler, err := api.NewHandler(orchestrator, verifier, api.HandlerConfig{MaxConcurrentAsks: 4})
	if err != nil {
		t.Fatalf("create live API: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/v1/ask", strings.NewReader(`{"prompt":"Use get_cluster_health to assess the current OpenSVC cluster health, then answer concisely."}`))
	if err != nil {
		t.Fatalf("create live ask request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+mcpJWT)
	request.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("send live ask request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("live ask returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		t.Fatalf("read live ask stream: %v", err)
	}
	body := string(data)
	for _, expected := range []string{`"type":"tool_started"`, `"tool_name":"get_cluster_health"`, `"type":"completed"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("live ask stream does not contain %s: %s", expected, body)
		}
	}
	if strings.Contains(body, mcpJWT) {
		t.Fatal("live ask stream exposes delegated JWT")
	}
}
