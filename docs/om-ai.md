# Interactive `om ai` client

The om3 command-line client provides the local user interface for
`opensvc-ai-agent`. It supports one-shot prompts, persistent interactive
conversations, and conversation metadata management.

## Architecture

`om ai` communicates only with services on the local node:

```text
om ai ── access token ──> OpenSVC daemon
  │
  └── authenticated request ──> AI agent ──> LLM provider
                                      │
                                      └──> OpenSVC MCP ──> OpenSVC daemon
```

The client obtains a short-lived OpenSVC access token from the local daemon.
The agent verifies the token, delegates it to MCP for tool calls, and binds
persistent conversations to its issuer and subject. The client never stores
the token, messages, or conversation state.

The agent listens on `http://127.0.0.1:8090` by default. For a non-default local
development deployment, set a loopback URL:

```bash
export OPENSVC_AI_AGENT_URL=http://127.0.0.1:8091
```

The override must use an HTTP or HTTPS loopback IP. There is intentionally no
public `--agent-url` flag, and remote agent URLs are rejected.

## Prerequisites

Before using the client:

1. Start the OpenSVC daemon on the node.
2. Start the OpenSVC MCP server used by the agent.
3. Configure and start `opensvc-ai-agent`.
4. Verify the agent health endpoint:

   ```bash
   curl http://127.0.0.1:8090/health
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
access token for conversation creation or resume and for every prompt.

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

Copy the ID printed when the conversation is created, or obtain it with
`om ai list`, then pass it to `chat`:

```bash
om ai chat 1d8f521a6df5ab128d264d88244c229c
```

Conversation content is loaded by the agent. The client does not maintain a
history file or local conversation cache.

## List conversations

List the active conversations owned by the authenticated OpenSVC identity:

```bash
om ai list
```

The default table contains the ID, creation time, last update time, expiry time,
and stored byte count. Conversations are ordered by their most recent update.

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

The command returns metadata only. The API intentionally does not expose stored
prompts, model responses, tool arguments, or tool results.

## Delete a conversation

Delete one owned conversation:

```bash
om ai delete 1d8f521a6df5ab128d264d88244c229c
```

A successful deletion produces no output. The conversation cannot be resumed
after deletion.

## Identity and security

Conversation access is isolated by the verified OpenSVC token issuer and
subject. Listing returns only conversations owned by that identity. Reading or
deleting another identity's conversation does not reveal whether it exists.

The CLI never accepts a provider token. Provider credentials remain in the
agent process configuration and are never returned through the API. The
OpenSVC JWT is request-scoped, is not persisted in SQLite, and is not printed by
the client.

## Troubleshooting

### Agent connection refused

Verify the local health endpoint and the configured loopback URL:

```bash
curl http://127.0.0.1:8090/health
printf '%s\n' "$OPENSVC_AI_AGENT_URL"
```

### Local daemon permission denied

The client must contact the local OpenSVC daemon to issue an access token. Use
an account with access to its local socket. A root-only development deployment
may require `sudo om ai ...`.

### Conversation not found

Check the ID with `om ai list`. A missing, deleted, expired, or foreign-owned
conversation cannot be resumed. Foreign ownership is deliberately reported as
not found.

### Conversation busy

Only one turn can run in a conversation at a time. Wait for the active turn to
finish or cancel it before retrying.

### Turn timeout

Increase the per-turn limit when a diagnosis requires slow tools or several
LLM iterations:

```bash
om ai chat CONVERSATION_ID --timeout 15m
```

The timeout must remain between 1 second and 30 minutes.
