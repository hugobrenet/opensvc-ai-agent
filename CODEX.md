# CODEX

Project guidance for AI coding agents working on opensvc-ai-agent.

## Mission

Build a small, secure Go daemon that orchestrates LLM providers and OpenSVC MCP
servers for authenticated cluster diagnostics.

The AI agent is independent from the om3 daemon and from MCP servers. MCP
servers remain the deterministic OpenSVC integration layer.

## Current scope

The current implementation exposes an HTTP health endpoint, an authenticated
MCP client, provider-neutral LLM contracts, a Responses protocol adapter, and
an agent loop coordinating LLM turns with MCP tool calls. Add other protocol
adapters, HTTP ask endpoints, persistent sessions, or om3 integration code only
as an explicit project step.

## Build order

Implement the first incomplete step only unless the user explicitly expands the
active project step:

1. Authenticated MCP Streamable HTTP client with request-scoped JWT delegation. Complete.
2. Provider-neutral LLM client types. Complete.
3. Responses protocol adapter. Complete.
4. Agent loop coordinating LLM tool calls with MCP tool execution. Complete.
5. `POST /v1/ask`, carrying the caller JWT from the HTTP request to MCP. Next.

The ask API is the next step. The OpenSVC JWT belongs only to the MCP path. It
must never enter an LLM request, LLM context, prompt, tool argument, or provider
configuration.

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

`internal/agent` opens one request-scoped MCP session, exposes every discovered
MCP tool to the model, and executes requested tools sequentially. Tool arguments
are limited to 256 KiB, results to 1 MiB, and each LLM turn to four tool calls.
Functional MCP tool errors return to the model; MCP transport errors stop the
run. The versioned system prompt belongs to the agent package, not provider
configuration.

`internal/llm` must not import an HTTP protocol adapter or contain provider
configuration. Protocol adapters implement `llm.Client`, map their wire events
to neutral LLM events, and return transport or provider failures as Go errors.

`internal/llm/responses` implements only the Responses wire protocol using
`net/http`. It must set `store` to false, bound requests and streams, disable
redirects, and ignore unknown semantic events. `internal/llmfactory` selects
clients by protocol name, never by provider or model name.

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
- Stream future long-running responses with an explicit, documented event
  contract. Do not expose model chain-of-thought.

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

Responses integration tests use the same build tag and generic
`OPENSVC_AI_LLM_*` environment variables. Never commit gateway URLs, model
identifiers, or API tokens.

## Change discipline

- Keep each commit limited to the active project step.
- Preserve unrelated user changes.
- Do not commit or push unless explicitly requested.
- Do not store credentials, `.env` files, binaries, or generated secrets in the
  repository.
- Keep README.md and this file aligned with the implementation.
