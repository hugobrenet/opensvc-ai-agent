package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/hugobrenet/opensvc-ai-agent/internal/agent"
	"github.com/hugobrenet/opensvc-ai-agent/internal/api"
	"github.com/hugobrenet/opensvc-ai-agent/internal/auth"
	"github.com/hugobrenet/opensvc-ai-agent/internal/config"
	"github.com/hugobrenet/opensvc-ai-agent/internal/llmfactory"
	"github.com/hugobrenet/opensvc-ai-agent/internal/mcpclient"
)

func main() {
	processConfig, err := config.Load()
	if err != nil {
		log.Fatalf("load configuration: %v", err)
	}
	llmConfig, err := config.LoadLLM()
	if err != nil {
		log.Fatalf("load LLM configuration: %v", err)
	}
	agentConfig, err := config.LoadAgent()
	if err != nil {
		log.Fatalf("load agent configuration: %v", err)
	}
	mcpConfig, err := config.LoadMCP()
	if err != nil {
		log.Fatalf("load MCP configuration: %v", err)
	}
	jwtConfig := config.LoadJWT()
	verifier, err := auth.NewJWTVerifier(jwtConfig.VerifyKeyFile)
	if err != nil {
		log.Fatalf("create OpenSVC JWT verifier: %v", err)
	}

	model, err := llmfactory.New(llmConfig, nil)
	if err != nil {
		log.Fatalf("create LLM client: %v", err)
	}
	mcpClient, err := mcpclient.New(mcpConfig.Endpoint, nil)
	if err != nil {
		log.Fatalf("create MCP client: %v", err)
	}
	orchestrator, err := agent.New(model, func(ctx context.Context) (agent.MCPSession, error) {
		return mcpClient.Connect(ctx)
	}, agent.Config{MaxIterations: agentConfig.MaxIterations, Timeout: agentConfig.Timeout})
	if err != nil {
		log.Fatalf("create agent: %v", err)
	}
	handler, err := api.NewHandler(orchestrator, verifier, api.HandlerConfig{MaxConcurrentAsks: processConfig.MaxConcurrentAsks})
	if err != nil {
		log.Fatalf("create HTTP API: %v", err)
	}

	server := &http.Server{
		Addr:              processConfig.ListenAddress,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	log.Printf("opensvc-ai-agentd listening on http://%s", processConfig.ListenAddress)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve HTTP API: %v", err)
	}
}
