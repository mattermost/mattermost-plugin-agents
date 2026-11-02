# Use Mattermost Agents

Mattermost Agents adds AI agents to Mattermost. An agent is a bot backed by a large language model (LLM) that your system admin chose. You can ask it questions, have it summarize and search, and let it act for you in Mattermost and in other systems through tools and MCP servers. Agents act with your permissions, and you approve actions that change something. Mattermost Agents was formerly called Mattermost Copilot.

This page is for people who chat with agents. To set up Agents, see the [Admin guide](admin_guide.md).

## What you can do

| I want to | Go to |
|---|---|
| Have an agent search, read, and post in Mattermost and other systems for me | [Use Agents as your personal AI assistant](personal_agent.md) |
| Approve what an agent does, or connect my account to a tool | [Use tools and approve actions](using_tools.md) |
| Bring an agent into a channel or thread | [Use agents in channels](agents_in_channels.md) |
| Summarize a thread, channel, or unread messages, or search with AI | [Summaries and AI search](summaries_and_search.md) |
| Summarize a Calls recording or Zoom meeting | [Meeting summaries](meeting_summaries.md) |
| Save a prompt I reuse | [Custom prompts](custom_prompts.md) |
| Know what data an agent sends to the LLM | [Data handling](data_handling.md) |

Some features depend on your Mattermost license level. If a feature described here is missing, ask your system admin. The full list is in [License requirements](admin_guide.md#license-requirements).

## Start a conversation

| Where | How | Notes |
|---|---|---|
| Direct message | Open a direct message with an agent's bot account. | Private. Tools can run here by default. |
| Agents pane | Select the **Agents** icon in the apps bar to open the right-hand pane, then start a new chat. | Private. Has suggested prompts, pinned custom prompts, file attachments, and the **Tools** menu. |
| Channel or thread | @mention the agent by its username, for example `@assistant`. | Visible to everyone in the channel. Needs a Professional license or higher. See [Use agents in channels](agents_in_channels.md). |
| Mobile | Open a direct message with the agent. | Mobile can't start MCP OAuth. Use web or desktop to connect tools that need your account. |

To see which agents exist on your server, select the product switcher (grid icon) and choose **Agents**. The page lists every agent you can use. Agents you can't edit are marked **Read only**. People with permission to manage agents can create and edit them there. See [Create and manage agents](managing_agents.md).

## Choose an agent

If your workspace has several agents, pick one from any agent picker, such as the Agents pane, the **Custom prompts** menu, or the search bar. Your choice is saved to your account and applies on web, desktop, and mobile. Until you choose, Mattermost uses the default agent your admin set. You can also @mention a specific agent by name.

An agent doesn't remember other conversations. It knows only the current conversation, plus whatever it reads with its tools.

## Attach files and images

You can attach files in the Agents pane, in direct messages, and when you @mention an agent in a channel.

- Images are used only when your admin turned on vision for that agent and the model supports it. Images larger than the provider's maximum dimensions are left out, and the agent tells you so.
- Other files contribute text only when Mattermost can read text from them, such as plain-text files or documents with extracted text. Text extraction for PDF and Office documents depends on the server setting **Enable document search by content** (`FileSettings.ExtractContent`, on by default) under **System Console > Environment > File Storage**. Very large text may be shortened. If one attachment can't be read, the agent skips it and continues.
- In a thread, a later @mention can include images attached to earlier posts in that thread.

## Ask for output as a file

Ask the agent to put long output in a file, for example "the meeting notes as a Markdown file" or "a Python script that parses this log, as a file". The agent attaches the file to its reply.

- Files are text only. The file extension sets the type (`.md`, `.csv`, `.py`, `.html`).
- An agent can attach up to 10 files to one reply. Each file is limited by the server's maximum file size.
- In direct messages this is always available. In channels it follows the admin setting **Enable Channel Mention Tool Calling**.
- When an agent posts for you with `create_post`, `dm`, or `group_message`, it can attach text files to that post too.
- If your admin enabled HTML artifacts, an `.html` file the agent creates renders as an interactive page in the reply after you select **Run**. See [Secure AI-generated links and HTML](ai_output_security.md#html-artifacts).

## Actions on a message

Hover over a message and select the **AI Actions** icon.

| Action | What it does | Minimum license |
|---|---|---|
| **Summarize Thread** | Summarizes the thread in the Agents pane. Only you see it. | Professional |
| **Find action items** | Lists action items in the thread. | Professional |
| **Find open questions** | Lists unanswered questions in the thread. | Professional |
| **React for me** | Picks an emoji that fits the message and adds the reaction as you. | Any |

The menu has an agent selector at the top. See [Summaries and AI search](summaries_and_search.md) for the details.

## Voice dictation

Agents has no built-in dictation. Use your operating system's: **Windows key + H** on Windows, or **System Settings > Keyboard > Dictation** on macOS. Linux needs a separate speech recognition tool.

## Links in AI responses

By default, an agent's Markdown links to other sites appear as plain text with the address shown, so you can read the destination but can't click it. Citations from web search stay clickable. Your admin can allow clickable links, optionally limited to specific domains. A model can be tricked into pointing a link at a harmful site, so check where a link goes before you open it.
