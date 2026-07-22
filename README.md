# opensvc-ai-agent

Standalone Go daemon that orchestrates LLM providers and OpenSVC MCP servers
for authenticated AI-assisted cluster diagnostics.

The project is intentionally independent from the om3 daemon. Its future API
will let command-line clients submit prompts while the agent coordinates LLM
providers and OpenSVC MCP tools.

## Current status

The implementation provides a health endpoint and a request-scoped MCP
Streamable HTTP client. The client can list and call MCP tools while delegating
the caller's OpenSVC Bearer JWT. Provider-neutral LLM contracts describe text
messages, tools, tool calls, tool results, streaming events, and token usage. A
Responses and Chat Completions protocol adapters implement streamed text and
function calls using the Go standard library. The agent loop discovers MCP
tools, lets the LLM select them, executes calls, returns results to the LLM,
and repeats until a final answer. The ask API, persistent sessions, and om3
integration are not implemented yet.

The JWT is never stored by the MCP client. It must be attached to the operation
context and is forwarded only to MCP HTTP requests. The agent masks it from the
context passed to the LLM client.

The neutral LLM package has no provider, HTTP, credential, or model
configuration. The factory selects an adapter by wire protocol, never by
provider name.

## LLM configuration

Both LLM adapters use generic process configuration. The factory selects the
wire contract from `OPENSVC_AI_LLM_PROTOCOL` explicitly:

| Variable | Description |
| --- | --- |
| `OPENSVC_AI_LLM_PROTOCOL` | Wire protocol: `responses` or `chat_completions`. |
| `OPENSVC_AI_LLM_BASE_URL` | API root; the selected adapter appends `/responses` or `/chat/completions`. |
| `OPENSVC_AI_LLM_MODEL` | Model identifier understood by the configured endpoint. |
| `OPENSVC_AI_LLM_AUTH_MODE` | `none` or `bearer`. |
| `OPENSVC_AI_LLM_API_TOKEN` | Bearer token when authentication is enabled. |
| `OPENSVC_AI_LLM_TIMEOUT` | Whole request timeout, default `2m`. |
| `OPENSVC_AI_LLM_MAX_OUTPUT_TOKENS` | Maximum generated tokens, default `4096`. |

## Agent configuration

| Variable | Description |
| --- | --- |
| `OPENSVC_AI_AGENT_MAX_ITERATIONS` | Maximum LLM turns per request, default `8`, maximum `32`. |

For each request, the agent opens an MCP session, lists all available tools,
and sends their schemas to the LLM. Tool calls run sequentially, with at most
four calls in one LLM turn. Functional tool errors are returned to the model so
it can explain or recover; MCP transport errors stop the request. Tool arguments
are limited to 256 KiB and encoded MCP results to 1 MiB.

No endpoint, model, or token has a project default. Plain HTTP endpoints must
use a loopback IP. The token value is checked at configuration time, read again
when sending a request, and never retained in the non-secret configuration
structure.

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

Each LLM adapter has opt-in live text and synthetic tool-call tests. After
exporting the generic LLM variables, run the directory matching the configured
protocol:

```bash
go test -tags=integration ./internal/llm/responses
go test -tags=integration ./internal/llm/chatcompletions
```

The complete agent loop can be tested against both services by exporting the
MCP variables above together with the generic LLM variables, then running:

```bash
go test -tags=integration ./internal/agent
```
