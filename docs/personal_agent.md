<!--
Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
See LICENSE.txt for license information.
-->

# Use Mattermost Agents as your personal AI assistant

Mattermost Agents gives each person a personal AI agent they can talk to in a single conversation, and that agent can act on their behalf: read and post in Mattermost channels, reach other systems through MCP servers, and use integrations, all with the user's own permissions and approval. This page walks through what already exists and links to the detailed docs.

## How it fits together

You talk to an agent in Mattermost, in a direct message or the **Agents** pane. The agent is a Mattermost bot backed by the LLM your admin chose, including a self-hosted model. When a request needs more than the model's own knowledge, the agent calls tools:

- **Mattermost tools** to search, read, and post in Mattermost.
- **Remote MCP servers** for other systems, such as an issue tracker or a calendar, each connected with your own account.
- **Plugin tools** from Mattermost plugins that register with Agents.

Mattermost is the hub. Conversations, approvals, and results stay in Mattermost, and every tool call runs as you, so the agent can reach only what you can reach. Admins control which tools exist, which need approval, and who can use each agent.

## Talk to your agent

- **Direct message.** Start a direct message with an agent bot for a private conversation. This is the default place where tools run, and tool results are shared automatically because only you can see the conversation.
- **Agents pane.** Open the **Agents** icon in the apps sidebar to get a right-hand pane with suggested prompts, pinned custom prompts, file attachments, and a **Tools** menu.
- **Mobile.** Open a direct message with the agent. Connecting OAuth-backed MCP servers needs web or desktop.
- **Multiple agents.** Your workspace can have several agents with different instructions, models, and tool access. Pick one from any agent picker, or @mention one by name. See [Managing Agents](features/managing_agents.md).

See [Access AI features](user_guide.md#access-ai-features) and [Chat with agents](user_guide.md#chat-with-agents).

## What the agent can do in Mattermost

The built-in Mattermost tools run through the embedded [Mattermost MCP server](mcp_server.md), always as you. They include:

- Search posts and files, read posts, threads, and channels, and look up users, teams, and channel members.
- Post to channels and reply in threads, send direct messages and group messages, and attach text files to posts.
- Create channels, add people to channels and teams, and manage bookmarks, pins, and reactions.
- Create scheduled posts and set post reminders.
- Check unread counts, mentions, and threads you follow.
- List the agents available to you.

Read-only tools are available at every license level. Tools that change state need an Enterprise license. See the [license requirements](admin_guide.md#license-requirements). The full list is in [Available Tools](admin_guide.md#available-tools).

Example prompts:

- "Search for what we decided about the March launch and link the posts."
- "Summarize what I missed in the incident channel since yesterday, then post a two-line status in the leadership channel."
- "DM Priya the notes from this thread and remind me about the thread tomorrow morning."
- "Schedule a post in the release channel for Friday at 9:00 with the changelog."

## Reach other systems with MCP

Admins can connect remote MCP servers, for example for Jira, GitHub, or an internal service. Each user signs in to those systems with their own account, so the agent acts with your access there and external systems see your identity. Remote MCP servers are available at Enterprise and above.

- **Connect in the conversation.** If you ask for something that needs a server you have not connected, the agent shows a **Connect** card in the chat. Select **Connect**, sign in, and the agent continues. Select **Skip** to continue without it.
- **Tools menu.** In the Agents pane, the **Tools** menu shows each provider, lets you **Connect** or **Disconnect**, and lets you turn individual providers on or off for yourself.
- **Plugin-registered tools.** Compatible Mattermost plugins can register MCP tools with Agents. Once an admin enables them, the agent can use them with the same user permissions and approval policies.
- **Dynamic tool loading.** Agents can search a large tool catalog and load only the tools a request needs, so many servers can be connected without filling the model's context.

Example prompts:

- "What is assigned to me in Jira that is due this week? Post the list in my team channel."
- "Open a GitHub issue for the bug described in this thread and reply with the link."

See [Model Context Protocol (MCP) Integration](admin_guide.md#model-context-protocol-mcp-integration) and [Use tools](user_guide.md#use-tools).

## Stay in control of what it does

- **Tool approval.** Depending on the admin's per-tool policy, a tool call can run automatically or wait for you to select **Accept** or **Reject**, with the tool name and arguments shown. Only the person who started the conversation can approve. Per-tool policies are an Enterprise feature.
- **Share or keep private.** In channels, after a tool runs you choose whether its arguments and results are shared with the channel (**Share**) or kept private (**Keep private**). If you keep them private, the agent's reply is generated without that content.
- **Your permissions.** Mattermost and plugin tools run as you. For agents an admin configures with service account authentication, external MCP tools use shared credentials instead, and you are not asked to connect accounts.
- **Admin governance.** Admins control who can use each agent and which tools it can call. The server audit log records state-changing actions such as configuration changes, agent edits, and in-channel tool approvals, without prompt or tool content. See [Multiplayer Tool Calling](features/multiplayer_tool_calling.md) for the full model.

## Bring the agent into channels

- **@mention.** Mention an agent in a channel or thread, such as `@copilot`, and it replies in the thread using the thread's earlier context. Everyone in the channel can see and join the conversation. Multiplayer agents in channels are available at Professional and above. By default, tool calls run in direct messages; an admin can enable the experimental **Enable Channel Mention Tool Calling** setting to allow tools in channels.
- **Channel auto-reply.** A channel manager can set an agent to reply to new top-level posts, to threads too, or in experimental ambient modes where the agent replies only when a classification step decides a reply is useful. Auto-reply is available at Enterprise Advanced. See [Channel auto-reply](user_guide.md#channel-auto-reply).
- **Ask the user.** An agent can ask you a multiple-choice question in the conversation when it needs a decision before continuing.

## Summaries, search, and meetings

- **Threads and channels.** Summarize a thread, summarize unread messages, or select **Ask Agents about this channel** to summarize a date range or ask a question. See [Summarize discussion threads](user_guide.md#summarize-discussion-threads) and [Channel Summaries](features/channel_summaries.md). Available at Professional and above.
- **AI search.** Search from the Mattermost search bar by choosing **Agents**. The agent runs keyword searches, reads the surrounding threads, and cites posts. With embedding search configured, it also uses semantic search. Use `/ask-channel` to ask about the channel you are in. AI search is experimental and available at Enterprise and above. See [Search with AI](user_guide.md#search-with-ai).
- **Meeting summaries.** Select **Create meeting summary** above a Mattermost Calls recording or a Zoom transcript to get a summary as a direct message. Available at Enterprise and above. See [Record calls to summarize meetings](user_guide.md#record-calls-to-summarize-meetings).

## Files, charts, and artifacts

- **Create files.** Ask for output as a file, such as "the meeting notes as a Markdown file" or "a Python script that parses this log". The agent attaches text-based files to its reply. See [Ask agents to create files](user_guide.md#ask-agents-to-create-files).
- **HTML artifacts.** When an admin turns on **Enable HTML artifacts**, HTML files that an agent creates render as interactive pages inside the reply, with a fullscreen viewer. This is experimental. See [HTML artifacts](admin_guide.md#html-artifacts).
- **Read attachments.** Attach files and images to the conversation. The agent can read text from supported files, and use images when vision is enabled for its model.

## Long-running work

For work that finishes later, an agent can pause and be woken again after a set number of minutes to check on it, up to 30 minutes at a time. This lets it follow up on an asynchronous task started through another tool without you re-prompting.

## Make it yours

- **Custom prompts.** Save prompts you reuse, pin them as buttons in the Agents pane, and use variables such as your name and the current channel. Personal prompts are available at every license level, and sharing prompts with others needs Enterprise. See [Custom Prompts](features/custom_prompts.md).
- **Your own agents.** Where your role allows, create an agent with its own instructions, model, access rules, and MCP tools, with version history, restore, and export and import. See [Managing Agents](features/managing_agents.md).
- **Choose the model.** Your admin can connect a self-hosted model or a cloud provider. See [LLM Provider Configuration](providers.md) and [Implementing Sovereign AI](sovereign_ai.md).
- **Prompting tips.** See [Agents Usage Tips and Best Practices](usage_tips.md).

## The other direction: use Mattermost from your own AI client

You can also bring Mattermost into the AI tools you already use. The Agents plugin includes a built-in Mattermost MCP server that Claude Code, Claude Desktop, Cursor, VS Code, and other MCP clients can connect to, signing in as you with OAuth. The client then searches and reads Mattermost, and posts to it, with your permissions. See [Mattermost MCP Server](mcp_server.md).

## Related docs

- [User Guide](user_guide.md)
- [Admin Guide](admin_guide.md)
- [Mattermost MCP Server](mcp_server.md)
- [Managing Agents](features/managing_agents.md)
- [Multiplayer Tool Calling](features/multiplayer_tool_calling.md)
