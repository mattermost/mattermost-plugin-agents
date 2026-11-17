# Troubleshooting

Fix common Agents problems by symptom. Start with the Mattermost Server log, filtered by plugin ID `mattermost-ai`. For request-level detail, turn on trace output in **Server Logs** mode. See [Monitor Agents](observability.md).

## Setup and activation

| Symptom | Cause and fix |
|---|---|
| The plugin won't enable and the log says "site URL not configured" | Set **Site URL** under **System Console > Environment > Web Server**. |
| The Agents pane asks users to configure an LLM | No service is configured. Add one under **System Console > Plugins > Agents**. See [Configure LLM services](llm_services.md). |
| A self-hosted model returns errors, or the agent never answers | The endpoint might not support the Responses API. For an OpenAI Compatible service, turn **Use Responses API** off. Also check that the **API URL** is reachable from the Mattermost Server process and that **Default model** matches a model the server exposes. See [Provider configuration](providers.md#openai-compatible-ollama-vllm-and-others). |
| A self-hosted model ignores tools | The model might not support tool calling. Send a direct message that needs a tool and look for a tool call in the reply. If there is none, turn **Enable Tools** off for that agent. See [Verify a self-hosted model](providers.md#verify-a-self-hosted-model). |
| A self-hosted model keeps calling tools without answering | Lower **Max tool turns** on the agent so it answers sooner, or turn **Enable Tools** off. |
| The agent doesn't reply in a direct message | Check the log for the provider error. Common causes are a wrong API key or model name, a service missing required settings, or an unreachable endpoint. Also check the agent isn't **Inactive**. |
| `AccessDeniedException`, `ValidationException`, or throttling from Bedrock | See [Configure Agents with AWS Bedrock](aws_bedrock_setup.md#troubleshooting). |
| Saving a service or setting fails with a license message | The capability needs a higher license level. The message names it. See [License requirements](admin_guide.md#license-requirements). |
| Requests fail through an HTTP proxy | LLM traffic follows `HTTP_PROXY` and `HTTPS_PROXY` on the Mattermost Server process. Set them there and restart. |

## Agents

| Symptom | Cause and fix |
|---|---|
| **Create agent** is disabled | The license's agent cap is full (1 on Free, 3 on Professional). Delete an agent or upgrade. |
| **Create agent** is hidden | The user lacks `manage_own_agent` and isn't a System Admin. |
| **Edit** and **Delete** are missing for an agent | The user isn't its creator or an agent admin, and lacks `manage_others_agent`. Migrated v1 agents have no creator, so only System Admins manage them. |
| An agent shows **Inactive** | Hover for the reason: deleted or incomplete service, a service not active on the license, or the agent cap. See [Inactive agents](managing_agents.md#inactive-agents). |
| "This username is already taken" | Another user or bot, including a deactivated one, holds it. Pick another name. |
| The avatar didn't change | The avatar uploads after the save and can fail alone, for example if it's over 10 MB. Select the image and save again. |
| A change doesn't show on another cluster node | Propagation takes seconds. If it doesn't arrive, look for "Failed to re-ensure bots after agent update cluster event" or "Failed to publish agent update cluster event" in that node's log, and reload the plugin on it. |
| A user can't see or use an agent | An access list, a channel restriction, or a service access policy excludes them. See [Control who can use agents](access_control.md). |
| Nobody can use an attribute-based agent | It has no policy, or the access engine is unavailable. An attribute-based agent with no policy denies everyone. Define a policy. |

## Channels

| Symptom | Cause and fix |
|---|---|
| A feature or button is missing, does nothing, or returns a licensing message | Check [License requirements](admin_guide.md#license-requirements). Channels, summaries, write tools, remote MCP servers, AI search, and meeting summaries each need a license level. |
| The agent replies but won't use tools in a channel | **Enable Channel Mention Tool Calling** is off. It's off by default. See [Control agents in channels](channel_settings.md). |
| The **Agents** channel settings tab is missing | It needs an Enterprise Advanced license, the channel-properties permission, a public or private channel, and at least one agent available in the channel. |
| Ambient auto-reply stays silent | **Enable ambient auto-replies** is off, the classification said no, or a rate limit applied. See [Control agents in channels](channel_settings.md#ambient-limits-and-cost). |
| Users can't click links in replies | **Render AI-generated links** is off, or the domain isn't on the allowlist. See [Secure AI-generated links and HTML](ai_output_security.md). |

## Tools and MCP

| Symptom | Cause and fix |
|---|---|
| A tool call fails repeatedly | After three failures in a row the agent stops using tools and describes the error. Check API keys and endpoints, MCP connectivity and third-party authorization, and that the user can access the target. Trace output shows tool errors. |
| Agents have no Mattermost tools after an upgrade from v1, or channel questions fail with "read_channel tool not available" | The embedded Mattermost MCP server stays off for a configuration migrated from v1 until it's saved. Open **System Console > Plugins > Agents** and select **Save** once. See [Upgrade steps](upgrading_to_2.0.md#upgrade-steps). |
| An agent has no tools | **Enable Tools** is off, or the MCP grants on its **MCPs** tab don't include the tool, or an access policy or the user's **Tools** menu choices removed the server. |
| A remote server's tool list is empty | For per-user OAuth servers, the user must connect first. For a service account agent, **No service account credentials** means the server has no service account headers. **Couldn't connect** means the headers were rejected. Check that the header value doesn't repeat the header name. |
| An OAuth authorization fails with a redirect error | Register the **MCP OAuth Callback URL** from the **Configuration** tab in the remote server's OAuth app. |
| Atlassian shows an authorization error | An Atlassian organization admin must allow your domain. See [Atlassian MCP server](mcp_integrations.md#atlassian-mcp-server). |
| Users can't connect OAuth servers on mobile | Mobile can't start MCP OAuth. Use web or desktop. |
| The agent says it can't find a tool it should have | With dynamic tool loading, `search_tools` might not surface it. Add a **Retrieval description override** on the **Tools** tab, or turn dynamic loading off for that agent. |
| External MCP client problems | See [Troubleshooting in the MCP server guide](mcp_server.md#troubleshooting). |

## Search, summaries, and meetings

| Symptom | Cause and fix |
|---|---|
| Channel questions fail with an error about embedded MCP tools | The built-in `read_channel` and `get_channel_info` tools are unavailable to the user. An access policy on the built-in server is the usual cause. |
| The agent can't read the text of an attached PDF or Office document | The server must extract document text. Turn on **Enable document search by content** (`FileSettings.ExtractContent`) under **System Console > Environment > File Storage**. |
| AI search or `/ask-channel` is unavailable | They need an Enterprise license, and the agent needs the built-in Mattermost tools. |
| Search results are keyword-only | Semantic search isn't set up, or the index is missing. See [Set up semantic search](embedding_search.md). |
| Semantic search returns HTTP 503 | A deferred reindex or a vector index rebuild is running. Search returns when it finishes. |
| The panel shows **Embedding Model Changed** | The provider, model, dimensions, or precision changed. Run a **Full Reindex**. |
| A meeting summary fails | Check the server log. The default agent might be inactive or its service unreachable. See [Meeting summaries](meeting_summaries.md). |

## HTML artifacts

| Symptom | Cause and fix |
|---|---|
| "can only be viewed inside Mattermost" | Mattermost is served over plain HTTP, or the artifact URL was opened directly in a tab. Use HTTPS, and open it from the conversation. |
| "This artifact could not be loaded" | The **Site URL** doesn't match the address users open. |
| An artifact can still reach the network | The browser isn't Chromium 152 or later. See [Secure AI-generated links and HTML](ai_output_security.md#html-artifacts). |
