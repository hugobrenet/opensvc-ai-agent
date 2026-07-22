package mcpclient

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"

	"github.com/hugobrenet/opensvc-ai-agent/internal/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	clientName    = "opensvc-ai-agent"
	clientVersion = "v0.1.0"
)

// Client creates request-scoped MCP sessions. It never retains a Bearer token.
type Client struct {
	endpoint   string
	httpClient *http.Client
}

// New creates an MCP client for a Streamable HTTP endpoint.
func New(endpoint string, httpClient *http.Client) (*Client, error) {
	if err := validateEndpoint(endpoint); err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	clientCopy := *httpClient
	baseTransport := clientCopy.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	clientCopy.Transport = responseLimitTransport{
		base:     bearerTransport{base: baseTransport},
		maxBytes: maxMCPResponseBodyBytes,
	}

	return &Client{endpoint: endpoint, httpClient: &clientCopy}, nil
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
	var tools []*mcp.Tool
	for tool, err := range s.session.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("list MCP tools: %w", err)
		}
		tools = append(tools, tool)
	}
	return tools, nil
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

func validateEndpoint(endpoint string) error {
	parsed, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return fmt.Errorf("parse MCP endpoint: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("MCP endpoint scheme must be http or https")
	}
	if parsed.Host == "" {
		return fmt.Errorf("MCP endpoint host is empty")
	}
	if parsed.User != nil {
		return fmt.Errorf("MCP endpoint must not contain user information")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("MCP endpoint must not contain a query or fragment")
	}
	if parsed.Scheme == "http" {
		host := parsed.Hostname()
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("plain HTTP MCP endpoint must use a loopback IP")
		}
	}
	return nil
}
