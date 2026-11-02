# Summaries and AI search

Use an agent to summarize a thread, catch up on a channel, pull out action items, or search Mattermost in plain language. This page covers each entry point, what it needs, and how it behaves. Summaries need a Professional license or higher. AI search needs Enterprise.

In every case the agent works only with content you can already read. Results open in the Agents pane in a private conversation with the agent, and only you can see them.

## Summarize a thread

1. Hover over a message in the thread and select the **AI Actions** icon.
2. Choose the agent at the top of the menu if you have more than one.
3. Select **Summarize Thread**, **Find action items**, or **Find open questions**.

The same menu has **React for me**, which isn't a summary action.

## Summarize a channel

**Ask Agents about this channel** is a button in the channel header. Select it, choose an agent under **GENERATE WITH:**, and then pick one of:

| Option | What it does |
|---|---|
| **Summarize unreads** | Summarizes messages since you last viewed the channel. |
| **Summarize last 7 days** | Summarizes the last week. |
| **Summarize last 14 days** | Summarizes the last two weeks. |
| **Select date range to summarize** | Summarizes between a start and an end date you pick. |
| Text box | Asks your own question about the channel, for example "What decisions were made here?" or "List open action items with owners". |

To summarize only what's new, scroll to the **New Messages** line in a channel with unread messages, select **Ask AI**, and choose **Summarize new messages**, **Find action items**, or **Find open questions**.

How channel summaries read the channel:

- **Ask Agents about this channel** reads the channel with the built-in `read_channel` and `get_channel_info` tools, limited to the channel you're in. This works even if the selected agent has a narrower tool list. If those tools are unavailable to you, the request fails with an error about embedded MCP tools. Ask your admin to check the built-in Mattermost MCP server.
- **Ask AI** at the **New Messages** line and `/summarize-channel` read the channel's posts directly and don't use tools.

### Summarize from the message box

Use the `/summarize-channel` slash command:

```text
/summarize-channel --period 3d --bot @assistant
```

- `--period` accepts hours (`5h`), days (`3d`), `1w`, or `2w`. Anything else falls back to 24 hours. The default is 24 hours.
- `--bot` names the agent. Without it, Mattermost uses the workspace's default agent, not the agent you selected in a picker.

## Search with AI

AI search is an experimental feature. The agent runs several searches and reads the surrounding threads before it answers.

1. Select the Mattermost search bar and choose the **Agents** button. If you have several agents, pick one under **SEARCH WITH**.
2. Ask a question or enter search terms, for example "what did we decide about the new product launch?"
3. The agent answers in the Agents pane and cites the posts it used with links. Reply in the thread to ask a follow-up.

To ask about the channel you're viewing, type `/ask-channel` followed by your question. The command always uses the workspace's default agent. The agent searches that channel first and then, if the answer isn't there, other content you can access. When the answer comes from another channel, it tells you which one.

Things to know:

- Searches run as you. The agent can't find content you can't access.
- By default the agent uses keyword search. If your admin set up [semantic search](embedding_search.md), it also finds related content that doesn't contain your exact words.
- The agent needs access to the built-in Mattermost tools. If it says it can't search, ask your admin.
- If no relevant posts turn up, the agent says so instead of answering from general knowledge.

## Availability

| Feature | Minimum license |
|---|---|
| Summarize Thread, Find action items, Find open questions | Professional |
| Ask Agents about this channel, Ask AI at the **New Messages** line, `/summarize-channel` | Professional |
| Search with AI, `/ask-channel` | Enterprise |
| React for me | Any |

If a button is missing, your license level or an agent restriction is the usual cause. Ask your system admin.
