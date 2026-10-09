# Interactive `om ai` client

The om3 command-line client provides the local user interface for
`opensvc-ai-agent`. It supports one-shot prompts, persistent interactive
conversations, and conversation metadata management.

The client connects to the agent over HTTPS using its configured remote address.

## Architecture

Token issuance stays at the OpenSVC daemon. The agent and MCP run in the
operator's infrastructure, on the same host:

```text
om ai ── access token + cluster ID ──> OpenSVC daemon
  │
  └── HTTPS request ──> AI agent ──> LLM provider
                          │
                          └── Unix socket ──> OpenSVC MCP ──> cluster VIP daemon
```

The client obtains a short-lived access token and the cluster ID from the same
daemon, and sends them as `Authorization: Bearer` and `X-OpenSVC-Cluster-ID`.
Both are required. Before each protected API operation, the agent verifies the
token through MCP `GET /mcp/auth/whoami`, which MCP serves only on its local
socket: MCP selects the cluster from the header and relays the token to that
daemon's `GET /api/auth/whoami`. The same token and cluster ID are delegated
for MCP tools. Persistent conversations are bound to the authenticated cluster
ID, issuer and subject. The client never stores the token, messages, or
conversation state.

The agent treats the token as opaque. MCP owns catalogue routing; the daemon
authenticates the token and enforces grants. The agent uses the returned
identity and expiry for conversation ownership and request deadlines. The
cluster header only selects where the token is verified: a token issued by
another cluster is refused by the selected daemon.

Configure the agent's TCP listener and certificate/key files with
`OPENSVC_AI_LISTEN_ADDR`, `OPENSVC_AI_TLS_CERT_FILE`, and
`OPENSVC_AI_TLS_KEY_FILE`. Configure its MCP connection with the absolute
socket path `OPENSVC_AI_MCP_SOCKET`. These are agent settings, not CLI
settings. The CLI requires `OPENSVC_AI_AGENT_URL` and accepts an optional
`OPENSVC_AI_AGENT_CA_FILE` for its HTTPS endpoint and TLS trust.

## Prerequisites

Before using the client:

1. Start the OpenSVC daemon on the node.
2. Start the OpenSVC MCP server on the agent host, with its local socket.
3. Configure and start `opensvc-ai-agent`.
4. Verify the agent health endpoint:

   ```bash
   curl --cacert /etc/opensvc-ai/agent-ca.pem https://127.0.0.1:8090/health
   ```

5. Verify the available commands:

   ```bash
   om ai
   ```

The user running `om` must be able to request an access token from the local
OpenSVC daemon. Development installations with a root-only daemon socket may
require running the examples with `sudo`.

## One-shot prompt

Use `ask` for a single non-persistent prompt:

```bash
om ai ask "Assess the health of my cluster"
```

The response is streamed to standard output. Tool progress is written to
standard error:

```text
[tool] get_cluster_health
The cluster is healthy.
```

`--timeout` limits the complete request. Its default is 10 minutes and its
accepted range is 1 second to 30 minutes:

```bash
om ai ask --timeout 2m "Summarize the current cluster health"
```

An `ask` request is never added to a persistent conversation.

## Interactive conversation

Start a new persistent conversation with:

```bash
om ai chat
```

The client prints the server-generated conversation ID before accepting the
first prompt:

```text
Conversation: 1d8f521a6df5ab128d264d88244c229c
Enter 'exit' or 'quit' to end the session.
> Assess the health of my cluster.
[tool] get_cluster_health
The cluster currently has one unavailable service.
> Which service is unavailable?
The unavailable service is lab/svc/redis.
> exit
```

Prompts are read one line at a time. A successful turn is stored by the agent
and becomes context for later turns. The client requests a fresh short-lived
access token and the cluster ID for conversation creation or resume and for
every prompt.

After the first successful turn, the agent derives a title from that prompt.
Whitespace is normalized and titles longer than 80 characters are truncated.
This is deterministic and does not trigger an additional LLM request. Failed,
canceled, or timed-out first turns do not set a title.

The interactive controls are:

| Input | Behavior |
| --- | --- |
| `Ctrl+C` | Cancel only the active turn and return to the prompt. |
| `Ctrl+D` | End the session cleanly. |
| `exit` or `quit` | End the session cleanly. |
| `SIGTERM` | Terminate the complete client session. |

`--timeout` applies independently to every turn, not to the total lifetime of
the interactive session:

```bash
om ai chat --timeout 5m
```

Canceled, failed, timed-out, and interrupted turns are not added to future
model context. The conversation remains on the agent and can be resumed unless
it was deleted or expired.

## Resume a conversation

Use the interactive selector to resume an existing conversation:

```bash
om ai chat --resume
```

Conversations are shown with their title, last update age, and a shortened ID:

```text
Select a conversation:

  1. Redis availability  updated 2 minutes ago  1d8f521a
  2. Cluster health review  updated 3 days ago  a93c740e

Conversation [1]:
```

Enter a number, or press Enter to select the first conversation. The first
entry is the most recently updated one. `exit`, `quit`, or `Ctrl+D` leaves the
selector without opening a conversation. `Ctrl+C` returns to the selection
prompt.

The shortened ID is displayed only to help distinguish duplicate titles. The
agent still opens the conversation through its complete immutable ID.

For scripts, or when the ID is already known, pass it directly to `chat`:

```bash
om ai chat 1d8f521a6df5ab128d264d88244c229c
```

Conversation content is loaded by the agent. The client does not maintain a
history file or local conversation cache. A conversation ID and `--resume`
cannot be used together.

## List conversations

List the active conversations owned by the authenticated OpenSVC identity:

```bash
om ai list
```

The default table contains the title, immutable ID, creation time, last update
time, expiry time, and stored byte count. Conversations are ordered by their
most recent update. A conversation without a successful first turn is shown as
`Untitled conversation`.

Use JSON when the result is consumed by another command:

```bash
om ai list --output json
```

## Show conversation metadata

Show one owned conversation:

```bash
om ai show 1d8f521a6df5ab128d264d88244c229c
om ai show 1d8f521a6df5ab128d264d88244c229c --output json
```

The command and `GET /v1/conversations/{id}` return metadata only. The separate
[messages endpoint](webapp.md#conversation-messages) exposes stored display text
to authorized clients; `om ai` does not call it or display previous messages.
Tool arguments and raw tool results remain private.

## Rename a conversation

Assign a more useful title to an owned conversation:

```bash
om ai rename 1d8f521a6df5ab128d264d88244c229c "Redis incident review"
```

The command returns the updated metadata. Titles are normalized, limited to 80
characters, and do not have to be unique. A manually assigned title is not
replaced by later prompts. Renaming changes metadata only; the immutable ID and
stored conversation history remain unchanged.

## Delete a conversation

Delete one owned conversation:

```bash
om ai delete 1d8f521a6df5ab128d264d88244c229c
```

A successful deletion produces no output. The conversation cannot be resumed
after deletion.

## Identity and security

Conversation access is isolated by the authenticated OpenSVC cluster ID,
issuer and subject. Resuming a chat is authenticated again through whoami. Listing returns only conversations owned by that identity. Reading or
deleting another identity's conversation does not reveal whether it exists.

The CLI never accepts a provider token. Provider credentials remain in the
agent process configuration and are never returned through the API. The
OpenSVC JWT is request-scoped, is not persisted in SQLite, and is not printed by
the client.

## Troubleshooting

### Agent connection refused

Verify the configured TCP endpoint and TLS trust:

```bash
curl --cacert /etc/opensvc-ai/agent-ca.pem https://127.0.0.1:8090/health
```

Connection refused means the listener is not reachable. For a TLS error, check
the CA bundle and the certificate hostname/IP. Do not disable certificate
verification.

### Local daemon permission denied

The client must contact the local OpenSVC daemon to issue an access token. Use
an account with access to its local socket. A root-only development deployment
may require `sudo om ai ...`.

### Conversation not found

Use `om ai chat --resume` to select an active conversation, or check the full ID
with `om ai list`. A missing, deleted, expired, or foreign-owned conversation
cannot be resumed. Foreign ownership is deliberately reported as not found.

### Conversation busy

Only one turn can run in a conversation at a time. Wait for the active turn to
finish or cancel it before retrying. A turn waiting for the confirmation of a
destructive action also keeps the conversation busy until the action is
confirmed, rejected or expires; see
[action confirmation](webapp.md#action-confirmation), which `om ai` does not
handle yet.

### Turn timeout

Increase the per-turn limit when a diagnosis requires slow tools or several
LLM iterations:

```bash
om ai chat CONVERSATION_ID --timeout 15m
```

The timeout must remain between 1 second and 30 minutes.
