# Use agents in channels

@mention an agent in a channel or thread, or have a channel manager set it to auto-reply. The agent reads the thread and replies in a thread everyone can see. Channels need a Professional license or higher, and auto-reply needs Enterprise Advanced. Only the person who asked can approve tool calls.

## @mention an agent

Mention an agent by its username, for example `@assistant`, in a channel or thread. The agent replies in a thread, so the channel stays organized. Everyone in the channel can read the reply and join the thread.

- **Context.** On a mention in a thread, the agent reads the thread up to your mention, with each author's username. Replies posted after your mention, and temporary placeholder posts, aren't included. On a mention outside a thread, it reads only the mentioning message. In both cases it also gets the channel and team names.
- **Follow-ups need a mention.** If you reply to an agent's post without an @mention, Mattermost shows you a private reminder with a link to loop the agent in. Select it to send your reply to the agent without editing the post.
- **Restrictions.** An agent can be limited to certain users, teams, or channels. If you can't mention an agent, an admin may have restricted it, so ask your admin.
- **Direct messages are different.** In a direct message with an agent you don't need to mention it. If you mention a different agent there, that agent answers.
- **Group messages** behave like channels: mentions need Professional, and tools follow the channel setting.

## Tools in channels

Tools are off in channels unless your admin turned them on. When they're on, only the person who mentioned the agent can approve its tool calls and decide whether results are shared with the channel. See [Use tools and approve actions](using_tools.md).

## Auto-reply

Auto-reply makes an agent respond in a channel without being mentioned. Channel managers set it up:

1. Select the channel name, then **Channel Settings**.
2. Open the **Agents** tab.
3. Choose an **Auto-reply mode** and an **Auto-replying agent**, then select **Save**.

The **Agents** tab appears only in public and private channels, only for people who can manage the channel's properties, and only when at least one agent is available in the channel.

| Mode | Behavior |
|---|---|
| **Off** | Default. The agent replies only when mentioned or in direct messages. |
| **Top-level posts only** | The agent replies to each new top-level post and starts a thread. Later replies in that thread need a mention or the loop-in link. |
| **Threads too** | As above, and the agent also replies to every non-bot reply in the channel's threads. |
| **Ambient (Experimental)** | The agent reads each new message and replies only if a separate classification step decides a reply is useful. |
| **Ambient in mentioned threads (Experimental)** | Like Ambient, but only in threads the agent already replied in. Mention the agent to bring it into a thread. |

An automatic reply behaves as if the post's author had mentioned the agent. It uses the same agent restrictions and tool rules, and runs with the author's permissions. It never replies to webhooks or system messages, or to other bots unless the post sets `activate_ai`.

### Ambient modes

- Ambient modes need an admin to turn them on. Busy channels can get no ambient reply to some messages.
- **Ambient instructions** tell the agent when to reply. They're optional, up to 8,000 characters, and shared by both ambient modes.
- The classification step uses the selected agent's model. If it fails or answers no, the agent stays silent. When the agent does reply, that reply uses the agent's model, tools, and the author's permissions.

## Channel instructions and agent context

Channel members who can manage channel settings can give agents background about a channel, such as what it's for, team conventions, or where things are tracked. Whenever an agent works in the channel (a mention, an automatic reply, or a thread or channel summary), it receives this context, much like a coding assistant reads a repository's `AGENTS.md` file. Agents are told the context is optional and may not be relevant, so they use it only when it helps. It never overrides the agent's own instructions set by your system admin.

There are two kinds of channel context:

- **Channel instructions**: free-form text, up to 8,000 characters. Open **Channel Settings**, select the **Agents** tab, enter text under **Channel instructions**, and select **Save**. Clear the text and save to remove it.
- **Posts pinned to agent context**: posts whose content agents receive with every request. Hover over a post, open the **AI Actions** menu, and select **Pin to agent context**. Select **Unpin from agent context** in the same menu, or remove it from the **Pinned agent context** list in the **Agents** tab. Pinned posts show an **Agent context** label next to their timestamp. A channel can pin up to 10 posts, and agents receive at most the first 4,000 characters of each.

Pinning to agent context is separate from [pinning a message](https://docs.mattermost.com/collaborate/pin-messages.html) to the channel: agent context pins don't appear in the channel's pinned messages, and regular pinned messages aren't sent to agents. Edits to a pinned post are picked up automatically, and deleting the post removes it from agent context.

Channel context is captured when a conversation starts, so changes apply to new threads and mentions, not to follow-up replies in an existing conversation. It's available in public and private channels (not direct or group messages) at Enterprise Advanced. Below Enterprise Advanced the context isn't sent to agents, but you can still clear instructions and unpin posts. Admins and integrators: see [Channel instructions and pinned context](channel_settings.md#channel-instructions-and-pinned-context).

## Related

- [Control agents in channels](channel_settings.md) for admins.
- [What agents can see and keep](what_agents_see.md): what a mention sends to the LLM.
