# Use Mattermost Agents

A Mattermost agent searches, reads, and posts in Mattermost, and works in systems like Jira and GitHub through MCP servers. It acts as you, with your permissions, and asks before it changes anything it isn't set to do automatically.

An agent is a bot backed by a large language model (LLM) that your System Admin chose, which can be a self-hosted model. Mattermost Agents was formerly called Mattermost Copilot. This page is for people who work with agents. To set up Agents, see the [Admin guide](admin_guide.md).

## What an agent can do

You message an agent in a direct message or the Agents pane. When a request needs more than the model knows, the agent calls tools in Mattermost and in connected systems such as Jira or GitHub. Example prompts:

- "Search for what we decided about the March launch and link the posts."
- "What's assigned to me in Jira and due this week? Post the list in my team channel."
- "Open a GitHub issue for the bug in this thread and reply with the link."
- "Summarize what I missed in ~incident-room since yesterday, then post a two-line status in ~leadership."

Every tool call runs as you, and the agent asks before it changes anything your admin hasn't set to run automatically. See [Use Agents as your personal AI assistant](personal_agent.md) for what agents can do and how to prompt them, and [Use tools and approve actions](using_tools.md) for approvals.

## What you can do

| I want to | Go to |
|---|---|
| See everything an agent can do for me, and how to prompt it | [Use Agents as your personal AI assistant](personal_agent.md) |
| Approve what an agent does, or connect my account to a tool | [Use tools and approve actions](using_tools.md) |
| Bring an agent into a channel or thread | [Use agents in channels](agents_in_channels.md) |
| Summarize a thread, channel, or unread messages, or search with AI | [Summaries and AI search](summaries_and_search.md) |
| Summarize a Calls recording or Zoom meeting | [Meeting summaries](meeting_summaries.md) |
| Save instructions I reuse | [Skills](skills.md) |
| Use Mattermost from Claude, Cursor, or VS Code | [Mattermost MCP server](mcp_server.md) |
| Know what an agent can see and keep | [What agents can see and keep](what_agents_see.md) |

Some features depend on your Mattermost license level. If a feature described here is missing, ask your System Admin.

## Start a conversation

| Where | How | Notes |
|---|---|---|
| Direct message | Open a direct message with an agent's bot account. | Private. Tools can run here by default. |
| Agents pane | Select the **Agents** icon in the apps bar to open the right-hand pane, then start a new chat. | Private. Has file attachments and the **Tools** menu. |
| Channel or thread | @mention the agent by its username, for example `@assistant`. | Visible to everyone in the channel. See [Use agents in channels](agents_in_channels.md). |
| Mobile | Open a direct message with the agent. | Mobile can't start MCP OAuth. Use web or desktop to connect tools that need your account. |

To see which agents exist, select the product switcher (grid icon) and choose **Agents**. The page lists every agent you can use. Agents you can't edit are marked **Read only**.

If your workspace has several agents, pick one from any agent picker, such as the Agents pane, the **Skills** menu, or the search bar. Your choice is saved to your account and applies on web, desktop, and mobile. Until you choose, Mattermost uses the default agent your admin set. You can also @mention a specific agent.

An agent doesn't remember other conversations. It knows only the current conversation and whatever it reads with its tools.

## Create your own agent

If your admin allows it, you can create agents of your own, such as a release-notes writer or a support triager. If you don't see **Create agent** on the **Agents** page, ask your admin.

1. Select the product switcher (grid icon), choose **Agents**, then select **Create agent**.
2. On **Configuration**, set a **Display name**, an **Agent username** (the `@mention` handle), and **Custom instructions** that describe its role and tone. Pick the **AI Service** your admin set up.
3. On **Access**, choose which channels and users can use the agent, and add **Agent admins** who can edit it with you.
4. On **MCPs**, choose which tools it can use. By default it can use every tool you can.
5. Select **Save**. Mention the agent or message it to try it.

Every save creates a version. Open the version history and select **Restore this version** to roll back. You can edit or delete the agents you created, and the **Agents** page marks agents you can't edit as **Read only**.

## Attach files and images

You can attach files in the Agents pane, in direct messages, and when you @mention an agent in a channel.

- Images are used only when your admin turned on vision for that agent and the model supports it. Images larger than the provider's maximum dimensions are left out, and the agent tells you so.
- Other files contribute text only when Mattermost can read text from them, such as plain-text files or documents with extracted text. If the agent can't read a PDF or Office file, ask your admin to check document text extraction. Very large text may be shortened. If one attachment can't be read, the agent skips it and continues.
- In a thread, a later @mention can include images attached to earlier posts in that thread.

## Ask for output as a file

Ask the agent to put long output in a file, for example "the meeting notes as a Markdown file" or "a Python script that parses this log, as a file". The agent attaches the file to its reply.

- Files are text only. The file extension sets the type (`.md`, `.csv`, `.py`, `.html`).
- An agent can attach up to 10 files to one reply. Each file is limited by the server's maximum file size.
- In direct messages this is always available. In channels it needs the same admin setting as tools.
- When an agent posts for you with `create_post`, `dm`, or `group_message`, it can attach text files to that post too.
- If your admin enabled HTML artifacts, an `.html` file the agent creates renders as an interactive page in the reply after you select **Run**.

## What an agent sees and keeps

An agent reads the conversation it's in and whatever its tools return, all with your permissions. Conversations are stored in Mattermost's database. With a cloud model the provider receives the content, and with a self-hosted model it stays in your organization. See [What agents can see and keep](what_agents_see.md).

## Links in AI responses

By default, an agent's Markdown links to other sites appear as plain text with the address shown, so you can read the destination but can't click it. Citations from web search stay clickable. Your admin can allow clickable links, optionally limited to specific domains. A model can be tricked into pointing a link at a harmful site, so check where a link goes before you open it.
