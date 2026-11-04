# Back up and automate Agents

Agents stores its configuration, agents, and conversations in the Mattermost database, not `config.json`, so a database backup covers it. Automate configuration through `/plugins/mattermost-ai/admin/config`.

## What's stored where

A regular Mattermost database backup covers everything below. The plugin has no separate data store.

| Data | Location | Notes |
|---|---|---|
| Plugin configuration: services, MCP servers, web search, embedding settings, and every setting in **System Console > Plugins > Agents** | `Agents_ConfigHistory` | The row with `Active = true` is current. Every save adds a row, and older rows are kept. **API keys and other credentials are stored in this JSON, in the active row and in the history.** |
| Agents | `Agents_UserAgents` | Identity, instructions, access rules, and tool grants. |
| Agent version history | `Agents_AgentVersions` | Includes past custom instructions. |
| Skills (custom prompts) and pins | `LLM_CustomPrompts`, `LLM_CustomPromptPins` | |
| Conversations | `LLM_Conversations`, `LLM_Turns` | Messages, tool calls, and tool results. A conversation is soft-deleted when its post is deleted. |
| Channel auto-reply settings | `Agents_ChannelAutoReply` | |
| Migration state | `Agents_System` | Flags such as which one-time migrations ran. |
| Semantic search vectors | `llm_posts_embeddings` | Can be rebuilt with a Full Reindex. Back it up separately for large deployments to avoid a long reindex. |
| MCP OAuth tokens, plugin MCP registrations | Mattermost plugin key-value store | |
| Access policies | Mattermost's access control store | Stored by the server, not the plugin. |

The `PluginSettings.Plugins["mattermost-ai"]` entry in the server's configuration is emptied after the one-time migration to the database, and nothing reads it. Editing it does nothing. Use the System Console or the admin API.

Restoring a backup that predates a credential change restores the old credentials. Rotate any key you pasted into the System Console if a database backup or config history might be exposed.

## Back up

1. Include the Mattermost database in your normal backup. That covers every table above.
2. For a large semantic search index, snapshot `llm_posts_embeddings` too.
3. To restore, restore the database, then enable the plugin. Restoring only `config.json` doesn't bring back agents or services.

## Configuration API

Use the admin API to read or write plugin settings from scripts. It requires a session or personal access token for a user with the `manage_system` permission.

```bash
# Read
curl -s -H "Authorization: Bearer $MM_TOKEN" \
  https://your-mattermost-server/plugins/mattermost-ai/admin/config

# Write (send the whole configuration back)
curl -s -X PUT -H "Authorization: Bearer $MM_TOKEN" -H "Content-Type: application/json" \
  --data @config.json \
  https://your-mattermost-server/plugins/mattermost-ai/admin/config
```

`PUT` replaces the configuration, so read it first, change what you need, and send it all back. The response is the saved configuration with any server-assigned IDs. Saves go through the same license and validation checks as the System Console. Invalid MCP configuration, duplicate IDs, bad link domains (when **Render AI-generated links** is on), and gated changes are rejected.

A trimmed example of the shape:

```json
{
  "services": [
    {
      "id": "k3g8sdb1ntbibnyt8u5jmqhc7w",
      "name": "OpenAI Service",
      "type": "openai",
      "apiKey": "sk-...",
      "defaultModel": "gpt-4o",
      "fallbackServiceID": "z9d4meq7jtrx5nch1u8kwsoa3e"
    }
  ],
  "defaultBotUserID": "q8n4w7bpstfxjcmk3yr9zhde1a",
  "allowUnsafeLinks": false,
  "enableChannelMentionToolCalling": false,
  "mcp": { "enablePluginServer": false, "servers": [], "idleTimeoutMinutes": 30 },
  "webSearch": { "enabled": false }
}
```

Service types are `openai`, `anthropic`, `azure`, `openaicompatible`, `bedrock`, `cohere`, `north`, `mistral`, `gemini`, and `vertex`. Config keys for each System Console setting are in the [settings table](admin_guide.md#plugin-settings).

- **Service IDs** are 26-character Mattermost IDs. Leave `id` empty on a new service and the server assigns one. Configurations from before this format used UUIDs and were rewritten once on upgrade. A write that carries a UUID service ID is rejected.
- **Default and transcript agents** are chosen by bot user ID in `defaultBotUserID` and `transcriptBotUserID`, so renaming an agent doesn't change the selection. `defaultBotUserID` is the agent used by features that don't name one, such as meeting summaries. `transcriptBotUserID` selects the agent whose OpenAI, OpenAI Compatible, or Azure service transcribes call recordings through the `/post/{postid}/transcribe/file/{fileid}` route. The **Create meeting summary** button doesn't use it. The older `defaultBotName` and `transcriptBackend` username fields are read only when the ID field is empty. On startup and on every save, the plugin replaces a username with the matching bot user ID. A username no agent holds stays in place and is logged as a warning. Automation should write the ID fields and set the legacy fields to `""`.
- **Legacy `bots`.** Configurations that stored agents in `bots` are migrated into the database on startup, then `bots` is cleared. See [Upgrade from Agents 1.x](upgrading_to_2.0.md).

## Agent API

Agents have REST routes under `/plugins/mattermost-ai/agents`: `POST` to create, `GET` to list, and `GET`, `PUT`, and `DELETE` on `/{agentid}`. They also have `/{agentid}/avatar`, `/{agentid}/versions`, and `/{agentid}/access_policy`. These routes enforce the same permissions and license limits as the Agents page. `PUT` is a full-document save. Send every field. Send `mcpDynamicToolLoading` explicitly on create and update, because omitting it stores `false`.

Every state-changing call is written to the audit log. See [Monitor Agents](observability.md#audit-logging).

## High availability

Agent and configuration changes propagate to every node in a cluster by cluster event, within seconds. License changes are picked up on every node without a restart. If a node misses an event, a config save or plugin reload on it forces a fresh load from the database.
