# Data handling

What Mattermost Agents sends to the LLM and other services for each feature, and what it stores, for admins and security reviewers. With a self-hosted model, content stays in your environment. With a cloud provider, the provider receives it and its own terms apply. End users read a shorter version in [What agents can see and keep](what_agents_see.md).

For strict privacy requirements, run the LLM yourself so PII and message content never reach an outside vendor. See [Sovereign AI deployments](sovereign_ai.md).

## What every request includes

Each request to the LLM starts with a system prompt that contains:

- The agent's display name and username, and the model name.
- The Mattermost Server name, the company name if one is set, and the Site URL.
- The current date and time in the requesting user's location.
- The requesting user's username, first and last name, position, and locale, and whether they're a System Admin. Not their email address or user ID.
- In a channel, the channel name and display name, and the team name and display name.
- The agent's custom instructions.

Each request also carries the definitions (name, description, and parameters) of the tools the agent can use. For Gemini and Vertex AI, the system prompt lists them too. Then it adds the conversation itself. What the conversation includes depends on how the agent was started.

## What each entry point sends

| Entry point | Sent to the LLM |
|---|---|
| Direct message or Agents pane | The messages exchanged with the agent in that conversation, plus attachments. A separate small request generates the conversation title from your first message. |
| @mention in a channel, not in a thread | The mentioning message, its attachments, and the channel and team names. |
| @mention in a thread | The thread's messages up to the mention, with each author's username and attachment file names. Replies after the mention aren't included. |
| Channel auto-reply | The same as an @mention. |
| Ambient auto-reply | Each new message and its thread go to a classification request on the agent's model, with the channel's ambient instructions. If it decides to reply, the reply is an ordinary mention. |
| Summarize Thread, Find action items, Find open questions | The thread's messages with their authors. |
| Ask Agents about this channel, Ask AI, `/summarize-channel` | The channel's posts for the range you chose, with their authors. |
| Meeting summary | The transcript from Calls, Zoom, or Google Meet. Long transcripts are split into chunks. |
| AI search and `/ask-channel` | Your query, plus the posts the agent reads while it searches. |
| React for me | The text of the message you picked. |
| Custom prompts | The rendered prompt, as an ordinary message. |

Attachments in a conversation are included when the model and agent allow them. Images need **Enable Vision** and a vision-capable model. Other files contribute text only when Mattermost can extract it.

## What tools add

Tool arguments and results go to the LLM, because the model needs them to continue. A Mattermost tool returns what the requesting user can read, so a result might include posts, channel members, or profile fields. An external MCP server's results go to the LLM too.

In channels, a result the initiator keeps private isn't sent to the LLM for the channel-visible reply. The model doesn't see it when writing that reply. See [Tool approval and credentials](tool_approval.md).

## What reaches other services

| Service | What it receives |
|---|---|
| LLM provider | Everything described above. |
| Web search provider (Brave, Google, SearXNG) | The model's search queries, and page fetches for result URLs. |
| MCP servers | Tool arguments the model chooses. Remote servers see your identity when you connect with your own account. |
| Embedding provider | The text of every indexed post, in chunks, when semantic search is on. |
| Native provider tools | Whatever the provider's tool needs, such as search queries or code. Code execution may retain content in the provider's sandbox under the provider's terms. |

## What Agents stores

| Data | Where | Content |
|---|---|---|
| Conversations | Plugin tables in the Mattermost database | Messages, tool calls and results, and titles. A conversation is soft-deleted when its root post is deleted. |
| Semantic search index | Plugin table in the database | Post text chunks and their vectors. Deleting a post removes its embeddings. |
| Configuration and agents | Plugin tables | Service credentials, MCP server settings, agent instructions, and version history. |
| Token usage log | `logs/agents/token_usage.log`, if enabled | Counts and identifiers. No message content. |
| Audit records | The server audit log | Identifiers and outcomes. No prompts, messages, tool arguments, or results. |
| Traces | Server log or an OTLP collector, if enabled | Identifiers and sizes. No prompts or tool results. |

Where each table lives and how to back it up is in [Back up and automate](backup_and_automation.md).

## Reduce what leaves your environment

- Use a self-hosted OpenAI-compatible model for the LLM, and for embeddings if you use semantic search.
- Use SearXNG for web search so queries stay in your network, or leave web search off.
- Restrict agents to the teams and channels that need them. See [Control who can use agents](access_control.md).
- Leave tools off in channels, which is the default, so shared conversations don't carry tool results.
- Set tools that return sensitive data to **Ask Every Time**.
- Review each remote MCP server before you connect it. Its operator receives tool arguments.
