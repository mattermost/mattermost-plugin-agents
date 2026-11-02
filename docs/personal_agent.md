<!--
Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
See LICENSE.txt for license information.
-->

# Use Mattermost Agents as your personal AI assistant

Your personal agent searches, reads, and posts in Mattermost and works in other systems through MCP servers, as you, and asks before it changes anything. This page lists what it can do, how it reaches other systems, and how to prompt it well.

## How an agent works

You message an agent in a direct message or the **Agents** pane. The agent is a Mattermost bot backed by the LLM your admin chose, which can be a self-hosted model. When a request needs more than the model knows, the agent calls tools:

- **Mattermost tools** to search, read, post, and manage channels, from the built-in [Mattermost MCP server](mcp_server.md).
- **Remote MCP servers** for other systems such as an issue tracker or a calendar. You connect each one with your own account.
- **Plugin tools** from Mattermost plugins that register with Agents.
- **Built-in tools** to search the web (if your admin set it up), create files, ask you a multiple-choice question, and wait for long-running work.

Every Mattermost and MCP tool call runs as you, so the agent reaches only what you can reach, and external systems see your identity. The exception is an agent your admin configured with a shared service account. For that agent, external MCP tools use the shared credentials, you aren't asked to connect accounts, and the **Tools** menu is hidden. Calls that need your approval wait for you to accept them. See [Use tools and approve actions](using_tools.md).

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

- "DM Priya the notes from this thread, and remind me about the thread tomorrow at 9."
- "Schedule a post in ~release-announcements for Friday at 9:00 with the changelog."
- "Who is in ~platform-oncall, and which of them are online?"

## Reach other systems with MCP

Admins can connect remote MCP servers such as Jira, GitHub, or an internal service. Remote MCP servers need an Enterprise license. You sign in to each system with your own account, so the agent has your access there and nothing more.

- **Connect when needed.** If you ask for something that needs a system you haven't connected, the agent shows a **Connect** card. Select **Connect**, sign in, and the agent continues. Select **Skip** to continue without it.
- **Tools menu.** In the Agents pane, the **Tools** menu lists each tool provider, lets you **Connect** or **Disconnect**, and lets you turn providers off for yourself.
- **Large tool sets.** By default an agent searches its tool catalog and loads only the tools a request needs, so many servers can be connected without filling the model's context.

Example prompts:

- "Find the Jira ticket mentioned in this thread and summarize its status."
- "List my open pull requests that need review and message me the list."

## Long-running work

An agent can pause and wake itself after 1 to 30 minutes to check on work started through another tool, then report back. You don't need to prompt it again. Sending a new message in the thread cancels a pending wait.

An agent can also ask you a multiple-choice question when it needs a decision. Pick an option, type your own answer, or skip.

## Prompt an agent well

- **Name the system and the target.** For an action that changes something, say where: "Create a Jira ticket in the PLAT project" beats "make a ticket". If the target is missing, the agent asks before it acts.
- **Say what to do with the result.** "Post it in ~team-eng", "as a file", or "message it to me" tells the agent where the output goes.
- **Start a new chat for a new topic.** An agent knows only the current conversation, so a new topic works better in a new one.
- **Save what works.** Turn prompts you reuse into [custom prompts](custom_prompts.md).
