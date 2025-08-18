# Mattermost Agents User Guide

This guide explains how to use the AI features available through the Mattermost Agents plugin. This plugin transforms Mattermost into an AI-enhanced collaboration platform to improve team productivity and communication.

With Mattermost Agents, you can summarize call and meeting recordings, turn long threads and unread channel messages into concise summaries, stay on top of your messages by identifying next steps and decisions, extract learnings and transform content into charts and documentation, dig further into any topic by asking for insights, and leverage voice dictation tools for hands-free communication.

## Access AI features

You can access AI features in Mattermost in the following ways:

### Web and desktop

Access AI features through the right pane in one of the following ways:

- Select the **Agents** icon in the apps sidebar.
- @mention an AI bot in any channel where you have access (such as `@copilot`).
- Use the **AI Actions** menu by hovering over the first message in any conversation thread (requires a Mattermost Enterprise license)
- Use the **Ask AI** option in channels with unread messages (requires a Mattermost Enterprise license)

### Mobile

Start or open a direct message with the Agent bot. If your system admin has configured multiple bots, switch between them by starting or opening each bot by name.

## Conversational AI features

### Chat with agents

You can have conversations with Agents in several ways:

**Agents pane**: Use the Agents right-hand pane for a streamlined experience. Begin with suggested prompts, or engage in a private thread with an Agent for a tailored experience. If you have follow-up questions or need further insights, simply ask. You can also attach files for AI analysis or reference.

**Direct messages**: Start a direct message with an Agent bot to have a private conversation. Chat privately with an Agent in direct message threads like you would any other Mattermost user.

**Channel mentions**: [@mention](https://docs.mattermost.com/collaborate/mention-people.html) Agent bots by their username, such as `@copilot`, in any thread to bring Agents capabilities to your conversation. The bot responds in a thread to keep channels organized, and other team members can view and contribute to the conversation. An Agent can help extract information quickly or transform discussions into charts, resources, documentation, and more, and can find action items and open questions in new messages.

### Select a bot

If multiple Agent bots are configured for your Mattermost workspace, select your preferred bot in the Agents pane or @mention specific bots by name in channels.

### Approve tools

When Agents use external tools or integrations, you may be prompted to approve tool usage for security. When a tool is called, you'll see a card showing the tool name and description, arguments being passed to the tool, and **Approve/Reject** options.

For security, tool calls are only available in direct messages and each tool call requires explicit approval before execution. You can review tool arguments before approving, and tool results are shown after successful execution.

Available tools in direct messages include:

- Server search (semantic search across your Mattermost instance)
- User lookup (find information about Mattermost users)
- GitHub integration (the ability to fetch GitHub issues and pull requests requires the [GitHub plugin](https://docs.mattermost.com/integrate/github.html))
- [Jira integration](https://docs.mattermost.com/integrate/jira.html) (retrieve Jira issues from public instances)
- MCP tools (external tools provided by configured MCP servers if enabled). Tool availability depends on your user permissions and system configuration.

## Analyze threads and channels

### Summarize discussion threads

Summarizing a discussion thread requires a Mattermost Enterprise license.

To summarize a discussion thread:

1. Hover over the first message in any [conversation thread](https://docs.mattermost.com/collaborate/organize-conversations.html).
2. Select the **AI Actions** icon.
3. Select **Summarize Thread**. 

The thread summary is generated in the Agents pane, and only you can view the summary.

This is particularly useful for catching up on long discussions, creating meeting notes, and sharing outcomes with team members. You can also extract action items or find open questions in the same menu.

### Summarize unread channels

Summarizing unread Mattermost channels requires a Mattermost Enterprise license.

To summarize unread Mattermost channels:

1. Scroll to the **New Messages** cutoff line in a channel with unread messages.
2. Select **Ask AI**.
3. Select **Summarize new messages**. 

The channel summary is generated in the Agents pane, and only you can view the summary.

## Search with AI

Enterprise customers can enhance Mattermost [search](https://docs.mattermost.com/collaborate/search-for-messages.html) with AI capabilities. Semantic AI search requires a Mattermost Enterprise license, and AI search is an [experimental](https://docs.mattermost.com/manage/feature-labels.html#experimental) feature.

Open the Agents pane from the right sidebar and use natural language to search for content (such as "find discussions about the new product launch"). The AI will find semantically relevant results, even if they don't contain the exact keywords, and results respect your permissions so you'll only see content you have access to.

This feature accelerates decision-making and improves information flows by making it easier to find relevant content across threads, channels, and teams.

Contact your system admin if this feature isn't available for your Mattermost instance.

## Analyze images

For AI models with vision capabilities, attach an image file to your message when chatting with an Agent to ask questions about the image or request analysis. The Agent responds based on the visual content.

Image analysis is a [Beta](https://docs.mattermost.com/manage/feature-labels.html#beta) feature. Your system admin must enable vision capabilities for your bot, and the underlying AI model must support vision features.

## Record calls to summarize meetings

Mattermost Enterprise customers can leverage Mattermost Calls to turn meeting recordings into actionable summaries with a single action. Ensure key points of your calls and meetings are captured and shared easily, and share meeting insights with your team and the broader organization.

To summarize a Mattermost call recording:

1. [Start a call](https://docs.mattermost.com/collaborate/make-calls.html#start-a-call) in Mattermost and [record the call](https://docs.mattermost.com/collaborate/make-calls.html#record-a-call) during the meeting. 
2. Once the call ends and the call recording and transcription is ready, select the **Create meeting summary** option located directly above the call recording. 

The meeting summary is generated and shared as a direct message with the person who requested the meeting summary.

Both call recordings and recorded meeting summarization requires a Mattermost Enterprise license. Contact your system admin if these features aren't available for your Mattermost instance.
