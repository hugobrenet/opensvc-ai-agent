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
an authenticated one-shot SSE ask API, a provider-neutral conversation turn
engine, and a local SQLite conversation store not yet wired to the API. Add
other protocol adapters or om3 integration code only as an explicit project
step.

## Build order

Implement the first incomplete step only unless the user explicitly expands the
active project step:

1. Provider-neutral conversation turn engine. Complete.
   - Refactor the current one-shot loop behind an internal turn method accepting
     bounded provider-neutral history and returning the complete messages
     produced by the turn.
   - Keep `Agent.Ask` and `POST /v1/ask` as non-persistent one-shot wrappers.
   - Open and close a request-scoped MCP session for every turn; conversations
     must never retain MCP sessions, JWTs, or provider-specific state.
2. Local SQLite conversation store. Complete.
   - Introduce a storage interface independent from SQLite and the API layer.
   - Persist conversations, turns, and provider-neutral messages, including the
     bounded tool calls and results required to reconstruct LLM context.
   - Use embedded migrations, transactions, WAL mode, owner-only file
     permissions, retention, size limits, and interrupted-turn recovery.
   - Never persist JWTs, authorization headers, provider credentials, grants,
     system prompts, or raw audit data.
3. Conversation service.
   - Bind every conversation to the authenticated OpenSVC issuer and subject.
   - Serialize turns per conversation without holding a database transaction
     during LLM or MCP work.
   - Commit only completed messages to future model context; record failed,
     canceled, and interrupted turns without replaying partial output.
   - Enforce bounded history, turn count, stored bytes, expiry, and deletion.
4. Authenticated conversation API.
   - Add `POST /v1/conversations`, `GET /v1/conversations`,
     `GET /v1/conversations/{id}`, and
     `DELETE /v1/conversations/{id}`.
   - Add streaming `POST /v1/conversations/{id}/turns` using the existing SSE
     event contract.
   - Return stable ownership-safe errors, including `conversation_busy` and
     `conversation_expired`, without disclosing another user's conversation.
   - Add bounded structured audit events containing identifiers and counters,
     never conversation content.
5. Local interactive `om ai` client.
   - Preserve `om ai ask` for one-shot use.
   - Add a local prompt loop that creates or resumes a conversation and obtains
     a fresh short-lived OpenSVC JWT for every turn.
   - Make Ctrl+C cancel only the active turn; support clean EOF and explicit
     exit, with no client-side conversation persistence.
6. Conversation hardening and end-to-end validation.
   - Test ownership isolation, concurrent turns, expiry, deletion, migration,
     crash recovery, database failures, bounded context, SSE disconnects, and
     graceful shutdown with active conversations.
   - Run a real multi-turn workflow through LLM, MCP, and the OpenSVC daemon and
     verify restart and resume behavior.
7. Local service deployment and Unix sockets.
   - Version independent systemd units for the agent and MCP without making the
     OpenSVC daemon depend on either service.
   - Run under dedicated unprivileged users, protect state and credentials, and
     apply systemd filesystem, privilege, and resource hardening.
   - Replace agent and MCP loopback listeners with permissioned Unix sockets
     while preserving their HTTP contracts.
8. Remote OpenSVC client integration. Deferred until local interactive use is
   complete.
   - Design `ox ai` and an optional authenticated OpenSVC daemon proxy without
     exposing the agent or MCP listener to the network.

The next incomplete step is step 3. The OpenSVC JWT belongs only to the
authenticated agent, MCP, and daemon path. It must never enter an LLM request,
LLM context, persisted conversation, prompt, tool argument, provider
configuration, or audit record.

## Technology

- Go 1.25.5 or later
- Go standard library HTTP server
- Go testing and httptest
- SQLite through `database/sql` and the pure-Go `modernc.org/sqlite` driver

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
    history.go
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
  conversation/
    model.go
    store.go
    sqlite/
      codec.go
      migrations.go
      operations.go
      store.go
      store_test.go
      migrations/
        001_initial.sql
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
`internal/llm`; LLM/MCP orchestration belongs in `internal/agent`;
conversation domain types and the storage contract belong in
`internal/conversation`; the SQLite adapter belongs in
`internal/conversation/sqlite`.

The composition root loads and validates HTTP, JWT, LLM, MCP, and agent
configuration; constructs one shared JWT verifier, LLM client, MCP client, and
Agent; then injects them into the API handler. `POST /v1/ask` never constructs
provider clients. Its middleware authenticates the OpenSVC access JWT before
reading the prompt or starting SSE, removes the inbound Authorization header,
and retains the JWT only in private request context. `Agent.Ask` wraps
`Agent.RunTurn` with an empty history. `RunTurn` deep-copies and validates up to
256 provider-neutral history messages and 2 MiB before opening and closing one
request-scoped MCP session using the same JWT. It injects the current system
prompt outside persisted history and returns only the complete new user,
assistant, tool-call, and tool-result messages.

The SQLite conversation store is local to one node and is not constructed by
the composition root until the conversation service exists. It uses embedded
migrations, WAL with full synchronous writes, foreign keys, secure deletion,
owner-filtered operations, atomic turn completion, and strict provider-neutral
message encoding. It permits one writer connection, stores no partial model
output, marks abandoned running turns interrupted on explicit recovery, and
enforces logical conversation and database limits. The database directory and
file must deny group and other access. JWTs, provider credentials, system
prompts, grants, authorization headers, and audit records never enter it.

`internal/agent` opens one request-scoped MCP session, exposes every discovered
MCP tool to the model, and executes requested tools sequentially. Tool arguments
are limited to 256 KiB, results to 1 MiB, each LLM turn to four tool calls, and
each ask to sixteen tool calls. Functional MCP tool errors return to the model;
MCP transport errors stop the run. The MCP HTTP transport rejects response
bodies larger than 4 MiB before the SDK decodes them. MCP catalogs are limited
to 128 tools, 512 KiB per complete definition, and 4 MiB total. Model-visible
names, descriptions, input schemas, and the aggregate catalog have tighter
bounds. The versioned system prompt belongs to the agent package, not provider
configuration.

`internal/api` assigns a cryptographically random request ID and emits bounded
JSON audit records to stdout for authentication rejection, ask rejection and
lifecycle, tool lifecycle, LLM usage, and stable failure codes. Audit records
may contain the verified subject and issuer, tool names, counters, durations,
and finish reasons. They must never contain JWTs, authorization headers,
prompts, model text, tool arguments or results, provider credentials, grants,
or raw upstream errors.

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
- Reject asks above the configured process-wide concurrency limit before SSE
  with HTTP 429, the stable `too_many_requests` code, and `Retry-After`.
- On SIGINT or SIGTERM, stop accepting requests, drain active asks for the
  configured shutdown timeout, then close remaining connections so request
  cancellation propagates to LLM and MCP calls.

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
