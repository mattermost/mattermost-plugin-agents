# Upgrade from Agents 1.x

This page covers upgrading the Mattermost Agents plugin (formerly Mattermost AI and Mattermost Copilot) from a v1.x release to v2. It lists the supported version path, the migrations that run on first start, the breaking changes and default changes, how to upgrade, and how to roll back. A fresh install of v2 doesn't need it. Start from the [Admin guide](admin_guide.md) instead.

## Before you start

1. **Back up the Mattermost database.** Migrations run on first start, and some can't be reversed.
2. **Back up `config.json`.** The plugin moves its configuration into the database and then clears its entry in `config.json`. The copy is the simplest way to recover your original bot definitions.
3. **Record your bots.** Note each bot's username, display name, service, and access rules, so you can compare them with the migrated agents.
4. **Check your license.** Several v2 capabilities depend on the license level. See [License requirements](admin_guide.md#license-requirements).
5. **Plan a maintenance window.** The plugin restarts, and migrations finish before users resume.

Current Agents releases need Mattermost Server v12.0.0 or later. Earlier v2 releases had lower minimums, so check the release notes of the version you install.

## Version path

Mattermost Server v11.7 includes Agents v2. To upgrade through the server, first upgrade to the latest v11.6 patch release and confirm Agents works, then upgrade to v11.7. If you manage the plugin on its own, upload the plugin bundle instead. See [Upgrade steps](#upgrade-steps).

## What runs on first start

The plugin runs these migrations in order. A cluster mutex ensures one node does the work in a high availability cluster.

| Migration | What it does |
|---|---|
| Schema 000005 to 000007 | Creates `Agents_UserAgents`, adds bot fields to it, and creates `LLM_Conversations` and `LLM_Turns`. Later releases add more tables for versions, auto-reply, and other features. |
| Drop `LLM_PostMeta` | Drops the v1 conversation titles table without copying it. **v1 conversation titles are lost.** Restore from backup if you need them. |
| Configuration to database | Copies the plugin configuration from `config.json` into the database, then clears the `config.json` entry. Edit settings in the System Console or the admin API from now on. See [Back up and automate](backup_and_automation.md). |
| Legacy bots to agents | Copies each `config.bots` entry into `Agents_UserAgents`, reusing the existing bot account. Migrated agents have no creator and no agent admins, so System Admins manage them. They get **Automatically enable all MCP tools** on, and **Dynamic tool loading** on unless the bot stored a value. Then `config.bots` is cleared. |
| Service IDs | Rewrites old UUID service IDs as 26-character IDs. Automation that hard-coded UUIDs must read the configuration again. |
| Structured output | The per-agent structured output setting is gone. A service used by at least one agent that had it on is set to **Native supported**. Other services stay on **Auto**. |

The legacy bot migration waits when a bot's Mattermost account isn't registered yet. It logs "Deferring legacy bot migration: Mattermost bot not found", changes nothing, and retries on the next configuration change or plugin reload. No action is needed.

## Breaking changes

- **`config.bots` is no longer the source of agents.** Editing it does nothing after migration. Tooling that wrote agents there must use the Agents page or the [agent API](backup_and_automation.md#agent-api).
- **The `LLM_PostMeta` table is gone.** Anything that read it breaks.
- **MCP can't be turned off globally.** The **Enable MCP Client** and **Enable Embedded Server** toggles are removed, and both are always on. To limit MCP, disable individual tools or servers, or restrict an agent's tools. See [Connect agents to MCP servers](mcp_integrations.md).
- **OpenAI always uses the Responses API.** For Chat Completions behavior, use an **OpenAI Compatible** service with **Use Responses API** off.
- **The standalone `mattermost-mcp-server` binary is for development and local use only.** For production, use the embedded server and the [HTTP endpoint](mcp_server.md).
- **The AI Bots panel in the System Console** no longer holds the agent editor. It points to the **Agents** page.

| Removed | Replacement |
|---|---|
| Built-in GitHub and Jira issue tools | MCP servers |
| Built-in Mattermost search and user tools | The embedded Mattermost MCP server (`search_posts`, `search_users`) |
| Prompt hint buttons | [Custom prompts](custom_prompts.md) |
| Ephemeral OAuth prompt messages in threads | The **Connect** card and the **Tools** menu |
| **Minimum Size Ratio** chunking option | Nothing |
| `config.bots` entries | Agents in `Agents_UserAgents` |

## Default changes

These apply to new agents and to settings you hadn't changed. An agent you configured explicitly keeps its settings.

| Setting | v1 | v2 |
|---|---|---|
| MCP client and embedded Mattermost MCP server | Opt in | Always on |
| OpenAI Responses API | Toggle, off | Always on, no toggle |
| Native web search | Off | On for new agents on Anthropic, OpenAI, Azure, Google Gemini, and Vertex AI |
| Extended reasoning | Off | On for new and migrated agents |

Migrated agents don't get native web search automatically. Turn it on in the agent's **Advanced configuration**. Migrated agents do get reasoning on. If you rely on agents not using extended thinking, for cost or latency, turn it off on each one.

## Upgrade steps

**Through Mattermost Server.**

1. Take the backups above.
2. Upgrade to the latest v11.6 patch release and confirm Agents is enabled and healthy.
3. Upgrade to v11.7.
4. Watch the server logs for the migrations and the message `Migrated legacy config bots to self-service agents table`. The last appears only if bots existed.
5. Open the **Agents** page. Confirm each bot you recorded is there. Open one and check its configuration, its **Access** tab, and that **Automatically enable all MCP tools** is on.

**Plugin bundle on its own.**

1. Take the backups.
2. Disable the plugin in **System Console > Plugin Management**.
3. Upload the v2 `.tar.gz` from the [releases page](https://github.com/mattermost/mattermost-plugin-agents/releases) and enable it.
4. Check the same things as above.

## Roll back

Don't run only the down migrations. The v1 titles can't be recovered that way. Restore instead:

1. Disable the v2 plugin.
2. Restore the database and `config.json` from your backups.
3. Install the v1 plugin bundle and enable it.

## Verify

- The plugin shows v2 enabled without errors in **Plugin Management**.
- The logs show the migrations and, if you had bots, the legacy bot message.
- Every v1 bot appears on the **Agents** page with the same display name, username, and service.
- **System Console > Plugins > Agents > Model Context Protocol (MCP)** has no **Enable MCP Client** or **Enable Embedded Server** toggles.
- If semantic search was on, **Embedding Search** shows healthy and no reindex job is stuck.

In an HA cluster, check the logs on every node. If one node reports migration errors and another succeeds, stop, keep the logs, and contact support before retrying. If migrations fail or agents don't appear, contact Mattermost Support with the server and plugin versions, the migration log lines, and whether the deployment is single-node or HA. You can also use the [issue tracker](https://github.com/mattermost/mattermost-plugin-agents/issues).
