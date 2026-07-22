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
and repeats until a final answer. An authenticated one-shot ask API exposes the
agent event stream over SSE. Persistent sessions and om3 integration are not
implemented yet.

The API emits structured JSON operational audit records to stdout. Every HTTP
request receives a server-generated `X-Request-ID` for correlation.

The JWT is never stored by the MCP client. It must be attached to the operation
context and is forwarded only to MCP HTTP requests. The agent masks it from the
context passed to the LLM client.

The neutral LLM package has no provider, HTTP, credential, or model
configuration. The factory selects an adapter by wire protocol, never by
provider name.

## API configuration

| Variable | Description |
| --- | --- |
| `OPENSVC_AI_LISTEN_ADDRESS` | Loopback listen address, default `127.0.0.1:8090`. |
| `OPENSVC_AI_MAX_CONCURRENT_ASKS` | Process-wide concurrent ask limit, default `4`, maximum `128`. |

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
| `OPENSVC_AI_AGENT_TIMEOUT` | End-to-end timeout for one ask, default `5m`, accepted range `1s` to `30m`. |

## MCP configuration

| Variable | Description |
| --- | --- |
| `OPENSVC_AI_MCP_ENDPOINT` | Streamable HTTP MCP endpoint used for request-scoped sessions. |

## OpenSVC authentication

| Variable | Description |
| --- | --- |
| `OPENSVC_AI_JWT_VERIFY_KEY_FILE` | Cluster CA certificate or RSA public key used to verify OpenSVC access JWTs; defaults to `/var/lib/opensvc/certs/ca_certificates`. |

The API accepts only RS256 JWTs signed by the configured OpenSVC cluster CA. It
requires valid registered time claims, non-empty `sub` and `iss`, and
`token_use=access`. Authentication happens before the ask request body is read
or its SSE response starts. The same request-scoped JWT is then independently
verified by MCP and delegated to the OpenSVC daemon for grant enforcement.

For each request, the agent opens an MCP session, lists all available tools,
and sends their schemas to the LLM. Tool calls run sequentially, with at most
four calls in one LLM turn and sixteen calls in one ask. Functional tool errors
are returned to the model so it can explain or recover; MCP transport errors
stop the request. Tool arguments are limited to 256 KiB and encoded MCP results
to 1 MiB. Every MCP HTTP response is limited to 4 MiB before the MCP SDK decodes
it. The agent rejects catalogs larger than 128 tools, individual encoded
definitions larger than 512 KiB, or an aggregate encoded catalog larger than 4
MiB. Model-visible tool names, descriptions, and input schemas are additionally
limited to 128 bytes, 4 KiB, and 256 KiB respectively, with a 1 MiB aggregate
limit.

No endpoint, model, or token has a project default. Plain HTTP endpoints must
use a loopback IP. The token value is checked at configuration time, read again
when sending a request, and never retained in the non-secret configuration
structure.

## Requirements

- Go 1.25.5 or later

## Run

Configure the generic LLM variables, `OPENSVC_AI_MCP_ENDPOINT`, and then run:

```bash
go run ./cmd/opensvc-ai-agentd
```

The daemon validates the LLM, agent, and MCP configuration at startup. Provider
tokens remain in their environment variable and are not retained in process
configuration.

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

## One-shot ask

`POST /v1/ask` accepts one prompt and streams agent events as SSE. After local
verification, the OpenSVC JWT remains request-scoped and is delegated only to
MCP:

```bash
curl -N http://127.0.0.1:8090/v1/ask \
  -H "Authorization: Bearer $OPENSVC_JWT" \
  -H "Content-Type: application/json" \
  -d '{"prompt":"Assess the health of my cluster."}'
```

The stream can contain `text_delta`, `tool_started`, `tool_finished`, `usage`,
`completed`, and `error` events. Tool arguments, tool results, provider errors,
and credentials are not exposed by this API. Authentication and request
validation failures use JSON HTTP errors before streaming starts. Runtime
failures use a generic terminal `error` SSE event because the HTTP 200 response
has already started. The stable error code is `agent_failed`, or
`request_timeout` when an operation deadline expires. Each SSE write must
complete within 15 seconds so a client that stops reading cannot retain an ask
indefinitely. When four asks are already running by default, a new request is
rejected before SSE with HTTP `429`, error code `too_many_requests`, and a
`Retry-After` header.

## Operational audit

The daemon writes one-line JSON audit events for authentication and ask
rejections, ask start and completion, tool start and completion, LLM token
usage, timeouts, cancellations, and stable failures. Records contain the
server-generated request ID and may contain the verified subject and issuer,
tool name, iteration, duration, finish reason, and token counters.

Terminal audit failure codes are `agent_failed`, `agent_cleanup_failed`,
`agent_incomplete`, `request_timeout`, `request_canceled`, and
`stream_write_failed`. A functional MCP tool error uses `tool_error` and does
not make the ask itself a transport failure.

Audit records never contain JWTs, authorization headers, prompts, model text,
tool arguments or results, provider credentials, grants, or raw errors from the
LLM, MCP server, or OpenSVC daemon.

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
go test -tags=integration ./internal/api
```
