# Use agents in channels

You can bring an agent into a channel or thread by @mentioning it, and a channel manager can set an agent to reply on its own. This page covers how agents behave when other people can see the conversation: mentions, context, tools, and auto-reply. Agents in channels need a Professional license or higher. Auto-reply needs Enterprise Advanced.

## @mention an agent

Mention an agent by its username, for example `@assistant`, in a channel or thread. The agent replies in a thread, so the channel stays organized. Everyone in the channel can read the reply and join the thread.

- **Context.** On a mention in a thread, the agent reads the thread up to your mention, with each author's username. Replies posted after your mention, and temporary placeholder posts, aren't included. On a mention outside a thread, it reads only the message that mentions it. In both cases it also gets the channel and team names.
- **Follow-ups need a mention.** Replies in the thread to the agent need an @mention too. If you reply to an agent's post without one, Mattermost shows you a private reminder with a link to loop the agent in. Select it to send your reply to the agent without editing the post.
- **Who can use it.** An agent can be limited to certain users, teams, or channels. If you can't mention an agent, an admin may have restricted it. See [Control who can use agents](access_control.md).
- **Direct messages are different.** In a direct message with an agent you don't need to mention it. If you mention a different agent in that direct message, that agent answers.
- **What triggers an agent.** Posts from webhooks, system messages, and other bots don't trigger agents. A bot or integration can trigger one only by setting the `activate_ai` post property.

## Tools in channels

Tools are off in channels unless your admin turned on **Enable Channel Mention Tool Calling**. When they're on, only the person who mentioned the agent can approve its tool calls, and that person decides whether tool results are shared with the channel. See [Use tools and approve actions](using_tools.md).

If tools are off and you ask for something that needs one, the agent tells you to use a direct message or the Agents pane. Native web search in channels is a separate admin setting, **Allow native web search in channels**.

## Auto-reply

Auto-reply makes an agent respond in a channel without being mentioned. A channel manager sets it up.

1. Select the channel name, then **Channel Settings**.
2. Open the **Agents** tab.
3. Choose an **Auto-reply mode** and an **Auto-replying agent**, then select **Save**.

You need permission to manage the channel's properties: `manage_public_channel_properties` for public channels, `manage_private_channel_properties` for private channels. The **Agents** tab appears only in public and private channels, only to people with that permission, and only when at least one agent is available in the channel. Channel members without the permission can't see the instructions. The same setting is available through `GET` and `PUT /plugins/mattermost-ai/channel/{channelid}/autoreply`.

| Mode | Behavior |
|---|---|
| **Off** | Default. The agent replies only when mentioned or in direct messages. |
| **Top-level posts only** | The agent replies to each new top-level post and starts a thread. Later replies in that thread need a mention or the loop-in link. |
| **Threads too** | As above, and the agent also replies to every non-bot reply in the channel's threads. |
| **Ambient (Experimental)** | The agent reads each new message and replies only if a separate classification step decides a reply is useful. |
| **Ambient in mentioned threads (Experimental)** | Like Ambient, but only in threads the agent already replied in. Mention the agent to bring it into a thread. |

An automatic reply behaves as if the post's author had mentioned the agent. It uses the same agent restrictions and tool rules, and runs with the author's permissions. It never replies to webhooks or system messages, or to other bots unless the post sets `activate_ai`.

### Ambient modes

- Ambient modes need an admin to turn on **Enable ambient auto-replies**. When it's off, ambient modes can't be saved, and channels already using them stop replying. Stored settings are kept and resume if the admin turns the setting back on.
- **Ambient instructions** tell the agent when to reply. They're optional, up to 8,000 characters, and shared by both ambient modes.
- The classification step uses the selected agent's model. If it fails or answers no, the agent stays silent. When the agent does reply, that reply uses the agent's model, tools, and the author's permissions.
- Every analyzed message costs one LLM call. To bound cost, each server node runs at most 8 classifications at once, and each channel is limited to a burst of 6 messages, then about one every 5 seconds. Messages over the limit get no ambient reply.
- Before downgrading the plugin to a version without ambient modes, switch every ambient channel to another mode or **Off**. Older versions treat an unknown mode as "reply to every message".

## Related

- [Tool approval and credentials](tool_approval.md): the rules admins configure for channels.
- [Data handling](data_handling.md): what a mention sends to the LLM.
