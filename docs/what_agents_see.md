# What agents can see and keep

An agent reads the conversation it's in plus what its tools return, and it normally acts with your permissions. An agent set up with a shared service account uses that account for external tools. Conversations with an agent are stored in the Mattermost database. Your System Admin chose the AI model, so ask your admin whether it's self-hosted or run by a vendor.

## What an agent reads

What goes to the AI model depends on where you ask:

| Where you ask | What the agent reads |
|---|---|
| Direct message or Agents pane | The messages and attachments in that conversation. |
| @mention in a channel | The message that mentions it, or in a thread, the thread up to the mention. Later replies aren't included. |
| Summarize a thread or channel, find action items | The posts you chose, with their authors. |
| Meeting summary | The meeting transcript. |
| Search with AI | Your question, plus the posts the agent opens while it searches. |

Chat requests also tell the model who you are: your username, and your name, position and locale if set, and that you're a System Admin if you are. It doesn't include your email address.

## What an agent can reach

Every Mattermost and MCP tool call runs as you. The agent can read the channels, teams, and files you can, and can use a connected system such as Jira only after you connect your account. It can't see private channels you aren't in, and other systems see your identity, not a shared one. The exception is an agent your admin set up with a shared service account, which uses that account's access for external tools.

Anything a tool returns goes to the AI model so it can continue. In a channel, you choose whether to share a tool's result with the channel or keep it private. A private result isn't used in the channel reply.

## What is stored and who can see it

- **Conversations** are stored in Mattermost's database with their tool calls and results. A conversation is hidden when its root post is deleted, but it stays in the database.
- **Channel replies** are ordinary posts, so everyone in the channel can read them.
- **Direct messages and the Agents pane** are private to you.
- **Admins** can see audit records of who did what and, if enabled, token usage counts. Neither holds message content.
- **The AI provider** receives the content of your requests. With a cloud model, its terms apply. With a self-hosted model, the content stays in your organization.

An agent doesn't remember other conversations. It knows only the current one and whatever it reads with its tools.

Admins can read the full details in [Data handling](data_handling.md).
