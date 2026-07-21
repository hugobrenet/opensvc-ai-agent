# opensvc-ai-agent

Standalone Go daemon that orchestrates LLM providers and OpenSVC MCP servers
for authenticated AI-assisted cluster diagnostics.

The project is intentionally independent from the om3 daemon. Its future API
will let command-line clients submit prompts while the agent coordinates LLM
providers and OpenSVC MCP tools.

## Current status

The implementation provides a health endpoint and a request-scoped MCP
Streamable HTTP client. The client can list and call MCP tools while delegating
the caller's OpenSVC Bearer JWT. LLM protocols, agent orchestration, the ask API,
sessions, and om3 integration are not implemented yet.

The JWT is never stored by the MCP client. It must be attached to the operation
context and is forwarded only to MCP HTTP requests.

## Requirements

- Go 1.25.5 or later

## Run

```bash
go run ./cmd/opensvc-ai-agentd
```

The daemon listens on `127.0.0.1:8090` by default. Override the loopback
address with:

```bash
OPENSVC_AI_LISTEN_ADDRESS=127.0.0.1:8091 \
  go run ./cmd/opensvc-ai-agentd
```

Non-loopback addresses are rejected while server-side TLS is unavailable.

## Health

```bash
curl http://127.0.0.1:8090/health
```

Expected response:

```json
{"status":"ok"}
```

## Development

```bash
go fmt ./...
go test ./...
go vet ./...
go build -o /tmp/opensvc-ai-agentd ./cmd/opensvc-ai-agentd
git diff --check
```

An opt-in integration test can validate the authenticated client against a
running OpenSVC MCP server. Export `OPENSVC_AI_TEST_MCP_ENDPOINT` and
`OPENSVC_AI_TEST_MCP_JWT`, then run:

```bash
go test -tags=integration ./internal/mcpclient
```

The test initializes an MCP session and lists the available tools. It never
prints the JWT.
