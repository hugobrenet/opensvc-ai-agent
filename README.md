# opensvc-ai-agent

Standalone Go daemon that orchestrates LLM providers and OpenSVC MCP servers
for authenticated AI-assisted cluster diagnostics.

The project is intentionally independent from the om3 daemon. Its future API
will let command-line clients submit prompts while the agent coordinates LLM
providers and OpenSVC MCP tools.

## Current status

The initial implementation provides only a health endpoint using the Go
standard library. MCP, LLM providers, agent orchestration, sessions, and om3
integration are not implemented yet.

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
