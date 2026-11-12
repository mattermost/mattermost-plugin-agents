# Monitor Agents

Monitor Agents with a token usage log, Prometheus metrics, OpenTelemetry traces, and audit records. None of them record prompts, messages, or tool results.

## Token usage log

Use it for cost tracking, chargeback, and capacity planning. It needs a Professional license.

1. Go to **System Console > Plugins > Agents > Debug**.
2. Set **Enable Token Usage Logging** to **True**.

Mattermost writes one JSON object per LLM request to `logs/agents/token_usage.log`, relative to the Mattermost Server's working directory (for example `/opt/mattermost/logs/agents/`). A custom log file location doesn't move it. The file rotates at 100 MB and rotated files are compressed. Providers that don't report cached tokens, reasoning tokens, or cost leave those fields at `0`.

| Field | Meaning |
|---|---|
| `event`, `schema_version` | `llm_token_usage` and `1`. New optional fields don't change the version. |
| `user_id`, `team_id`, `channel_id`, `channel_type` | Who and where. |
| `agent_name`, `agent_username`, `bot_username`, `agent_user_id` | The agent. `bot_username` repeats `agent_username` for older queries. |
| `acting_user_id`, `tool_auth_mode` | The identity that built the tool catalog, and `user` or `service_account`. For a service account agent, the acting identity is the agent's bot, so you can tie the request to the person in `user_id`. The log doesn't record which external action ran. |
| `model`, `service_type`, `service_id`, `service_name` | What served the request. |
| `operation`, `operation_subtype` | The feature, such as `conversation`, `thread_analysis`, `channel_summary`, `meeting_summary`, `search`, `title_generation`, `bridge_agent`, `bridge_service`, or `ambient_classification`. |
| `input_tokens`, `output_tokens`, `total_tokens` | Token counts. |
| `cached_read_tokens`, `cached_write_tokens`, `reasoning_tokens`, `cost` | When the provider reports them. |

Calls made straight to a service through the LLM Bridge have no agent. Their records leave `agent_name`, `agent_username`, `bot_username`, and `agent_user_id` empty. The Prometheus counters label them `bot_name="unknown"`. An empty `service_name` means the service itself has a blank name. The value `unknown` means the service identity was missing.

To open the log in a spreadsheet, convert it with `jq`:

```bash
# JSON array
jq -s '.' logs/agents/token_usage.log > token_usage.json

# CSV
echo "timestamp,user_id,acting_user_id,tool_auth_mode,team_id,agent_username,operation,model,input_tokens,output_tokens,total_tokens,cost" > token_usage.csv
jq -r '[.timestamp, .user_id, .acting_user_id, .tool_auth_mode, .team_id, .agent_username, .operation, .model, .input_tokens, .output_tokens, .total_tokens, (.cost // 0)] | @csv' logs/agents/token_usage.log >> token_usage.csv
```

Two environment variables on the Mattermost Server process choose where records go, when logging is on. `MM_FEATUREFLAGS_AI_TOKEN_USAGE_LOG_TO_FILE` defaults to true. `MM_FEATUREFLAGS_AI_TOKEN_USAGE_LOG_TO_PLUGIN` defaults to false and also writes records to the plugin log.

## Metrics

Agents exposes Prometheus metrics under `/plugins/mattermost-ai/metrics` on the server's metrics endpoint. That endpoint is controlled by the server's **Listen address for performance** setting and defaults to port `8067`.

| Metric | Labels | Meaning |
|---|---|---|
| `agents_system_plugin_start_timestamp_seconds` | | When the plugin started. |
| `agents_system_plugin_info` | version, installation ID | Plugin version. |
| `agents_api_time_seconds` | handler, method, status code | API handler duration histogram. |
| `agents_http_requests_total`, `agents_http_errors_total` | | HTTP API requests and errors. |
| `agents_llm_input_tokens_total`, `agents_llm_output_tokens_total` | `bot_name`, `team_id` | Tokens used by LLM requests. Recorded only at Professional and above, and independent of **Enable Token Usage Logging**. |
| `agents_mcp_dynamic_tool_events_total` | `bot_name`, `event`, `result` | Dynamic tool loading search and load events. |

## OpenTelemetry tracing

Traces show request latency, LLM call performance, tool execution, and failures.

### Turn it on

In **System Console > Plugins > Agents > Debug**, set **Trace Output**:

| Value | Config value | Behavior |
|---|---|---|
| **Off** | `off` | No tracing and no overhead. Default. |
| **Server Logs** | `logs` | Finished spans are written to the Mattermost Server log as `agents trace span` entries. No collector needed. Span events are a JSON array under `events` and span links under `links`, so Loki's `\| json` can parse both. Spans log at ERROR level only when they record a failure. |
| **OTLP Endpoint** | `otlp` | Spans are exported over OTLP gRPC to **OpenTelemetry Endpoint**, for example `localhost:4317`. Use it with Grafana Tempo, Jaeger, or another collector. |

For OTLP, the exporter connects over plaintext gRPC by default. That suits a collector on the same host or a private network. For one that needs TLS or credentials, such as hosted Grafana Tempo:

- Turn on **Use TLS for OTLP Export** (`openTelemetryTLS`). The endpoint's certificate is checked against the system root certificates.
- Set **OTLP Export Headers** (`openTelemetryHeaders`) to comma-separated `key=value` pairs, as in `OTEL_EXPORTER_OTLP_HEADERS`, for example `Authorization=Basic <base64 of instance-id:token>`. Percent-encode commas inside values as `%2C`. The value is a credential. It's stored with the plugin configuration like an API key and never written to logs, spans, or audit records. Use it with TLS.

```json
{
  "telemetryOutput": "otlp",
  "openTelemetryEndpoint": "tempo-prod-04-prod-us-east-0.grafana.net:443",
  "openTelemetryTLS": true,
  "openTelemetryHeaders": "Authorization=Basic <credentials>"
}
```

The service name is `mattermost-ai-agents`. In production, send traces to your OpenTelemetry Collector or directly to an OTLP-capable backend. The repository's `dev/docker-compose.otel.yml` starts Grafana Tempo and Grafana for local testing.

### What's traced

- HTTP requests to the plugin, named by method and route, such as `POST /bridge/v1/completion/agent/:agent`. Requests that match no route aren't traced, and neither is the long-lived `GET /mcp-server/mcp` stream. The `POST` requests that carry MCP tool calls are.
- LLM calls: provider, model, operation, token counts, finish reason, time to first token, and per-source input token breakdown (system, history, tool definitions, tool results, images).
- Tool execution and MCP calls, web search calls, semantic search, and streaming a reply to a post.
- Access decisions. Decisions that allowed access are dropped to keep volume down. Denials (`agents.abac.denied=true`) and failed evaluations are kept.

### Follow one turn

One user message produces one `agent run` trace.

1. Find the `message has been posted` span with `agents.post.id` set to the user's post ID. Its `agents.agent.run.trace_id` names the run's trace.
2. The `agent run` span in that trace carries the post, channel, user, conversation, and user turn IDs. It links back to the message hook span and to the previous turn's trace.
3. Everything the turn did is in that trace: `process dm request`, each `llm chat completion` round, `resolve tool`, `stream to post`, and title generation. `agent run` lasts until the reply finishes streaming.

The agent's own reply posts don't start a trace.

### Span attributes

Cached token, reasoning token, and cost attributes appear only when the provider reports non-zero values.

| Attribute | Meaning |
|---|---|
| `agents.llm.provider`, `agents.llm.model`, `agents.llm.operation` | Provider, model, and operation such as `conversation` or `title_generation`. |
| `agents.llm.input_tokens`, `agents.llm.output_tokens` | Token counts. Also `cached_read_tokens`, `cached_write_tokens`, `reasoning_tokens`, and `cost`. |
| `agents.llm.tokens.system`, `.history`, `.tool_defs`, `.tool_results`, `.images` | Where the input tokens went. |
| `agents.llm.round` | 0-based index of the LLM call in a turn's tool loop. |
| `agents.llm.finish_reason`, `agents.llm.time_to_first_token_ms` | Why a round ended, and its latency. |
| `agents.tool.name`, `agents.tool.id`, `agents.tool.status` | The tool and its outcome, `success` or `error`. |
| `agents.tool.execution` | `auto` or `approved`. |
| `agents.tool.result.bytes`, `agents.tool.result.tokens_estimate` | Size of the tool result. The result itself is never recorded. |
| `agents.tool.returned_names`, `agents.tool.loaded_name` | Tools returned by `search_tools`, and loaded by `load_tool`. |
| `agents.mcp.server`, `agents.mcp.tool` | The MCP server and tool. |
| `agents.stream.outcome` | `completed`, `canceled`, `errored`, or `empty`. |
| `agents.conversation.id`, `agents.conversation.user_turn.id`, `agents.user.id`, `agents.channel.id`, `agents.post.id`, `agents.thread.root_post.id` | Correlation IDs. |
| `agents.abac.denied`, `agents.abac.resource_id`, `agents.abac.resource_name`, `agents.abac.decision_source` | Access decisions. `decision_source=legacy` means an agent's allow or block list decided it. |
| `agents.bridge.plugin_id`, `agents.service.id` | The calling plugin and the requested service on LLM Bridge spans. |

## Audit logging

Agents sends a server audit record for every state-changing operation except pinning and unpinning skills:

- Configuration saves.
- Reindex, cancel, catch up, and rebuild actions, and the MCP tool cache clear.
- Agent create, update, delete, avatar, version restore, and version deletion.
- Skill (custom prompt) create, update, and delete.
- Channel auto-reply changes.
- Conversation renames and the wait controls (check now, cancel).
- MCP OAuth start, callback, and disconnect, and per-user tool preferences.
- Access policy changes for agents, services, and MCP servers.
- Inter-plugin MCP server registration and unregistration, and admin changes to plugin-registered MCP servers (`updateMCPPluginServer`).
- In-channel tool call and tool result approvals.
- MCP session grants, when an external client gets a session as a user (`mcpSessionGrant`).

Each record has the actor (user ID, session, IP), the event, the request path, the outcome (including failures and permission denials as HTTP status codes), the `trace_id` for jumping to traces, and the IDs of the affected objects. Records never contain prompts, conversation or channel content, tool arguments or results, configuration values, or free-form error text. A configuration save records only which top-level sections changed. Failure detail stays in the server log, so correlate by time, actor, or `trace_id`.

The plugin has no audit toggle. Whether records are stored is controlled by the server's [audit logging configuration](/administration-guide/manage/logging).

## Server logs

Filter the Mattermost Server log by the plugin ID `mattermost-ai`. Set the server's log level to DEBUG to include debug messages while you troubleshoot, and turn it back afterward to reduce volume. For per-request detail, use trace output in **Server Logs** mode.
