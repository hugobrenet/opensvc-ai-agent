package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hugobrenet/opensvc-ai-agent/internal/config"
)

func TestNewHTTPServerHardening(t *testing.T) {
	server := newHTTPServer("127.0.0.1:8090", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	if server.MaxHeaderBytes != maxHTTPHeaderBytes {
		t.Fatalf("MaxHeaderBytes = %d, want %d", server.MaxHeaderBytes, maxHTTPHeaderBytes)
	}
	if server.ReadHeaderTimeout <= 0 || server.ReadTimeout <= 0 || server.IdleTimeout <= 0 {
		t.Fatalf("server timeouts are not all positive: %+v", server)
	}
}

func TestListenHTTPAPIUsesTCPFallback(t *testing.T) {
	listener, description, err := listenHTTPAPI(config.Config{ListenAddress: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("listen for HTTP API: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if listener.Addr().Network() != "tcp" || !strings.HasPrefix(description, "http://127.0.0.1:") {
		t.Fatalf("listener = %s, description = %q", listener.Addr().Network(), description)
	}
}

func TestListenUnixSocketCreatesPermissionedSocketAndCleansUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.sock")
	listener, err := listenUnixSocket(path)
	if err != nil {
		t.Fatalf("listen on Unix socket: %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat Unix socket: %v", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("path mode %s is not a socket", info.Mode())
	}
	if got := info.Mode().Perm(); got != unixSocketMode {
		t.Fatalf("socket mode = %04o, want %04o", got, unixSocketMode)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("close Unix socket: %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket remains after listener close: %v", err)
	}
}

func TestListenUnixSocketReplacesOnlyStaleSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.sock")
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("create stale Unix socket: %v", err)
	}
	stale.SetUnlinkOnClose(false)
	if err := stale.Close(); err != nil {
		t.Fatalf("close stale Unix socket: %v", err)
	}

	listener, err := listenUnixSocket(path)
	if err != nil {
		t.Fatalf("replace stale Unix socket: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
}

func TestListenUnixSocketRefusesActiveSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.sock")
	active, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("create active Unix socket: %v", err)
	}
	t.Cleanup(func() { _ = active.Close() })

	if _, err := listenUnixSocket(path); err == nil || !strings.Contains(err.Error(), "already accepting connections") {
		t.Fatalf("listen error = %v", err)
	}
}

func TestListenUnixSocketRefusesNonSocketPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.sock")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatalf("create ordinary file: %v", err)
	}
	if _, err := listenUnixSocket(path); err == nil || !strings.Contains(err.Error(), "refuse to remove non-socket") {
		t.Fatalf("listen error = %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "keep" {
		t.Fatalf("ordinary file changed: contents=%q error=%v", contents, err)
	}
}

func TestShutdownHTTPServerDrainsActiveRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(response, "done")
	}))
	t.Cleanup(server.Close)

	requestDone := make(chan error, 1)
	go func() {
		response, err := server.Client().Get(server.URL)
		if err == nil {
			_, err = io.ReadAll(response.Body)
			_ = response.Body.Close()
		}
		requestDone <- err
	}()
	<-started

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- shutdownHTTPServer(server.Config, time.Second)
	}()
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown returned before active request completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)

	if err := <-requestDone; err != nil {
		t.Fatalf("active request failed during graceful shutdown: %v", err)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("graceful shutdown: %v", err)
	}
}

func TestShutdownHTTPServerForcesCancellationAfterDeadline(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
		close(canceled)
	}))
	t.Cleanup(server.Close)

	requestDone := make(chan error, 1)
	go func() {
		response, err := server.Client().Get(server.URL)
		if response != nil {
			_ = response.Body.Close()
		}
		requestDone <- err
	}()
	<-started

	err := shutdownHTTPServer(server.Config, 20*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("forced shutdown error = %v, want deadline exceeded", err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("active request context was not canceled")
	}
	if err := <-requestDone; err == nil {
		t.Fatal("forced connection close returned no client error")
	}
}
