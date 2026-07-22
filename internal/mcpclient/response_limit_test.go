package mcpclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hugobrenet/opensvc-ai-agent/internal/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestResponseLimitTransportBoundsUnknownLengthBodies(t *testing.T) {
	const maxBytes = int64(4)
	for _, test := range []struct {
		name    string
		payload string
		wantErr bool
	}{
		{name: "under limit", payload: "abc"},
		{name: "exact limit", payload: "abcd"},
		{name: "over limit", payload: "abcde", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := &trackingReadCloser{Reader: strings.NewReader(test.payload)}
			transport := responseLimitTransport{
				base: roundTripperFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, ContentLength: -1, Body: body}, nil
				}),
				maxBytes: maxBytes,
			}
			response, err := transport.RoundTrip(httptest.NewRequest(http.MethodGet, "https://mcp.example.test", nil))
			if err != nil {
				t.Fatalf("RoundTrip() error: %v", err)
			}
			_, readErr := io.ReadAll(response.Body)
			if got := errors.Is(readErr, errMCPResponseBodyTooLarge); got != test.wantErr {
				t.Fatalf("read error = %v, oversized=%v, want %v", readErr, got, test.wantErr)
			}
			if test.wantErr && !body.closed {
				t.Fatal("oversized response body was not closed when the limit was reached")
			}
			if err := response.Body.Close(); err != nil {
				t.Fatalf("close response body: %v", err)
			}
			if !body.closed {
				t.Fatal("underlying response body was not closed")
			}
		})
	}
}

func TestResponseLimitTransportRejectsOversizedContentLength(t *testing.T) {
	const sensitiveMarker = "sensitive-response-marker"
	body := &trackingReadCloser{Reader: strings.NewReader(sensitiveMarker)}
	transport := responseLimitTransport{
		base: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, ContentLength: 5, Body: body}, nil
		}),
		maxBytes: 4,
	}
	response, err := transport.RoundTrip(httptest.NewRequest(http.MethodGet, "https://mcp.example.test", nil))
	if response != nil || !errors.Is(err, errMCPResponseBodyTooLarge) {
		t.Fatalf("RoundTrip() response=%v error=%v, want oversized error", response, err)
	}
	if !body.closed {
		t.Fatal("oversized response body was not closed")
	}
	if strings.Contains(err.Error(), sensitiveMarker) {
		t.Fatalf("oversized error exposes response content: %q", err)
	}
}

func TestClientRejectsOversizedToolResponse(t *testing.T) {
	const (
		token           = "delegated-test-token"
		sensitiveMarker = "sensitive-tool-result-marker"
	)
	server := mcp.NewServer(&mcp.Implementation{Name: "test-mcp", Version: "v0.1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "oversized_result"},
		func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, map[string]string, error) {
			content := sensitiveMarker + strings.Repeat("x", int(maxMCPResponseBodyBytes)+1)
			return nil, map[string]string{"content": content}, nil
		})
	streamHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+token {
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		streamHandler.ServeHTTP(response, request)
	}))
	t.Cleanup(httpServer.Close)

	client, err := New(httpServer.URL, httpServer.Client())
	if err != nil {
		t.Fatalf("create MCP client: %v", err)
	}
	ctx := auth.WithBearerToken(t.Context(), token)
	session, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	_, err = session.CallTool(ctx, "oversized_result", map[string]any{})
	if err == nil {
		t.Fatal("oversized MCP tool response succeeded")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("response limit error exposes JWT: %q", err)
	}
	if strings.Contains(err.Error(), sensitiveMarker) {
		t.Fatalf("response limit error exposes tool result: %q", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type trackingReadCloser struct {
	io.Reader
	closed bool
}

func (b *trackingReadCloser) Close() error {
	b.closed = true
	return nil
}
