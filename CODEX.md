# CODEX

Project guidance for AI coding agents working on opensvc-ai-agent.

## Mission

Build a small, secure Go daemon that orchestrates LLM providers and OpenSVC MCP
servers for authenticated cluster diagnostics.

The AI agent is independent from the om3 daemon and from MCP servers. MCP
servers remain the deterministic OpenSVC integration layer.

## Current scope

The current implementation exposes an HTTP health endpoint, an authenticated
MCP client, provider-neutral LLM contracts, Responses and Chat Completions
protocol adapters, an agent loop coordinating LLM turns with MCP tool calls,
and an authenticated one-shot SSE ask API. Add other protocol adapters,
persistent sessions, or om3 integration code only as an explicit project step.

## Build order

Implement the first incomplete step only unless the user explicitly expands the
active project step:

1. Authenticated MCP Streamable HTTP client with request-scoped JWT delegation. Complete.
2. Provider-neutral LLM client types. Complete.
3. Responses protocol adapter. Complete.
4. Agent loop coordinating LLM tool calls with MCP tool execution. Complete.
5. `POST /v1/ask`, carrying the caller JWT from the HTTP request to MCP. Complete.

The next project step is not selected. The OpenSVC JWT belongs only to the MCP
path. It must never enter an LLM request, LLM context, prompt, tool argument, or
provider configuration.

## Technology

- Go 1.25.5 or later
- Go standard library HTTP server
- Go testing and httptest

Keep dependencies minimal. Do not add a web framework while the API remains
small enough for `net/http`.

## Repository layout

```text
cmd/
  opensvc-ai-agentd/
    main.go
internal/
  agent/
    agent.go
    convert.go
    event.go
    prompt.go
  api/
    ask.go
    ask_test.go
    server.go
    server_test.go
  auth/
    context.go
    context_test.go
  config/
    agent.go
    config.go
    config_test.go
  llm/
    client.go
    types.go
    validate.go
    llm_test.go
    responses/
      client.go
      request.go
      stream.go
      client_test.go
    chatcompletions/
      client.go
      request.go
      stream.go
      client_test.go
  llmfactory/
    factory.go
    factory_test.go
  mcpclient/
    client.go
    client_test.go
```

`cmd/opensvc-ai-agentd/main.go` is the composition root. HTTP contracts belong
in `internal/api`; process configuration belongs in `internal/config`;
request-scoped credentials belong in `internal/auth`; MCP transport belongs in
`internal/mcpclient`; provider-neutral model and tool-call contracts belong in
`internal/llm`; LLM/MCP orchestration belongs in `internal/agent`.

The composition root loads and validates HTTP, LLM, MCP, and agent
configuration; constructs one shared LLM client, MCP client, and Agent; then
injects the Agent into the API handler. `POST /v1/ask` never constructs provider
clients. Each call attaches the opaque caller JWT to its request context and
`Agent.Ask` opens and closes one request-scoped MCP session.

`internal/agent` opens one request-scoped MCP session, exposes every discovered
MCP tool to the model, and executes requested tools sequentially. Tool arguments
are limited to 256 KiB, results to 1 MiB, and each LLM turn to four tool calls.
Functional MCP tool errors return to the model; MCP transport errors stop the
run. The versioned system prompt belongs to the agent package, not provider
configuration.

The current catalog is small enough to send every tool definition on each LLM
turn. If the MCP catalog grows, introduce request-scoped tool routing so only a
bounded relevant subset reaches the model. Preserve the authenticated MCP tool
visibility and invalidation semantics; do not replace it with an authorization-
blind global catalog. Protocol-specific conversation or prompt caching belongs
in protocol adapters and must remain an optional optimization.

`internal/llm` must not import an HTTP protocol adapter or contain provider
configuration. Protocol adapters implement `llm.Client`, map their wire events
to neutral LLM events, and return transport or provider failures as Go errors.

`internal/llm/responses` and `internal/llm/chatcompletions` implement their wire
protocols using `net/http`. They must set `store` to false, bound requests and
streams, disable redirects, and ignore unknown semantic events. The Chat
Completions adapter waits for `[DONE]` before emitting neutral completion so a
trailing usage chunk is preserved. `internal/llmfactory` selects clients by
protocol name, never by provider or model name.

## Security invariants

- Bind only to a loopback address until server-side TLS or a Unix socket is
  implemented.
- Never place JWTs, provider API keys, passwords, or private keys in prompts,
  request bodies, logs, errors, or test fixtures.
- Future OpenSVC JWTs must remain request-scoped and must never be stored in a
  global variable or persistent session.
- Treat OpenSVC JWTs as opaque credentials. The MCP server verifies signatures
  and claims; the agent only delegates the token.
- Attach OpenSVC JWTs to MCP HTTP requests through request context. Never retain
  them in a long-lived MCP client.
- Mask the delegated JWT from the context passed to an LLM client while
  preserving cancellation, deadlines, and unrelated context values.
- Future provider credentials must remain separate from OpenSVC credentials.

## API rules

- Use versioned paths for agent operations.
- Health and readiness endpoints may remain unversioned.
- Use typed JSON request and response structures.
- Bound request bodies, response bodies, timeouts, and agent iterations.
- Stream long-running responses with an explicit, documented event contract.
  Do not expose model chain-of-thought.
- `POST /v1/ask` is one-shot and streams `text_delta`, `tool_started`,
  `tool_finished`, `usage`, `completed`, or generic `error` SSE events. Do not
  expose tool arguments, tool results, provider errors, or credentials.
- Reject invalid authentication, media types, JSON, and prompt bounds before
  starting SSE. Once streaming starts, report runtime failures as a generic
  terminal SSE error because the HTTP status can no longer change.

## Go style

- Prefer explicit code and the standard library.
- Use `context.Context` for request and long-running operations.
- Wrap errors with `fmt.Errorf` and `%w`.
- Keep handlers thin and business logic outside the API layer.
- Avoid unnecessary interfaces, reflection, goroutines, and frameworks.
- Run gofmt on every changed Go file.

## Validation

Run before every commit:

```bash
go fmt ./...
go test ./...
go vet ./...
go build -o /tmp/opensvc-ai-agentd ./cmd/opensvc-ai-agentd
git diff --check
```

Use `httptest` for API behavior. Normal tests must not require a live LLM,
OpenSVC daemon, MCP server, network connection, or secret.

The `integration` build tag may be used for explicit tests against a running
MCP server. Such tests must read their endpoint and JWT from the environment,
skip when either is absent, and never print the JWT.

LLM adapter integration tests use the same build tag and generic
`OPENSVC_AI_LLM_*` environment variables. Never commit gateway URLs, model
identifiers, or API tokens.

## Change discipline

- Keep each commit limited to the active project step.
- Preserve unrelated user changes.
- Do not commit or push unless explicitly requested.
- Do not store credentials, `.env` files, binaries, or generated secrets in the
  repository.
- Keep README.md and this file aligned with the implementation.
