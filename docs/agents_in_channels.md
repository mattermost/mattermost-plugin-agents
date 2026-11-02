# Use agents in channels

@mention an agent in a channel or thread, or have a channel manager set it to auto-reply. The agent reads the thread, replies in a thread everyone can see, and only the person who asked can approve its tool calls. Channels need a Professional license or higher, and auto-reply needs Enterprise Advanced.

## @mention an agent

Mention an agent by its username, for example `@assistant`, in a channel or thread. The agent replies in a thread, so the channel stays organized. Everyone in the channel can read the reply and join the thread.

- **Context.** On a mention in a thread, the agent reads the thread up to your mention, with each author's username. Replies posted after your mention, and temporary placeholder posts, aren't included. On a mention outside a thread, it reads only the mentioning message. In both cases it also gets the channel and team names.
- **Follow-ups need a mention.** If you reply to an agent's post without an @mention, Mattermost shows you a private reminder with a link to loop the agent in. Select it to send your reply to the agent without editing the post.
- **Restrictions.** An agent can be limited to certain users, teams, or channels. If you can't mention an agent, an admin may have restricted it. See [Control who can use agents](access_control.md).
- **Direct messages are different.** In a direct message with an agent you don't need to mention it. If you mention a different agent there, that agent answers.
- **What triggers an agent.** Posts from webhooks and system messages never trigger agents. A bot account or a plugin can trigger one by setting the `activate_ai` post property.

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

## Related

- [Control agents in channels](channel_settings.md) for admins.
- [Data handling](data_handling.md): what a mention sends to the LLM.
