package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hugobrenet/opensvc-ai-agent/internal/agent"
	"github.com/hugobrenet/opensvc-ai-agent/internal/api"
	"github.com/hugobrenet/opensvc-ai-agent/internal/auth"
	"github.com/hugobrenet/opensvc-ai-agent/internal/config"
	"github.com/hugobrenet/opensvc-ai-agent/internal/conversation"
	conversationsqlite "github.com/hugobrenet/opensvc-ai-agent/internal/conversation/sqlite"
	"github.com/hugobrenet/opensvc-ai-agent/internal/llmfactory"
	"github.com/hugobrenet/opensvc-ai-agent/internal/mcpclient"
)

const (
	maxHTTPHeaderBytes = 64 << 10
	unixSocketMode     = 0o660
	socketProbeTimeout = 100 * time.Millisecond
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
	conversationConfig, err := config.LoadConversation()
	if err != nil {
		log.Fatalf("load conversation configuration: %v", err)
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
	startupContext, cancelStartup := context.WithTimeout(context.Background(), 30*time.Second)
	conversationStore, err := conversationsqlite.Open(startupContext, conversationsqlite.Config{Path: conversationConfig.DatabasePath})
	if err != nil {
		cancelStartup()
		log.Fatalf("open conversation store: %v", err)
	}
	conversationService, err := conversation.NewService(conversationStore, orchestrator, conversation.ServiceConfig{Lifetime: conversationConfig.Lifetime})
	if err != nil {
		cancelStartup()
		_ = conversationStore.Close()
		log.Fatalf("create conversation service: %v", err)
	}
	if recovered, recoverErr := conversationService.Recover(startupContext); recoverErr != nil {
		cancelStartup()
		_ = conversationStore.Close()
		log.Fatalf("recover interrupted conversation turns: %v", recoverErr)
	} else if recovered != 0 {
		log.Printf("recovered %d interrupted conversation turns", recovered)
	}
	if _, err := conversationService.DeleteExpired(startupContext); err != nil {
		cancelStartup()
		_ = conversationStore.Close()
		log.Fatalf("delete expired conversations: %v", err)
	}
	cancelStartup()
	defer func() {
		if err := conversationStore.Close(); err != nil {
			log.Printf("close conversation store: %v", err)
		}
	}()
	auditLogger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	handler, err := api.NewHandler(orchestrator, conversationService, verifier, api.HandlerConfig{
		MaxConcurrentAsks: processConfig.MaxConcurrentAsks,
		AuditLogger:       auditLogger,
	})
	if err != nil {
		_ = conversationStore.Close()
		log.Fatalf("create HTTP API: %v", err)
	}

	listener, err := listenUnixSocket(processConfig.SocketPath)
	if err != nil {
		_ = conversationStore.Close()
		log.Fatalf("listen for HTTP API: %v", err)
	}
	server := newHTTPServer(listener.Addr().String(), handler)
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()

	signalContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	log.Printf("opensvc-ai-agentd listening on unix://%s", processConfig.SocketPath)
	select {
	case err := <-serveErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("serve HTTP API: %v", err)
			return
		}
	case <-signalContext.Done():
		stopSignals()
		log.Printf("opensvc-ai-agentd shutting down with a %s deadline", processConfig.ShutdownTimeout)
		if err := shutdownHTTPServer(server, processConfig.ShutdownTimeout); err != nil {
			log.Printf("force HTTP API shutdown: %v", err)
		}
		if err := <-serveErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("serve HTTP API during shutdown: %v", err)
		}
	}
}

func listenUnixSocket(path string) (*net.UnixListener, error) {
	if err := removeStaleUnixSocket(path); err != nil {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("listen on Unix socket %s: %w", path, err)
	}
	listener.SetUnlinkOnClose(true)
	if err := os.Chmod(path, unixSocketMode); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("set Unix socket %s mode to %04o: %w", path, unixSocketMode, err)
	}
	return listener, nil
}

func removeStaleUnixSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect Unix socket path %s: %w", path, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refuse to remove non-socket path %s", path)
	}

	connection, probeErr := net.DialTimeout("unix", path, socketProbeTimeout)
	if probeErr == nil {
		_ = connection.Close()
		return fmt.Errorf("Unix socket %s is already accepting connections", path)
	}
	if errors.Is(probeErr, os.ErrNotExist) {
		return nil
	}
	if !errors.Is(probeErr, syscall.ECONNREFUSED) {
		return fmt.Errorf("probe existing Unix socket %s: %w", path, probeErr)
	}

	currentInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reinspect stale Unix socket %s: %w", path, err)
	}
	if currentInfo.Mode()&os.ModeSocket == 0 || !os.SameFile(info, currentInfo) {
		return fmt.Errorf("Unix socket path %s changed while checking whether it was stale", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale Unix socket %s: %w", path, err)
	}
	return nil
}

func newHTTPServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    maxHTTPHeaderBytes,
	}
}

func shutdownHTTPServer(server *http.Server, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		shutdownErr := fmt.Errorf("graceful HTTP shutdown: %w", err)
		if closeErr := server.Close(); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
			return errors.Join(shutdownErr, fmt.Errorf("close HTTP server: %w", closeErr))
		}
		return shutdownErr
	}
	return nil
}
