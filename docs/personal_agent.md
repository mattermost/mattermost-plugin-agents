<!--
Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
See LICENSE.txt for license information.
-->

# Use Mattermost Agents as your personal AI assistant

Ask an agent to search and post across Mattermost, act in Jira or GitHub through MCP servers, schedule posts, and hand off long-running work. It acts as you and asks before it changes anything.

## How an agent works

You message an agent in a direct message or the **Agents** pane. The agent is a Mattermost bot backed by the LLM your admin chose, which can be a self-hosted model. When a request needs more than the model knows, the agent calls tools:

- **Mattermost tools** to search, read, post, and manage channels. The [tool reference](mcp_tools.md) lists them all.
- **Remote MCP servers** for other systems such as an issue tracker or a calendar. You connect each one with your own account.
- **Plugin tools** from Mattermost plugins that register with Agents.
- **Built-in tools** to search the web (if your admin set it up), create files, ask you a multiple-choice question, and wait for long-running work.

Every Mattermost and MCP tool call runs as you, so the agent reaches only what you can reach, and external systems see your identity. Calls that need your approval wait for you to accept them. See [Use tools and approve actions](using_tools.md), which also covers agents that use a shared service account.

## Work across Mattermost

The built-in Mattermost tools cover most day-to-day actions:

- Search posts and files, and read posts, threads, and channels.
- Look up users, teams, groups, and channel members.
- Post to channels, reply in threads, send direct and group messages, and attach text files.
- Schedule posts, set post reminders, and manage drafts, pins, reactions, and bookmarks.
- Check unread counts, mentions, and followed threads.
- Create channels and add people to channels and teams.

If the agent says it can't post or change things, your workspace's plan doesn't include tools that change something. Ask your admin.

Example prompts:

- "DM Priya the notes from this thread, and remind me about the thread tomorrow at 9."
- "Schedule a post in ~release-announcements for Friday at 9:00 with the changelog."
- "Who is in ~platform-oncall, and which of them are online?"

## Reach other systems with MCP

Admins can connect remote MCP servers such as Jira, GitHub, or an internal service. You sign in to each system with your own account, so the agent has your access there and nothing more. If the agent can't reach a system you expect, ask your admin whether it's connected. To connect your account, see [Use tools and approve actions](using_tools.md#connect-your-account-to-a-tool-provider).

Example prompts:

- "Find the Jira ticket mentioned in this thread and summarize its status."
- "List my open pull requests that need review and message me the list."

## Long-running work

An agent can pause and wake itself after 1 to 30 minutes to check on work started through another tool, then report back. You don't need to prompt it again. Sending a new message in the thread cancels a pending wait.

## Prompt an agent well

- **Name the system and the target.** For an action that changes something, say where: "Create a Jira ticket in the PLAT project" works better than "make a ticket". If the target is missing, the agent asks before it acts.
- **Say what to do with the result.** "Post it in ~team-eng", "as a file", or "message it to me" tells the agent where the output goes.
- **Start a new chat for a new topic.** An agent knows only the current conversation, so a new topic works better in a new one.
- **Save what works.** Turn prompts you reuse into [custom prompts](custom_prompts.md).
