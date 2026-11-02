# Control agents in channels

Three settings under **System Console > Plugins > Agents > AI Functions** decide how agents behave in channels: tool calling, ambient auto-replies, and native web search. Agents in channels need a Professional license, and auto-reply needs Enterprise Advanced.

## Settings

| Setting | Config key | Default | Effect |
|---|---|---|---|
| **Enable Channel Mention Tool Calling** (experimental) | `enableChannelMentionToolCalling` | Off | Lets agents use tools in any channel that isn't a direct message with the agent. When it's off, mentions work but the agent has no tools, and it tells users to use a direct message or the Agents pane. Approval rules are in [Tool approval and credentials](tool_approval.md#tools-in-channels). |
| **Enable ambient auto-replies** (experimental) | `enableAmbientAutoReply` | Off | Lets channel managers choose the two ambient auto-reply modes. Needs Enterprise Advanced to turn on. |
| **Allow native web search in channels** | `allowNativeWebSearchInChannels` | Off | Lets agents with native provider web search use it in channels, not only direct messages. Needs Professional. It works even when channel tool calling is off. See [Web search](web_search.md#native-web-search). |

## Auto-reply permissions

Auto-reply is a per-channel setting that channel managers configure on the channel's **Agents** tab. See [Use agents in channels](agents_in_channels.md#auto-reply).

- Writing the setting needs `manage_public_channel_properties` for public channels or `manage_private_channel_properties` for private channels. Channel members can read the current mode and agent, but only people with that permission see the ambient instructions.
- The setting isn't available in direct or group messages.
- The same setting is available through `GET` and `PUT /plugins/mattermost-ai/channel/{channelid}/autoreply`. The routes don't depend on the default agent, so they work even if the default agent is restricted or no agents exist. Writes check the selected agent's channel access.
- Turning auto-reply off never needs a license, so an existing setting is always clearable. Below Enterprise Advanced the tab is hidden and a stored setting is inactive.

## Ambient limits and cost

- Every analyzed message costs one LLM call on the selected agent's model.
- To bound cost, each server node runs at most 8 classifications at once, and each channel is limited to a burst of 6 messages, then about one every 5 seconds. Messages over a limit get no ambient reply. In a cluster, the limits apply per node.
- Turning **Enable ambient auto-replies** off stops ambient replies in every channel immediately and blocks saving ambient modes. Stored settings are kept and resume if you turn it back on.
- Before you downgrade the plugin to a version without ambient modes, switch every ambient channel to another mode or **Off**. Older versions treat an unknown mode as "reply to every message".

## Related

- [Access control](access_control.md): restrict an agent to certain channels.
- [Troubleshooting](troubleshooting.md#channels)
