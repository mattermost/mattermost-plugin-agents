<!--
Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
See LICENSE.txt for license information.
-->

# Use Mattermost Agents as your personal AI assistant

A Mattermost agent can act for you: read and post in Mattermost, call tools in other systems through MCP servers, and ask you before it changes anything. This page shows what an agent can do, how it stays within your permissions, and how to prompt it. It is written for people who chat with agents. Admins configure the tools and approvals described here.

## How an agent works

You message an agent in a direct message or the Agents pane. The agent is a Mattermost bot backed by the LLM your admin chose, which can be a self-hosted model. When a request needs more than the model knows, the agent calls tools:

- **Mattermost tools** to search, read, post, and manage channels, from the built-in [Mattermost MCP server](mcp_server.md).
- **Remote MCP servers** for other systems such as an issue tracker or a calendar. You connect each one with your own account.
- **Plugin tools** from Mattermost plugins that register with Agents.
- **Built-in tools** to search the web (if your admin set it up), create files, ask you a multiple-choice question, and wait for long-running work.

Every Mattermost and MCP tool call runs as you, so the agent reaches only what you can reach. External systems see your identity. The exception is an agent your admin configured with a shared service account. For that agent, external MCP tools use the shared credentials, you aren't asked to connect accounts, and the **Tools** menu is hidden.

## Work across Mattermost

The built-in Mattermost tools cover most day-to-day actions:

- Search posts and files, and read posts, threads, and channels.
- Look up users, teams, groups, and channel members.
- Post to channels, reply in threads, send direct and group messages, and attach text files.
- Schedule posts, set post reminders, and manage drafts, pins, reactions, and bookmarks.
- Check unread counts, mentions, and followed threads.
- Create channels and add people to channels and teams.

Reading tools are available at every license level. Tools that change something need an Enterprise license. See the [tool reference](mcp_tools.md) for all of them.

Example prompts:

- "Search for what we decided about the March launch and link the posts."
- "Summarize what I missed in ~incident-room since yesterday, then post a two-line status in ~leadership."
- "DM Priya the notes from this thread, and remind me about the thread tomorrow at 9."
- "Schedule a post in ~release-announcements for Friday at 9:00 with the changelog."

## Reach other systems with MCP

Admins can connect remote MCP servers such as Jira, GitHub, or an internal service. Remote MCP servers need an Enterprise license. You sign in to each system with your own account, so the agent has your access there and nothing more.

- **Connect when needed.** If you ask for something that needs a system you haven't connected, the agent shows a **Connect** card. Select **Connect**, sign in, and the agent continues. Select **Skip** to continue without it.
- **Tools menu.** In the Agents pane, the **Tools** menu lists each tool provider, lets you **Connect** or **Disconnect**, and lets you turn providers off for yourself.
- **Large tool sets.** By default an agent searches its tool catalog and loads only the tools a request needs, so many servers can be connected without filling the model's context.

Example prompts:

- "What's assigned to me in Jira and due this week? Post the list in my team channel."
- "Open a GitHub issue for the bug in this thread and reply with the link."

See [Use tools and approve actions](using_tools.md).

## Stay in control

- **Approvals.** Depending on your admin's per-tool policy, a tool call runs automatically or waits for you to select **Accept** or **Reject**. The card shows the tool name and arguments. Only the person who started the conversation can approve.
- **Share or keep private.** In a channel, after a tool runs, you choose whether its arguments and results are shared with the channel or kept private.
- **Questions.** When the agent needs a decision, it asks a multiple-choice question in the conversation. You can pick an option, type your own answer, or skip.
- **Audit.** The server audit log records state-changing actions such as agent edits and in-channel tool approvals. It never records prompt or tool content.

Details are in [Tool approval and credentials](tool_approval.md).

## Long-running work

An agent can pause and wake itself after 1 to 30 minutes to check on work started through another tool, then report back. You don't need to prompt it again. Sending a new message in the thread cancels a pending wait.

## Bring the agent into channels

- **@mention** an agent in a channel or thread and it replies in the thread. Everyone in the channel can read and join. This needs a Professional license or higher.
- **Auto-reply.** A channel manager can have an agent reply to new posts without being mentioned. This needs an Enterprise Advanced license.

See [Use agents in channels](agents_in_channels.md).

## Summaries, search, and meetings

Summarize threads, unread messages, and date ranges, search with AI, and summarize Calls recordings and Zoom meetings. See [Summaries and AI search](summaries_and_search.md) and [Meeting summaries](meeting_summaries.md).

## Prompt an agent well

- **Be specific.** "Review this meeting summary and list action items that need follow-up" works better than "help me with this document".
- **Name the system and the target.** For an action that changes something, say where: "Create a Jira ticket in the PLAT project" beats "make a ticket". If the target is missing, the agent asks before it acts.
- **Give context.** Say what the output is for and who reads it.
- **Iterate.** Treat the first answer as a draft. Ask for specific changes such as "remove the second section" or "condense this into one paragraph".
- **Ask for another view.** "How would the on-call engineer read this?" often finds gaps.
- **Save what works.** Turn prompts you reuse into [custom prompts](custom_prompts.md).

## Use Mattermost from your own AI client

You can also bring Mattermost into the AI tools you already use. Claude Code, Claude Desktop, Cursor, VS Code, and other MCP clients can connect to the built-in Mattermost MCP server and sign in as you. See [Mattermost MCP server](mcp_server.md).
