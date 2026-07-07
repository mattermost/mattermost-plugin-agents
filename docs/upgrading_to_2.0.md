# Upgrading to Mattermost Agents V2

This guide describes how to upgrade the Mattermost Agents plugin (formerly Mattermost AI) from a v1.x release to v2.0.0. It covers the supported version path, the migrations that run automatically on first start of v2.0.0, the breaking changes and default-behavior flips that admins should know about before the upgrade window, and the verification steps to confirm the upgrade succeeded.

If you are running a fresh installation of v2.0.0 (no prior data, no prior `config.bots` entries), you do not need this guide — install the plugin and configure it using the [Admin Guide](admin_guide.md).

## 1. Who this guide is for

You are the right reader if all of the following apply:

- You currently run the Mattermost AI / Mattermost Agents plugin at v1.x.
- You administer the Mattermost server (System Admin) and own its database.
- You are planning to install v2.0.0.

If you are upgrading the Mattermost server itself, refer to the standard Mattermost upgrade documentation in addition to this guide. The plugin upgrade does not change Mattermost server requirements beyond those listed in the [Admin Guide](admin_guide.md#prerequisites).

## 2. Pre-flight checklist

Complete every item before starting the upgrade. Do not skip the database backup.

1. **Confirm your current plugin version.** Go to **System Console > Plugin Management** and note the installed version of Mattermost AI / Mattermost Agents.
2. **Confirm your Mattermost server version.** v2.0.0 requires Mattermost Server v10.0 or later. The optional external MCP HTTP server requires Mattermost Server v11.2 or later.
3. **Back up the Mattermost database.** v2.0.0 runs schema migrations on first start (see [Section 4](#4-what-gets-migrated-automatically)). Some migrations are not safely reversible (see [Section 8](#8-rollback-considerations)). In particular, migration 000007 drops the legacy `LLM_PostMeta` table without migrating its contents into the new conversation-entities tables — v1.x conversation titles cannot be recovered after the upgrade except from this backup. See [Section 5](#5-breaking-changes) for the full list of v1.x data that does not survive the upgrade.
4. **Back up `config.json`.** The legacy bot migration removes entries from stored plugin configuration after copying them into the database. Keeping a pre-upgrade copy of `config.json` is the simplest way to recover original bot definitions if you need to roll back.
5. **Verify your license is current.** Multi-agent configurations, fine-grained access controls, MCP support, and embedding search require an Entry, Enterprise, or Enterprise Advanced license. See [License requirements](admin_guide.md#license-requirements).
6. **Capture a list of currently configured bots.** From your existing System Console > Plugins > Agents (or AI Bots) page, note the username, display name, service binding, and access rules of each bot. After the upgrade, you can compare this list against the migrated agents on the new **Agents** product page.
7. **Identify any external integrations that read the `LLM_PostMeta` table directly.** That table is dropped by migration 000007. To our knowledge, no documented integration depends on this table.
8. **Schedule a maintenance window.** The plugin must be stopped and restarted, and the migrations must complete before users resume traffic. Typical windows in non-HA environments complete in minutes; HA clusters should plan additional time for migration coordination (see [Section 9](#9-ha-specific-notes)).

## 3. Version sequence

The supported upgrade path is to upgrade Mattermost to the latest v11.6 patch release before upgrading to Mattermost v11.7, which includes Agents v2.0.0.

> **Why this matters:** Mattermost v11.6 is the tested source release for the Agents v2.0.0 upgrade. Upgrade to Mattermost v11.6 first if you are running an earlier Mattermost or plugin version.

## 4. What gets migrated automatically

When v2.0.0 starts for the first time on an existing install, the plugin runs three database migrations and one in-process data migration. They are coordinated for HA via cluster mutex and a system-table flag, and are designed to be re-run safely.

### 4.1 Schema migrations (`store/migrations/`)

| Migration | Source PR | What it does |
|---|---|---|
| `000005_create_user_agents_table` | #589 | Creates the `Agents_UserAgents` table that stores agent identity, instructions, channel/user/team access lists, agent admins, MCP tool grants, and lifecycle timestamps. Creates supporting indexes; index creation is idempotent (PR #689). |
| `000006_user_agent_bot_fields` | #589 | Adds bot-specific columns to `Agents_UserAgents`: `Model`, `EnableVision`, `DisableTools`, `EnabledNativeTools`, `ReasoningEnabled`, `ReasoningEffort`, `ThinkingBudget`, `StructuredOutputEnabled`. Backfills existing rows with `EnableVision=false`, `DisableTools=false`, `ReasoningEnabled=true`. |
| `000007_create_conversations_table` | #602 | Creates the `LLM_Conversations` and `LLM_Turns` tables that back the new conversation-entities runtime. **Drops the legacy `LLM_PostMeta` table.** Note: conversation titles stored in `LLM_PostMeta` from v1.x are not automatically migrated; v2.0.0 builds its conversation history as users interact with agents after the upgrade. |

### 4.2 Legacy bot migration (`server/legacy_bot_migration.go`)

After the schema migrations run, the plugin performs a one-time migration of legacy `config.bots` entries into `Agents_UserAgents`:

1. The plugin acquires the cluster mutex `ai_legacy_bots_migration` so only one node performs the migration in HA deployments.
2. The plugin checks the `legacy_config_bots_migrated` flag in the `Agents_System` table. If set, the migration is a no-op.
3. For each entry in `config.bots`, the plugin verifies that a corresponding Mattermost bot account already exists. If any required Mattermost bot row is missing, the migration is **deferred** without setting the completion flag (it will retry on the next config update or restart).
4. Each `config.bots` entry is copied into `Agents_UserAgents` with:
    - A new agent ID and timestamps assigned by the store.
    - `BotUserID` linked to the existing Mattermost bot account.
    - `CreatorID` left empty (migrated agents do not have a creator).
    - `AdminUserIDs` cleared. Migrated agents are managed by system admins.
    - `AutoEnableNewMCPTools = true` so migrated agents preserve the v1.x behavior of having access to every MCP tool, including ones added later.
    - `MCPDynamicToolLoading = true` when the legacy bot did not explicitly store a value, so MCP tool schemas are loaded dynamically by default. Disable **Dynamic tool loading** on the agent's **MCPs** tab to expose the full MCP tool list up front.
5. The plugin clears `config.bots` from stored configuration to prevent duplicate bot registration on subsequent restarts.
6. The plugin sets `legacy_config_bots_migrated = true`.

### 4.3 What changes in the System Console

- **AI Bots** → redirects to the new top-level **Agents** product page. The legacy AI Bots editor is replaced by the **Agents** management UI (list page plus a three-tab agent editor: Configuration, Access, MCPs).
- **Enable MCP Client** and **Enable Embedded Server** toggles are removed (PR #617). MCP and the embedded Mattermost MCP server are always on.
- The **Use Responses API** toggle is removed for the **OpenAI** service type. OpenAI direct always uses the Responses API. The toggle remains for **OpenAI Compatible** and **Azure OpenAI** services.

## 5. Breaking changes

The following changes can affect deployments that depend on prior behavior. Review each item before the upgrade.

### 5.0 v1.x data that does not survive the upgrade

The upgrade removes some v1.x data without migrating it forward. Confirm your database backup is good before starting the procedure in [Section 7](#7-step-by-step-upgrade-procedure).

- **v1.x conversation titles (`LLM_PostMeta` table contents).** Migration 000007 drops the `LLM_PostMeta` table without copying its rows into the new `LLM_Conversations` / `LLM_Turns` schema. Conversation titles that v1.x stored against root post IDs are not visible in v2.0.0 and cannot be reconstructed from `LLM_Conversations` after the upgrade — restore from the pre-upgrade database backup if you need that data.

### 5.1 `config.bots` is no longer the source of truth for agent definitions

After the legacy bot migration runs, `config.bots` is cleared from stored plugin configuration. Editing `config.json` to add or modify agents will not have an effect — agent CRUD is now performed against `Agents_UserAgents` via the **Agents** page. Any tooling that drives agent definitions by writing to `config.bots` must be updated to call the agent API or use the **Agents** page.

### 5.2 The `LLM_PostMeta` table is dropped

Migration 000007 drops `LLM_PostMeta` after creating the new `LLM_Conversations` and `LLM_Turns` tables. The drop runs without copying row contents forward, so v1.x conversation titles stored in `LLM_PostMeta` are not preserved (see [Section 5.0](#50-v1x-data-that-does-not-survive-the-upgrade)). Any external integration that reads from `LLM_PostMeta` will break after the upgrade. To our knowledge, no public integration depends on this table.

### 5.3 MCP cannot be globally disabled

The **Enable MCP Client** and **Enable Embedded Server** System Console toggles are removed. To restrict MCP usage, disable individual tools or change tool approval policies under **System Console > Plugins > Agents > Model Context Protocol (MCP) > Tools**, or restrict an agent's MCP access via the agent's **MCPs** tab.

### 5.4 OpenAI direct always uses the Responses API

Services with type **OpenAI** unconditionally route through the OpenAI Responses API. If you require legacy Chat Completions behavior, configure an **OpenAI Compatible** service pointing at the OpenAI endpoint and turn **Use Responses API** off for that service.

### 5.5 Standalone MCP server binary scope clarified

The standalone `mattermost-mcp-server` binary (separate process, stdio transport) is documented as **development and local use only** and is not supported in production deployments. Production environments should rely on the embedded Mattermost MCP server and, when external clients are required, the HTTP MCP server endpoint configured from the System Console.

### 5.6 Removed in-product capabilities

| Removed | Replacement |
|---|---|
| Built-in GitHub and Jira issue tools | MCP-backed providers |
| Built-in Mattermost search/user tools | Embedded Mattermost MCP server (`search_posts`, `search_users`) |
| Prompt hint buttons | Custom prompt templates |
| Ephemeral OAuth-prompt messages in threads | **Connect** button in the agent's **MCPs** tab and the Agents RHS **Tools** menu |
| `Minimum Size Ratio` chunking option (embedding search) | Removed; not replaced |
| `LLM_PostMeta` DB table | `LLM_Conversations` + `LLM_Turns` |
| `config.bots` plugin-config entries | DB-backed `Agents_UserAgents` |

## 6. Default behavior changes

Defaults that ship with v2.0.0 differ from v1.x in the following ways. None of these defaults change the behavior of an agent that was already explicitly configured before the upgrade — they apply to **new** agents and to capabilities that were not previously toggled.

| Setting | v1.x default | v2.0.0 default | Scope of change |
|---|---|---|---|
| MCP client | Disabled / opt-in | **Always on** | All deployments |
| Embedded Mattermost MCP server | Disabled / opt-in | **Always on** | All deployments |
| OpenAI direct: Responses API | Toggle, off-by-default | **Always on (no toggle)** | OpenAI service type only |
| Native web search (per agent) | Off | **On for new agents** | Capable providers: OpenAI, Azure OpenAI, Anthropic, Google Gemini, Google Vertex AI. Native tools are filtered out at request time for Bedrock, Cohere, Mistral, and Scale-backed services. |
| Extended reasoning / thinking (per agent) | Off | **On for new and migrated agents** (migration 000006 backfills `ReasoningEnabled=true`) | Capable providers (same scope as native web search) |
| Structured output (per agent) | Off | **Off** (default `false` in both migration 000006 and the Agents UI's `emptyDraft`, for both new and migrated agents) | Capable providers: OpenAI, OpenAI Compatible, Azure OpenAI, Anthropic |

> **Migrated agents and native web search.** The migration 000006 backfill leaves `EnabledNativeTools = '[]'` for existing agent rows. Agents that were migrated from `config.bots` therefore do not automatically gain native web search at upgrade time. To enable native web search for a migrated agent, edit the agent on the **Agents** page and turn on **Enable Web Search** under the **Configuration** tab.

> **Reasoning is on by default for migrated agents.** Migration 000006 backfills `ReasoningEnabled = true` for existing rows. If your environment relies on agents *not* using extended thinking — for example, to reduce token spend or to keep latency predictable for a particular agent — explicitly turn off **Reasoning Enabled** on each affected agent after the upgrade.

> **Anthropic structured output and extended thinking are mutually exclusive.** When Anthropic structured output is enabled, the UI disables extended thinking for that agent — the two cannot be active simultaneously.

## 7. Step-by-step upgrade procedure

### 7.1 Primary path: Upgrade Mattermost

Use this path when you receive Agents through the Mattermost server packaging.

1. **Take backups.**
    1. Back up the Mattermost database.
    2. Back up `config.json` (or your equivalent stored plugin configuration source).
    3. If your deployment includes indexed vector data on a separate volume, snapshot it as well.
2. **Upgrade to the latest Mattermost v11.6 patch release.** Mattermost v11.6 is the tested source release for the Agents v2.0.0 upgrade.
3. **Confirm the v11.6 upgrade is healthy.** Verify Mattermost starts cleanly and that Mattermost AI / Mattermost Agents is enabled.
4. **Upgrade Mattermost to v11.7.** Mattermost v11.7 includes Agents v2.0.0.
5. **Watch the logs.** On startup, Agents v2.0.0 runs migrations 000005, 000006, and 000007, then performs the legacy bot migration described in [Section 4](#4-what-gets-migrated-automatically). In server logs, confirm:
    - Plugin start messages.
    - Migration log entries for each of 000005, 000006, 000007.
    - The log message `Migrated legacy config bots to self-service agents table` (one-time; only when at least one `config.bots` entry was present at startup).
    - No fatal migration errors. If any error occurs, see [Section 11](#11-troubleshooting).
6. **Open the Agents page.** From the Mattermost product menu, navigate to the top-level **Agents** product entry. Confirm that each agent you noted in [Section 2 step 6](#2-pre-flight-checklist) is present. Migrated agents have no creator listed; system admins can edit and delete them from the row overflow menu.
7. **Spot-check migrated agent configuration.**
    - Open the agent. Confirm the **Configuration** tab shows the expected display name, username, service, model, and custom instructions.
    - Confirm the **Access** tab shows the channel, user, team, and admin restrictions you expect.
    - Confirm the **MCPs** tab shows **Automatically enable all MCP tools** turned on (this preserves the v1.x behavior).
8. **Re-enable user traffic.** End the maintenance window.

### 7.2 Secondary path: Upload the plugin bundle

Use this path only when you manage the Agents plugin bundle separately from the Mattermost server package.

1. **Take backups.** Back up the Mattermost database, `config.json`, and any separate indexed-vector storage.
2. **Disable the plugin.** From **System Console > Plugin Management**, locate **Mattermost AI** / **Mattermost Agents** and select **Disable**. This prevents new traffic during the bundle swap.
3. **Upload the v2.0.0 bundle.** Download the v2.0.0 plugin bundle (`.tar.gz`) from the [GitHub releases page](https://github.com/mattermost/mattermost-plugin-agents/releases). In **System Console > Plugin Management**, select **Upload Plugin** and upload the bundle. If a previous version is shown alongside the new bundle, remove the old version after confirming the new one is selected.
4. **Re-enable the plugin.** Set the plugin to **Enabled**. On startup, v2.0.0 runs migrations 000005, 000006, and 000007, then performs the legacy bot migration described in [Section 4](#4-what-gets-migrated-automatically).
5. **Complete the same post-upgrade checks.** Watch the logs, confirm migrated agents are present, and spot-check migrated agent configuration as described in [Section 7.1](#71-primary-path-upgrade-mattermost).

## 8. Rollback considerations

Some v2.0.0 migrations cannot be cleanly reversed in production. Plan accordingly.

| Change | Reversible? | Notes |
|---|---|---|
| Migration 000005 (`Agents_UserAgents` table) | Yes (drops table) | Down migration drops the table and supporting indexes. Any agents created or edited from the v2 UI between upgrade and rollback are lost. |
| Migration 000006 (bot fields on `Agents_UserAgents`) | Yes (drops columns) | Down migration drops the added columns. |
| Migration 000007 (`LLM_Conversations`, `LLM_Turns`, drop `LLM_PostMeta`) | **Partial** | Down migration drops `LLM_Conversations` and `LLM_Turns` and recreates an empty `LLM_PostMeta`. `LLM_PostMeta` content (v1.x conversation titles) cannot be recovered after migration 000007 drops the table — restore from backup if you need that data. |
| Legacy bot migration (`config.bots` cleared) | **Manual** | The v2 plugin clears `config.bots` after migration. To roll back, restore the pre-upgrade `config.json` from your pre-flight backup. |

The supported rollback path is therefore:

1. Disable the v2.0.0 plugin.
2. Restore the database from the pre-upgrade backup.
3. Restore `config.json` from the pre-upgrade backup.
4. Reinstall the prior v1.x plugin bundle and re-enable.

Do not attempt a rollback that runs only the down migrations — `LLM_PostMeta` content (v1.x conversation titles) cannot be recovered after migration 000007 drops the table; restore from backup if you need that data.

## 9. HA-specific notes

In HA deployments, complete the upgrade during a maintenance window and allow the cluster to start normally. Agents coordinates the schema migrations and legacy bot migration so only one node performs the data migration work.

After startup, check logs from all nodes for migration errors and confirm the **Agents** page shows the expected migrated agents. If one node reports migration errors while another node succeeds, stop the rollout, preserve the logs, and contact Mattermost Support before retrying.

## 10. Verification checklist

Tick through this list before ending the upgrade window.

- [ ] Plugin status in **System Console > Plugin Management** shows v2.0.0 enabled with no errors.
- [ ] Server logs show successful execution of migrations 000005, 000006, 000007.
- [ ] Server logs show `Migrated legacy config bots to self-service agents table` (or no legacy bots existed in the source install).
- [ ] The top-level **Agents** product page is reachable from the Mattermost product menu.
- [ ] Every bot that existed in v1.x appears as an agent in the **Agents** list with the same display name, username, and service binding.
- [ ] Editing a migrated agent shows the expected access rules and **Automatically enable all MCP tools** turned on.
- [ ] **System Console > Plugins > Agents > AI Bots** redirects to the **Agents** product page.
- [ ] **System Console > Plugins > Agents > Model Context Protocol (MCP)** does not show **Enable MCP Client** or **Enable Embedded Server** toggles.
- [ ] Embedding search status (if previously enabled) shows healthy in **System Console > Plugins > Agents > Embedding Search**, and any prior reindex job is no longer stuck.

## 11. Troubleshooting

If the upgrade fails, stop the rollout and preserve the Mattermost server logs from every node. Restore the database and `config.json` from the pre-upgrade backups if you need to return service to the prior version.

If migrations fail, the legacy bot migration does not complete, or migrated agents do not appear on the **Agents** page after startup, contact Mattermost Support with the server version, Agents plugin version, migration log entries, and whether the deployment is single-node or HA.

## 12. Where to get help

- **Issue tracker:** [github.com/mattermost/mattermost-plugin-agents/issues](https://github.com/mattermost/mattermost-plugin-agents/issues)
- **Release notes:** [github.com/mattermost/mattermost-plugin-agents/releases](https://github.com/mattermost/mattermost-plugin-agents/releases)
- **Admin guide:** [admin_guide.md](admin_guide.md)
- **Provider configuration guide:** [providers.md](providers.md)
- **Mattermost support channels:** customers with a support contract should open a case through the standard Mattermost support process and reference this upgrade guide.
