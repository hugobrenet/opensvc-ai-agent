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
6. Runtime availability and cost hardening. Pending.
   - Add one configurable end-to-end deadline for each ask, including bounded
     SSE writes and MCP session cleanup. Complete.
   - Bound MCP response bodies before the SDK decodes them. Complete. Tool
     execution and OpenSVC daemon request timeouts belong to the MCP server; the
     agent keeps only its end-to-end ask deadline.
   - Add process-wide ask admission control returning an HTTP error before SSE,
     plus per-ask total tool-call and model-usage budgets.
   - Bound the MCP tool count, complete definitions, and individual and
     aggregate model-visible schemas. Complete.
   - Bound protocol-adapter tool-call accumulation.
7. Deterministic tool and data-governance policy. Pending.
   - Default to read-only tools and explicitly allow approved non-destructive
     diagnostic probes such as `refresh_instance_status`.
   - Fail closed for unannotated or unauthorized tools, and document that MCP
     results required for reasoning are sent to the configured LLM provider.
8. Structured operational audit logging. Pending.
   - Generate a server-side request ID and record bounded structured events for
     authentication rejection, ask lifecycle, tool lifecycle, usage, and stable
     failure codes.
   - Never log JWTs, authorization headers, prompts, model text, tool arguments
     or results, provider credentials, or raw upstream errors.
9. Graceful shutdown and HTTP hardening. Pending.
   - Drain in-flight asks with a bounded shutdown deadline.
   - Remove the inbound Authorization header after verification, set an
     explicit maximum header size, and document JWT verification-key rotation.
10. One-shot `om ai ask` client integration. Pending.

Implement step 6 before starting later pending steps. The OpenSVC JWT belongs
only to the MCP path. It must never enter an LLM request, LLM context, prompt,
tool argument, or provider configuration.

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
    auth_middleware.go
    server.go
    server_test.go
  auth/
    context.go
    context_test.go
    jwt.go
    jwt_test.go
  config/
    agent.go
    config.go
    config_test.go
    jwt.go
    mcp.go
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

The composition root loads and validates HTTP, JWT, LLM, MCP, and agent
configuration; constructs one shared JWT verifier, LLM client, MCP client, and
Agent; then injects them into the API handler. `POST /v1/ask` never constructs
provider clients. Its middleware authenticates the OpenSVC access JWT before
reading the prompt or starting SSE. `Agent.Ask` then opens and closes one
request-scoped MCP session using the same JWT.

`internal/agent` opens one request-scoped MCP session, exposes every discovered
MCP tool to the model, and executes requested tools sequentially. Tool arguments
are limited to 256 KiB, results to 1 MiB, and each LLM turn to four tool calls.
Functional MCP tool errors return to the model; MCP transport errors stop the
run. The MCP HTTP transport rejects response bodies larger than 4 MiB before the
SDK decodes them. MCP catalogs are limited to 128 tools, 512 KiB per complete
definition, and 4 MiB total. Model-visible names, descriptions, input schemas,
and the aggregate catalog have tighter bounds. The versioned system prompt
belongs to the agent package, not provider configuration.

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
- Authenticate `/v1/ask` with the OpenSVC cluster CA before reading its body or
  starting SSE. Accept only RS256 tokens with valid expiration, non-empty `sub`
  and `iss`, and `token_use=access`.
- Continue delegating the verified raw JWT to MCP. Agent authentication never
  replaces MCP and daemon verification or authorization.
- Attach OpenSVC JWTs to MCP HTTP requests through request context. Never retain
  them in a long-lived MCP client.
- Mask the delegated JWT, verified identity, and grants from the context passed
  to an LLM client while preserving cancellation, deadlines, and unrelated
  context values.
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
  terminal SSE error because the HTTP status can no longer change. Use the
  stable `request_timeout` code for deadline expiration and require each SSE
  write to complete within 15 seconds.

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
