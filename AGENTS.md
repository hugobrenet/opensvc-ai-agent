# ai-agent: context for coding agents

## Purpose and scope

This project is a standalone Go daemon that orchestrates LLM turns and
authenticated OpenSVC MCP tool calls for cluster diagnostics. It exposes an
HTTPS API used by clients such as `om ai`, with one-shot requests and persistent
conversations. It does not issue OpenSVC tokens or implement daemon operations.

Keep the agent independent from the om3 binary, a local daemon, the MCP
implementation and any particular LLM provider. OpenSVC semantics and factual
data collection belong to MCP; orchestration and diagnostic reasoning belong
to the agent and model.

The canonical Go module is `github.com/opensvc/ai-agent`, including when
working in a fork. The binary remains `opensvc-ai-agentd`. Use the Go standard
library, the MCP Go SDK, and SQLite through `database/sql` and
`modernc.org/sqlite`. The project is licensed under Apache-2.0; preserve
attribution notices.

## Before editing

- Inspect the working tree and preserve unrelated user changes.
- Read the relevant implementation, [README](README.md) and
  [client contract](docs/om-ai.md) before changing behavior.
- Identify the responsible layer and preserve its boundary. Prefer focused
  changes to existing capabilities over new frameworks or abstractions.
- Check actual API, MCP and provider contracts; do not invent compatibility,
  permissions or persistence guarantees.
- Changes to the agent API or SSE contract may affect the separate om3 client.
  Coordinate such changes explicitly; do not modify other projects implicitly.

## Architecture

- `cmd/opensvc-ai-agentd`: composition root, HTTPS listener and lifecycle.
  Construct shared clients, the orchestrator and conversation service here,
  not inside request handlers.
- `internal/config`: environment parsing and startup validation for the
  process, LLM, MCP, agent limits and conversation storage.
- `internal/api`: versioned HTTP routes, authentication middleware, SSE,
  concurrency admission, stable public errors and structured audit events.
  Keep handlers thin.
- `internal/auth`: verified identity, HTTP target bounds and private
  request-scoped credentials; remove authentication data from LLM contexts.
- `internal/mcpclient`: local Unix socket transport, remote whoami verification,
  request-scoped MCP sessions and bounded tool discovery/results.
- `internal/agent`: provider-neutral turn loop, history validation, system
  prompt and sequential execution of model-requested MCP tools. A tool call
  that MCP annotates destructive (`destructiveHint: true`, or not read-only
  without the hint) never runs on the model's request alone: the turn is
  suspended until the user confirms or rejects it through the API, and the
  decision applies to the exact stored call.
- `internal/llm`: neutral model contracts and events. Protocol adapters under
  `responses`, `chatcompletions` and `messages` implement these contracts;
  the neutral package must not import its adapters.
- `internal/llmfactory`: client construction selected by protocol, not by
  provider brand or model name.
- `internal/conversation`: ownership, turn lifecycle, retention and storage
  interface. `internal/conversation/sqlite` owns the current embedded schema,
  transactions and provider-neutral message encoding.

`Agent.Ask` is a non-persistent wrapper around `Agent.RunTurn` with empty
history. Each turn opens and closes its own authenticated MCP session.
Conversations must not retain MCP sessions or provider-specific state.

## Authentication and trust

- Treat the bearer token as opaque. Bound its size and reject missing,
  duplicate or malformed Authorization headers and query-string tokens.
  Do not decode JWTs or duplicate OpenSVC token rules: MCP owns profile
  selection, claims, algorithms, dates, target coherence and catalogue routing;
  the daemon verifies signatures and permissions.
- Require one bounded `X-OpenSVC-Cluster-ID` header and read an optional
  `X-OpenSVC-Node` header, without interpreting them against token claims.
  Forward them unchanged; the cluster ID selects where MCP verifies the token.
- Reach MCP only through its local Unix socket (`OPENSVC_AI_MCP_SOCKET`). MCP
  accepts delegated OpenSVC tokens there, never on its OAuth HTTPS listener.
  Do not add a network fallback.
- Authenticate through MCP's `GET /mcp/auth/whoami` bridge on that socket,
  which delegates signature verification to daemon `GET /api/auth/whoami`.
  Require a bounded JSON response with complete identity fields and a future
  expiry. This returned identity is authoritative; the requested cluster must
  match the response. Apply the returned expiry to the operation deadline.
- Complete authentication before reading prompts, accessing conversations or
  starting an SSE response. Invalid tokens return 401; unavailable verification
  returns 503. No offline fallback, identity cache or local verification keys.
- Delegate the unchanged JWT and explicit cluster/node headers to MCP in private
  request context; hide all from LLM contexts. Never retain
  it in a shared client or global state. Daemon grants remain authoritative.
- Keep OpenSVC and provider credentials separate. Never place OpenSVC JWTs,
  identities or grants in LLM contexts, prompts, tool arguments or provider
  requests; preserve cancellation and deadlines when removing authentication.
- Use HTTPS with TLS 1.2+, validate MCP chain and hostname, and bind delegated
  credentials to the configured origin. No MCP redirects or environment proxies.
  An explicit MCP CA bundle replaces system roots.
- Browser CORS is opt-in through `OPENSVC_AI_CORS_ALLOWED_ORIGINS`: exact
  origins or a standalone `*`. Handle preflights before JWT authentication;
  real operations still authenticate. Never enable automatic browser credentials
  or derive CORS trust from JWT claims or client targets. Preserve streaming
  interfaces and put CORS headers on allowed-origin errors as well as successes.
- Never expose credentials in logs, errors, API responses or stored history.
  Audit records contain identifiers, tool names, counters, durations and stable
  codes, not prompts, model text, tool arguments/results or raw upstream errors.

## Turn and conversation invariants

- Preserve bounds on history, iterations, tool calls, arguments, results,
  catalogs, streams and concurrent asks. Keep discovered tools request-scoped;
  do not introduce a caller-independent catalog that bypasses visibility.
- Functional MCP tool errors can return to the model; MCP transport failures
  stop the turn. Do not invent daemon data or hide authorization failures.
- Protocol adapters translate wire events into neutral events, bound I/O,
  disable redirects and set provider storage to false where the protocol
  supports it. Keep provider-specific parsing and completion semantics out of
  the orchestration loop.
- Bind all conversation operations to verified `cluster_id`, `issuer` and
  `subject`. Foreign and missing conversation IDs share the same public error.
- Serialize turns per conversation without holding a database transaction
  during LLM or MCP work. Commit complete messages before terminal completion;
  failed, canceled and interrupted turns must not replay partial output.
- Persist only the bounded provider-neutral history needed for later turns.
  Never persist JWTs, grants, provider credentials, system prompts or audit data.
  Keep the local SQLite directory and file owner-only; preserve atomic writes,
  retention, expiry and interrupted-turn recovery.
- Preserve the SSE event contract. Reject invalid requests before streaming;
  after streaming begins, emit stable generic errors, not raw provider failures,
  tool payloads or model chain-of-thought.
- Completion delivery can fail after persistence. Do not silently retry a turn
  or promise idempotency without an explicit API and storage design.
- On shutdown, stop admission, drain within the configured deadline, propagate
  cancellation to LLM/MCP, then close storage.

## Coding and change discipline

- Favor explicit Go and the standard library. Use `context.Context` for I/O,
  wrap errors with `fmt.Errorf` and `%w`, and format changes with `gofmt`.
- Keep types beside their owner and wire shapes private to protocol adapters.
  Avoid unnecessary interfaces, reflection, goroutines and frameworks.
- Add dependencies, protocols or operational scope only for an explicit need.
  Do not add migration or compatibility machinery without a requirement.
- Keep binaries, generated files, environment secrets and credentials out of
  Git. Keep documentation aligned with behavior and installation.
- Keep edits scoped to the request. Commit or push only when requested.
