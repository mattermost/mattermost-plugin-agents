# Use Mattermost Agents as your personal AI assistant

Ask an agent to search and post across Mattermost, act in Jira or GitHub through MCP servers, schedule posts, and hand off long-running work. For starting conversations, creating agents, and attaching files, see [Use Mattermost Agents](user_guide.md). For tool approvals and account connections, see [Use tools and approve actions](using_tools.md).

## Work across Mattermost

Built-in Mattermost tools cover day-to-day actions across channels, threads, and direct messages (see the [tool reference](mcp_tools.md) for the full list):

- Search posts and files, and read posts, threads, and channels.
- Look up users, teams, groups, and channel members.
- Post to channels, reply in threads, send direct and group messages, and attach text files.
- Schedule posts, set post reminders, and manage drafts, pins, reactions, and bookmarks.
- Check unread counts, mentions, and followed threads.
- Create channels and add people to channels and teams.

If the agent says it can't post or change things, ask your admin. Possible causes are your workspace plan, the tools granted to the agent, your permissions, or an access policy.

Example prompts:

- "DM Priya the notes from this thread, and remind me about the thread tomorrow at 9."
- "Schedule a post in ~release-announcements for Friday at 9:00 with the changelog."
- "Who is in ~platform-oncall, and which of them are online?"

## Reach other systems with MCP

Admins can connect remote MCP servers such as Jira, GitHub, or an internal service. You sign in to each system with your own account, so the agent has your access there and nothing more. An agent set up with a shared service account uses that account's access instead. If the agent can't reach a system you expect, ask your admin whether it's connected. To connect your account, see [Use tools and approve actions](using_tools.md#connect-your-account-to-a-tool-provider).

Example prompts:

- "Find the Jira ticket mentioned in this thread and summarize its status."
- "List my open pull requests that need review and message me the list."

## Long-running work

An agent can pause and wake itself after 1 to 30 minutes to check on work started through another tool, then report back. You don't need to prompt it again. Sending a new message in the thread cancels a pending wait.

## Prompt an agent well

- **Name the system and the target.** For an action that changes something, say where: "Create a Jira ticket in the PLAT project" works better than "make a ticket". If the target is missing, the agent asks before it acts.
- **Say what to do with the result.** "Post it in ~team-eng", "as a file", or "message it to me" tells the agent where the output goes.
- **Start a new chat for a new topic.** An agent knows only the current conversation, so a new topic works better in a new one.
- **Save what works.** Turn prompts you reuse into [skills](skills.md).
