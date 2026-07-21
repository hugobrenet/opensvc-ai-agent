package responses

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hugobrenet/opensvc-ai-agent/internal/llm"
)

const (
	AuthModeNone   = "none"
	AuthModeBearer = "bearer"

	maxRequestBytes   = 4 << 20
	maxErrorBodyBytes = 64 << 10
)

type TokenSource func() (string, error)

type Config struct {
	BaseURL         string
	Model           string
	AuthMode        string
	TokenSource     TokenSource
	Timeout         time.Duration
	MaxOutputTokens int
}

// Client implements llm.Client using the Responses HTTP protocol.
type Client struct {
	endpoint        string
	model           string
	authMode        string
	tokenSource     TokenSource
	maxOutputTokens int
	httpClient      *http.Client
}

var _ llm.Client = (*Client)(nil)

func New(config Config, httpClient *http.Client) (*Client, error) {
	endpoint, err := responsesEndpoint(config.BaseURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, fmt.Errorf("Responses model is empty")
	}
	if config.Timeout <= 0 {
		return nil, fmt.Errorf("Responses timeout must be positive")
	}
	if config.MaxOutputTokens <= 0 {
		return nil, fmt.Errorf("Responses max output tokens must be positive")
	}
	switch config.AuthMode {
	case AuthModeNone:
	case AuthModeBearer:
		if config.TokenSource == nil {
			return nil, fmt.Errorf("Responses bearer token source is missing")
		}
	default:
		return nil, fmt.Errorf("unsupported Responses authentication mode %q", config.AuthMode)
	}

	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	clientCopy := *httpClient
	clientCopy.Timeout = config.Timeout
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return &Client{
		endpoint:        endpoint,
		model:           config.Model,
		authMode:        config.AuthMode,
		tokenSource:     config.TokenSource,
		maxOutputTokens: config.MaxOutputTokens,
		httpClient:      &clientCopy,
	}, nil
}

func (c *Client) Stream(ctx context.Context, request llm.Request, emit llm.EmitFunc) error {
	if err := request.Validate(); err != nil {
		return fmt.Errorf("validate LLM request: %w", err)
	}
	if emit == nil {
		return fmt.Errorf("stream Responses: event consumer is nil")
	}

	wireRequest, err := newCreateRequest(c.model, c.maxOutputTokens, request)
	if err != nil {
		return err
	}
	body, err := json.Marshal(wireRequest)
	if err != nil {
		return fmt.Errorf("encode Responses request: %w", err)
	}
	if len(body) > maxRequestBytes {
		return fmt.Errorf("encode Responses request: body exceeds %d bytes", maxRequestBytes)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("create Responses request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream")

	var token string
	if c.authMode == AuthModeBearer {
		token, err = c.tokenSource()
		if err != nil {
			return fmt.Errorf("load Responses API token: %w", err)
		}
		if token == "" {
			return fmt.Errorf("load Responses API token: token is empty")
		}
		httpRequest.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("send Responses request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return readAPIError(response, token)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/event-stream" {
		return fmt.Errorf("read Responses stream: unexpected Content-Type %q", response.Header.Get("Content-Type"))
	}
	if err := consumeStream(response.Body, emit, token); err != nil {
		return fmt.Errorf("read Responses stream: %w", err)
	}
	return nil
}

func responsesEndpoint(baseURL string) (string, error) {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(baseURL))
	if err != nil {
		return "", fmt.Errorf("parse Responses base URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("Responses base URL scheme must be http or https")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("Responses base URL host is empty")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("Responses base URL must not contain user information")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("Responses base URL must not contain a query or fragment")
	}
	if parsed.Scheme == "http" {
		ip := net.ParseIP(parsed.Hostname())
		if ip == nil || !ip.IsLoopback() {
			return "", fmt.Errorf("plain HTTP Responses base URL must use a loopback IP")
		}
	}
	return parsed.JoinPath("responses").String(), nil
}

func readAPIError(response *http.Response, token string) error {
	data, err := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes+1))
	if err != nil {
		return fmt.Errorf("Responses API returned HTTP %d", response.StatusCode)
	}
	if len(data) > maxErrorBodyBytes {
		return fmt.Errorf("Responses API returned HTTP %d with an oversized error body", response.StatusCode)
	}
	var body struct {
		Error struct {
			Code    any    `json:"code"`
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &body) == nil {
		code := normalizeErrorText(fmt.Sprint(body.Error.Code), token)
		message := normalizeErrorText(body.Error.Message, token)
		if code != "" && code != "<nil>" && message != "" {
			return fmt.Errorf("Responses API returned HTTP %d (%s): %s", response.StatusCode, code, message)
		}
		if message != "" {
			return fmt.Errorf("Responses API returned HTTP %d: %s", response.StatusCode, message)
		}
	}
	return fmt.Errorf("Responses API returned HTTP %d", response.StatusCode)
}

func normalizeErrorText(value string, secret string) string {
	if secret != "" {
		value = strings.ReplaceAll(value, secret, "[redacted]")
	}
	value = strings.Join(strings.Fields(value), " ")
	const maximumLength = 512
	if len(value) > maximumLength {
		value = value[:maximumLength]
	}
	return value
}
