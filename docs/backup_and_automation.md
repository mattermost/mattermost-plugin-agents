# Back up and automate Agents

Agents stores its configuration, agents, and conversations in the Mattermost database, not `config.json`, so a database backup covers it. Automate configuration through `/plugins/mattermost-ai/admin/config`.

## What's stored where

A regular Mattermost database backup covers everything below. The plugin has no separate data store.

| Data | Location | Notes |
|---|---|---|
| Plugin configuration: services, MCP servers, web search, embedding settings, and every setting in **Agents > Settings** | `Agents_ConfigHistory` | The row with `Active = true` is current. Every save adds a row, and older rows are kept. **API keys and other credentials are stored in this JSON, in the active row and in the history.** |
| Agents | `Agents_UserAgents` | Identity, instructions, access rules, and tool grants. |
| Agent version history | `Agents_AgentVersions` | Includes past custom instructions. |
| Skills (custom prompts) and pins | `LLM_CustomPrompts`, `LLM_CustomPromptPins` | |
| Conversations | `LLM_Conversations`, `LLM_Turns` | Messages, tool calls, and tool results. A conversation is soft-deleted when its post is deleted. |
| Channel auto-reply settings | `Agents_ChannelAutoReply` | |
| Channel instructions and agent-context pins | `Agents_ChannelInstructions`, `Agents_ChannelContextPosts` | Pins store post IDs only; the post content is read live. |
| Migration state | `Agents_System` | Flags such as which one-time migrations ran. |
| Semantic search vectors | `llm_posts_embeddings` | Can be rebuilt with a Full Reindex. Back it up separately for large deployments to avoid a long reindex. |
| MCP OAuth tokens, plugin MCP registrations | Mattermost plugin key-value store | |
| Access policies | Mattermost's access control store | Stored by the server, not the plugin. |

The `PluginSettings.Plugins["mattermost-ai"]` entry in the server's configuration is emptied after the one-time migration to the database, and nothing reads it. Editing it does nothing. Use **Agents > Settings** or the admin API.

Restoring a backup that predates a credential change restores the old credentials. Rotate any key you pasted into **Agents > Settings** if a database backup or config history might be exposed.

## Back up

1. Include the Mattermost database in your normal backup. That covers every table above. Agent avatars are profile images, so also back up the Mattermost file store.
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

`PUT` replaces the configuration, so read it first, change what you need, and send it all back. The response is the saved configuration with any server-assigned IDs. Saves go through the same license and validation checks as **Agents > Settings**. Invalid MCP configuration, duplicate IDs, bad link domains (when **Render AI-generated links** is on), and gated changes are rejected.

### Credentials in the API

`GET` never returns a stored credential. Every configured credential comes back as `********************************` (32 asterisks): service `apiKey`, `awsSecretAccessKey` and `vertexAuthCredentials` (including those of a legacy agent's inline service), MCP `clientSecret` and every custom header value and service account header value, the Google and Brave web search API keys, `openTelemetryHeaders`, and the embedding provider `apiKey` parameter. A credential that isn't configured comes back empty. Identifiers such as `awsAccessKeyID`, `orgId`, URLs and header names are returned as they are. The response to `PUT` is masked the same way.

On `PUT`, each credential field is read like this:

- The exact mask means keep the stored credential. It's restored whatever else changed in the same save, including the service type, an API URL or an MCP server's URL, so a mistyped URL can be fixed without re-entering the key. Services are matched by `id`, MCP servers by `id` (or by name and URL for servers saved before IDs existed), and header values by header name.
- Any other value replaces the stored credential, including a value made only of asterisks that isn't the full mask.
- An empty value clears the credential.
- A mask with no stored credential to restore, such as on a new service or server, or in an **Agents > Settings** page left open across an upgrade or another admin's change, is rejected with `400` and the names of the fields. The mask is never stored. Reload the page, or read the configuration again, and re-enter those credentials.

That makes the read-modify-write flow above safe to run without handling secrets: leave the credentials you don't want to change exactly as `GET` returned them. A script can't read a credential back through this API.

`POST /plugins/mattermost-ai/admin/models/fetch` accepts a `serviceID` so **Agents > Settings** can list models for a saved service without holding its key: a masked `apiKey` or `vertexAuthCredentials` is replaced by the saved service's credential, and an empty value is sent empty. A mask with nothing to resolve it against is rejected with `400`.

A trimmed example of the shape:

```json
{
  "services": [
    {
      "id": "k3g8sdb1ntbibnyt8u5jmqhc7w",
      "name": "OpenAI Service",
      "type": "openai",
      "apiKey": "********************************",
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

Service types are `openai`, `anthropic`, `azure`, `openaicompatible`, `bedrock`, `cohere`, `north`, `mistral`, `gemini`, and `vertex`. Config keys for each setting are in the [settings table](admin_guide.md#plugin-settings).

- **Service IDs** are 26-character Mattermost IDs. Leave `id` empty on a new service and the server assigns one. Configurations from before this format used UUIDs and were rewritten once on upgrade. A write that carries a UUID service ID is rejected.
- **Default and transcript agents** are chosen by bot user ID in `defaultBotUserID` and `transcriptBotUserID`, so renaming an agent doesn't change the selection. `defaultBotUserID` is the agent used by features that don't name one, such as meeting summaries. `transcriptBotUserID` selects the agent whose OpenAI, OpenAI Compatible, or Azure service transcribes call recordings through the `/post/{postid}/transcribe/file/{fileid}` route. The **Create meeting summary** button doesn't use it. The older `defaultBotName` and `transcriptBackend` username fields are read only when the ID field is empty. On startup and on every save, the plugin replaces a username with the matching bot user ID. A username no agent holds stays in place and is logged as a warning. Automation should write the ID fields and set the legacy fields to `""`.
- **Legacy `bots`.** Configurations that stored agents in `bots` are migrated into the database on startup, then `bots` is cleared. See [Upgrade from Agents 1.x](upgrading_to_2.0.md).

## Agent API

Agents have REST routes under `/plugins/mattermost-ai/agents`: `POST` to create, `GET` to list, and `GET`, `PUT`, and `DELETE` on `/{agentid}`. They also have `/{agentid}/avatar`, `/{agentid}/versions`, and `/{agentid}/access_policy`. These routes enforce the same permissions and license limits as the Agents page. `PUT` is a full-document save. Send every field. Send `mcpDynamicToolLoading` explicitly on create and update, because omitting it stores `false`.

Every state-changing call except skill pins is written to the audit log. See [Monitor Agents](observability.md#audit-logging).

## High availability

Agent and configuration changes propagate to every node in a cluster by cluster event, within seconds. License changes are picked up on every node without a restart. If a node misses an event, a config save or plugin reload on it forces a fresh load from the database.
