# ai-agent

Standalone AI agent for OpenSVC cluster diagnostics, usable with `om ai`.

## Requirements

- Go 1.25.5 or later to build.
- A configured OpenSVC MCP server accessible over HTTPS.
- An LLM endpoint and model.
- A TLS certificate covering the agent's hostname, with its private key.

## Install

From the repository root, on a Linux host:

```bash
go build -o bin/opensvc-ai-agentd ./cmd/opensvc-ai-agentd
sudo useradd --system --user-group --no-create-home --shell /usr/sbin/nologin opensvc-ai
sudo install -Dm755 bin/opensvc-ai-agentd /usr/local/libexec/opensvc-ai-agentd
sudo install -d -m750 -o root -g opensvc-ai /etc/opensvc-ai
sudo install -d -m700 -o opensvc-ai -g opensvc-ai /var/lib/opensvc-ai-agent
```

Place the TLS certificate at `/etc/opensvc-ai/agent.crt` and the private key
at `/etc/opensvc-ai/agent.key`. Both must be readable by `opensvc-ai`; restrict
the private key to that user.

## Configure

Create `/etc/opensvc-ai/agent-llm.env`, owned by `root:opensvc-ai` with mode `0640`:

```dotenv
OPENSVC_AI_LISTEN_ADDR=0.0.0.0:8090
OPENSVC_AI_TLS_CERT_FILE=/etc/opensvc-ai/agent.crt
OPENSVC_AI_TLS_KEY_FILE=/etc/opensvc-ai/agent.key
OPENSVC_AI_CONVERSATION_DB_PATH=/var/lib/opensvc-ai-agent/conversations.db
OPENSVC_AI_MCP_SOCKET=/run/opensvc-mcp/delegated.sock

OPENSVC_AI_LLM_PROTOCOL=responses
OPENSVC_AI_LLM_BASE_URL=https://llm.example.test/v1
OPENSVC_AI_LLM_MODEL=your-model
OPENSVC_AI_LLM_AUTH_MODE=bearer
OPENSVC_AI_LLM_API_TOKEN=replace-me
```

Replace the example values. The conversation database is created on first
start. The agent refuses a database written by another schema version: remove
the file to start with an empty one. Use `chat_completions` instead of
`responses` when required by the provider. For a provider without authentication, set
`OPENSVC_AI_LLM_AUTH_MODE=none` and omit the API token.

For the Anthropic Messages API, use:

```dotenv
OPENSVC_AI_LLM_PROTOCOL=messages
OPENSVC_AI_LLM_BASE_URL=https://api.anthropic.com/v1
OPENSVC_AI_LLM_MODEL=your-anthropic-model
OPENSVC_AI_LLM_AUTH_MODE=api_key
OPENSVC_AI_LLM_API_TOKEN=replace-me
```

`api_key` sends `x-api-key`; `bearer` remains available for Messages endpoints.
Messages supports streamed text and MCP tool calls, without extended thinking
or provider-hosted tools. Keep the API key only in the protected environment
file, never in Git or conversation history.

The agent reaches MCP only through its local Unix socket: run both on the same
host, typically as two resources of one OpenSVC service, and give the
`opensvc-ai` user access to the socket through its group. The socket need not
exist when the agent starts. Restrict network access to the agent port.

For browser clients, set `OPENSVC_AI_CORS_ALLOWED_ORIGINS` to comma-separated
webapp origins, or `*` to allow all origins. Empty by default. See the
[browser client guide](docs/webapp.md#cors) for examples and restrictions.

## Start

```bash
sudo -u opensvc-ai sh -c '
  set -a
  . /etc/opensvc-ai/agent-llm.env
  set +a
  exec /usr/local/libexec/opensvc-ai-agentd
'
```

For managed deployments, use an OpenSVC `app.simple` resource to launch the
binary with the same environment. The agent reads environment variables, not
the environment file itself; its launcher must load that file if used.

Check health using the hostname covered by the certificate:

```bash
curl https://agent.example.test:8090/health
```

Add `--cacert /path/to/ca.pem` if the agent uses a private CA.

## Use with `om ai`

On an OpenSVC node:

```bash
export OPENSVC_AI_AGENT_URL=https://agent.example.test:8090
om ai ask "Assess the health of my cluster"
om ai chat
```

For a private agent CA, also set `OPENSVC_AI_AGENT_CA_FILE`. `om ai` sends a
daemon-issued token and the cluster ID of the daemon that issued it.
See the [client guide](docs/om-ai.md) for more commands.

For OpenID clients, see the [HTTP header contract](docs/webapp.md).

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) and
[NOTICE](NOTICE).
