package mcpclient

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/hugobrenet/opensvc-ai-agent/internal/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	clientName           = "opensvc-ai-agent"
	clientVersion        = "v0.1.0"
	streamableEndpoint   = "http://localhost/mcp"
	maximumUnixPathBytes = 107
)

// Client creates request-scoped MCP sessions. It never retains a Bearer token.
type Client struct {
	endpoint   string
	httpClient *http.Client
}

// New creates an MCP client that carries Streamable HTTP over a Unix socket.
func New(socketPath string) (*Client, error) {
	path, err := cleanUnixSocketPath(socketPath)
	if err != nil {
		return nil, fmt.Errorf("parse MCP Unix socket path: %w", err)
	}

	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "unix", path)
	}

	return &Client{
		endpoint:   streamableEndpoint,
		httpClient: securedHTTPClient(&http.Client{Transport: transport}),
	}, nil
}

func securedHTTPClient(base *http.Client) *http.Client {
	clientCopy := *base
	baseTransport := clientCopy.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	clientCopy.Transport = responseLimitTransport{
		base:     bearerTransport{base: baseTransport},
		maxBytes: maxMCPResponseBodyBytes,
	}
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &clientCopy
}

// Connect initializes an MCP session using the delegated JWT in ctx.
func (c *Client) Connect(ctx context.Context) (*Session, error) {
	if _, ok := auth.BearerTokenFromContext(ctx); !ok {
		return nil, fmt.Errorf("connect MCP: delegated OpenSVC access JWT is missing from request context")
	}

	client := mcp.NewClient(
		&mcp.Implementation{Name: clientName, Version: clientVersion},
		&mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}},
	)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             c.endpoint,
		HTTPClient:           c.httpClient,
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect MCP: %w", err)
	}
	return &Session{session: session}, nil
}

// Session is an initialized MCP session scoped to one agent request.
type Session struct {
	session *mcp.ClientSession
}

// ListTools returns every tool exposed by the MCP server, following pagination.
func (s *Session) ListTools(ctx context.Context) ([]*mcp.Tool, error) {
	catalog := toolCatalog{tools: make([]*mcp.Tool, 0, maxMCPToolCount)}
	for tool, err := range s.session.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("list MCP tools: %w", err)
		}
		if err := catalog.add(tool); err != nil {
			return nil, fmt.Errorf("list MCP tools: %w", err)
		}
	}
	return catalog.tools, nil
}

// CallTool invokes a named MCP tool with JSON-compatible arguments.
func (s *Session) CallTool(ctx context.Context, name string, arguments map[string]any) (*mcp.CallToolResult, error) {
	if name == "" {
		return nil, fmt.Errorf("call MCP tool: tool name is empty")
	}
	result, err := s.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return nil, fmt.Errorf("call MCP tool %q: %w", name, err)
	}
	return result, nil
}

// Close terminates the MCP session.
func (s *Session) Close() error {
	if err := s.session.Close(); err != nil {
		return fmt.Errorf("close MCP session: %w", err)
	}
	return nil
}

type bearerTransport struct {
	base http.RoundTripper
}

func (t bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	token, ok := auth.BearerTokenFromContext(request.Context())
	if !ok {
		return nil, fmt.Errorf("send MCP request: delegated OpenSVC access JWT is missing from request context")
	}

	requestCopy := request.Clone(request.Context())
	requestCopy.Header = request.Header.Clone()
	requestCopy.Header.Set("Authorization", "Bearer "+token)
	return t.base.RoundTrip(requestCopy)
}

func cleanUnixSocketPath(value string) (string, error) {
	path := filepath.Clean(strings.TrimSpace(value))
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be absolute")
	}
	if path == string(filepath.Separator) {
		return "", fmt.Errorf("path must name a socket")
	}
	if len([]byte(path)) > maximumUnixPathBytes {
		return "", fmt.Errorf("path exceeds the Linux Unix socket limit of %d bytes", maximumUnixPathBytes)
	}
	return path, nil
}
