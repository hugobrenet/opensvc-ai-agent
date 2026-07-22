package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
