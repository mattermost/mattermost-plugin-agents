# Load Testing Agents

Developer and operator guide for non-production load-test setups using the Agents plugin `loadtest_mock` service type and the `mattermost-ai` load-test-ng controller.

## Scope

This document applies to **load-test and staging environments only**. It describes:

- The in-process mock LLM (`loadtest_mock`) that implements `llm.LanguageModel` inside the plugin process.
- Tool execution policy (`auto_run_everywhere`) so MCP tool calls run without human approval during simulations.
- Enabling the Agents plugin (`mattermost-ai`) and its simulator actions from mattermost-load-test-ng.

It does **not** replace production LLM configuration or security review for real provider traffic.

## Configure the In-Process Mock LLM

The `loadtest_mock` service type selects a **validated, in-process** `loadtest.MockLLM` before any Bifrost client is created. It is **not** a Bifrost provider, plugin transport, or standalone HTTP mock service. No external mock LLM endpoint is required.

Assign the bot to a service with `"type": "loadtest_mock"` and optional raw JSON in `loadTestMockConfig`:

```json
{
  "type": "loadtest_mock",
  "loadTestMockConfig": {
    "seed": 42,
    "profile_weights": {
      "realistic_default": 0.8,
      "realistic_fast": 0.2,
      "realistic_slow": 0
    }
  }
}
```

- **Nil, empty, or whitespace-only `loadTestMockConfig`** merges onto `DefaultReadSearchHeavyProfile()` from `loadtest.ParseProfile()`.
- **Invalid JSON or unknown fields** fail parsing before the mock is constructed; check server logs for `failed to parse load-test mock profile`.
- At plugin startup, when the mock LLM is initialized, the server logs a single **run-audit snapshot**:

  - Message: `Initialized load-test mock LLM`
  - Field `profile_summary`: multiline text with latency ranges, weights, tool argument distributions, and `defaults_source=spikes/llm-latency-benchmark`

The summary is emitted **once per bot service initialization**, not per chat request or stream chunk.

## Configure Tool Auto-Run

Mattermost MCP tools respect channel/user policy in the System Console. For unattended load tests, tool calls must not block on approval prompts.

Configure MCP tool policies so relevant tools use **`auto_run_everywhere`** (see `webapp` System Console components such as `mcp_tool_config_row.tsx`). Without this, simulations may stall waiting for approval while the mock LLM still completes streams.

## Enable Agents in mattermost-load-test-ng

Integration is **cross-repository**:

- This repo exposes `github.com/mattermost/mattermost-plugin-agents/loadtest/controller` (a standalone nested Go module) for blank-import registration of the Agents SimulController.
- mattermost-load-test-ng must import that package and include **`mattermost-ai`** in `EnabledPlugins` for the simulated server configuration.

**Plugin ID:** `mattermost-ai` (not `agents`).

**Registered actions** (names matter for simulator wiring):

- `mattermost-ai.AskAgentChannelMention`
- `mattermost-ai.AskAgentDM`

**Controller configuration:**

- Default file path: `./config/mattermost-ai-loadtest.json`
- Override with environment variable: `MM_AGENTS_LOADTEST_CONFIG` pointing at a JSON file

**Trigger frequencies** (`triggerFrequencyChannelMention`, `triggerFrequencyDM`) are **relative weights** inside load-test-ng, not global percentages. For example, `0.001` is one-thousandth the weight of an action configured with frequency `1.0`. The simulator scales from the smallest non-zero frequency.

Example `mattermost-ai-loadtest.json`:

```json
{
  "triggerFrequencyChannelMention": 0.001,
  "triggerFrequencyDM": 0.001,
  "agentUsername": "ai",
  "agentUserID": "",
  "triggerMode": "both",
  "promptProfile": "mixed",
  "mockProfile": null
}
```

Optional `mockProfile` embeds the same JSON schema as `loadTestMockConfig` when your ng-side harness merges mock defaults with simulator config (validated via `loadtest.ParseProfile` on read).

During development, you may use temporary `replace` directives in a **local** `go.mod`; do **not** commit unstable replace pins aimed only at your laptop.

## Profile Defaults

Unless overridden, the empirical **read/search-heavy** defaults apply (derived from `spikes/llm-latency-benchmark/`):

| Latency mix           | TTFT (ms)     | chunk_count | chunk_interval_ms | total_wall_time_ms_per_request |
|-----------------------|---------------|-------------|-------------------|--------------------------------|
| `realistic_default`   | 3000-12000    | 150-400     | 30-80             | 15000-25000                    |
| `realistic_fast`      | 600-2500      | 40-120      | 40-100            | 5000-10000                     |
| `realistic_slow`      | 12000-22000   | 400-1000    | 15-40             | 28000-40000                    |

- Profile weights (default): `realistic_default` **0.70**, `realistic_fast` **0.20**, `realistic_slow` **0.10**
- `reasoning_skip_probability`: **0.10**
- Streaming is enabled by default (`streaming_enabled: true`)

See the active merged profile in logs under `profile_summary` after initialization.

## Local Validation

From the Agents plugin repository:

```bash
go test ./loadtest/... ./bots/... ./toolrunner/... -race
# The SimulController lives in a separate nested module, so test it on its own:
(cd loadtest/controller && go test ./... -race)
```

Run an integrated mattermost-load-test-ng scenario only after both this branch (or equivalent) and the ng-side wiring branch are available; remove or document any temporary `replace` directives first.

## Troubleshooting

| Symptom | What to check |
|--------|----------------|
| Plugin fails to start or bot LLM errors mentioning `loadtest profile` | Fix `loadTestMockConfig` JSON (unknown fields are rejected). Validate weights sum positively and reference existing latency profile keys. |
| No simulated agent traffic | Confirm `EnabledPlugins` includes **`mattermost-ai`**. Confirm ng imports `github.com/mattermost/mattermost-plugin-agents/loadtest/controller`. |
| Actions never hit the agent user | Verify `agentUsername` / `agentUserID` match a real bot user in the test data set; check simulator logs for target resolution errors. |
| Tool calls hang or never execute | Ensure MCP tools use **`auto_run_everywhere`** for load-test channels/users so approvals do not block automation. |
| Need to confirm mock parameters | Search logs for `Initialized load-test mock LLM` and read `profile_summary` (initialization-time only). |
