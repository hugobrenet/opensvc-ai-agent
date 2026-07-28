# opensvc-ai-agent

Standalone Go daemon that orchestrates LLM providers and OpenSVC MCP servers
for authenticated AI-assisted cluster diagnostics.

The project is intentionally independent from the om3 daemon. Its local API
lets command-line clients submit prompts while the agent coordinates LLM
providers and OpenSVC MCP tools.

## Current status

The implementation provides a health endpoint and a request-scoped MCP
Streamable HTTP client. The client can list and call MCP tools while delegating
the caller's OpenSVC Bearer JWT. Provider-neutral LLM contracts describe text
messages, tools, tool calls, tool results, streaming events, and token usage. A
Responses and Chat Completions protocol adapters implement streamed text and
function calls using the Go standard library. The agent loop discovers MCP
tools, lets the LLM select them, executes calls, returns results to the LLM,
and repeats until a final answer. Authenticated one-shot and persistent
conversation APIs expose the agent event stream over SSE. Conversations are
stored locally in SQLite and isolated by the verified OpenSVC issuer and
subject. The om3 client provides one-shot prompts, persistent interactive
sessions, and conversation metadata management through `om ai`.

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
| `OPENSVC_AI_SHUTDOWN_TIMEOUT` | Maximum graceful shutdown drain time, default `30s`, accepted range `1s` to `5m`. |

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

## Conversation configuration

| Variable | Description |
| --- | --- |
| `OPENSVC_AI_CONVERSATION_DB_PATH` | Local SQLite path, default `/var/lib/opensvc-ai-agent/conversations.db`. |
| `OPENSVC_AI_CONVERSATION_LIFETIME` | Retention after the last successful turn, default `168h`, accepted range `1h` to `8760h`. |

The database directory and file must not grant access to group or other users.
SQLite is an internal implementation detail and is never exposed through HTTP.
Only provider-neutral completed messages are stored; credentials, JWTs, system
prompts, grants, audit records, and partial model output are never persisted.

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
After verification, the inbound `Authorization` header is removed before the
request reaches the ask handler; the JWT remains only in private request
context.

The verification certificate or public key is loaded once at process startup
and is not reloaded automatically. Replace the configured public file
atomically and restart the agent when the OpenSVC signing key changes.
Coordinate the same rotation with the MCP server and JWT issuer so they validate
the same generation of access tokens throughout the transition.

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

Non-loopback addresses are rejected while server-side TLS is unavailable. The
HTTP server limits request headers to 64 KiB. On `SIGINT` or `SIGTERM`, it stops
accepting new requests and lets active asks finish for at most
`OPENSVC_AI_SHUTDOWN_TIMEOUT`. Once that deadline expires, remaining
connections are closed so cancellation propagates to active LLM and MCP
operations.

## Health

```bash
curl http://127.0.0.1:8090/health
```

Expected response:

```json
{"status":"ok"}
```

## Quick start with `om ai`

The local om3 client is the recommended user interface for the agent:

```bash
om ai ask "Assess the health of my cluster"
om ai chat
om ai chat --resume
om ai list
```

Use `ask` for a one-shot request and `chat` for a persistent interactive
conversation. `chat --resume` displays existing conversations by title and
opens the selected one. The client obtains short-lived access tokens from the
local OpenSVC daemon and never stores conversation state itself.

See the [interactive om client guide](docs/om-ai.md) for command details,
examples, controls, output formats, and troubleshooting.

## HTTP API

The local API supports both one-shot requests and persistent conversations:

| Method and path | Purpose |
| --- | --- |
| `GET /health` | Report daemon health. |
| `POST /v1/ask` | Submit a non-persistent prompt and stream the response as SSE. |
| `POST /v1/conversations` | Create an owned conversation. |
| `GET /v1/conversations` | List owned conversations. |
| `GET /v1/conversations/{id}` | Read conversation metadata. |
| `PATCH /v1/conversations/{id}` | Change the conversation title. |
| `DELETE /v1/conversations/{id}` | Delete a conversation. |
| `POST /v1/conversations/{id}/turns` | Submit a persistent turn and stream the response as SSE. |

For example, submit a one-shot prompt with:

```bash
curl -N http://127.0.0.1:8090/v1/ask \
  -H "Authorization: Bearer $OPENSVC_JWT" \
  -H "Content-Type: application/json" \
  -d '{"prompt":"Assess the health of my cluster."}'
```

Except for `/health`, endpoints require a valid OpenSVC access JWT. Streaming
endpoints emit text, tool progress, usage, completion, and generic error events
without exposing credentials, tool payloads, or provider errors.

Conversations are stored in local SQLite and isolated by the verified token
issuer and subject. The first successful prompt provides the default title,
which can later be changed without altering the immutable conversation ID.
Successful turns extend expiry; failed or interrupted turns are not added to
future model context.

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

## License

See the LICENSE file.

## Project Status

This project is currently in development. Feedback, issues, and contributions are welcome.

For questions or discussion, you can contact me on LinkedIn:

https://fr.linkedin.com/in/hugo-brenet-49b200202
