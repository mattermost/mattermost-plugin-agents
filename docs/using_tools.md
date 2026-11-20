# Use tools and approve actions

Approve or reject an agent's tool calls, share or hide results in channels, and connect your accounts to MCP tool providers. Your admin decides which tools exist and which need your approval.

## Where tools run

- **Direct messages and the Agents pane:** tools are available by default.
- **Channels:** tools are off unless your admin turned on **Enable Channel Mention Tool Calling**. When it's off and you ask for something that needs a tool, the agent tells you to use a direct message or the Agents pane.
- **Which tools:** Mattermost tools, tools from remote MCP servers your admin connected, and tools from Mattermost plugins that register with Agents. Availability also depends on your permissions and on whether your account is connected to each provider.

Mattermost tools run as you. External MCP tools use your OAuth token, or the server's configured headers for servers without OAuth, unless the agent uses a shared service account.

## Approve or reject a tool call

When a tool needs your approval, the agent's reply shows a card with the tool name, its arguments, and **Accept** and **Reject** buttons. Nothing runs until you choose.

- If several tools wait at once, use **Accept all** or **Reject all**, or decide each one.
- A reply can mix tools that ran automatically and tools that wait. Automatic ones show an **Auto-approved** badge. The others keep their buttons.
- Only the person who started the conversation can approve. Other people in a channel see the tool name but not its arguments, and they can't approve.
- An unanswered call stays pending. It never approves itself.

## Share or keep private (channels only)

In a channel, you choose what happens to a tool result before the agent writes from it:

- **Share:** the agent uses the result in its reply, and everyone in the channel sees the tool's arguments and result.
- **Keep private:** the result stays with you. The agent writes its channel reply without that content.

You get this step after every tool call you approve. A tool your admin set to run automatically everywhere runs without asking, and you get the step only when Mattermost can't confirm that everyone in the channel can read what the tool returned. For example, the tool read a private channel, or it came from another system. The agent pauses until you decide. It shows you which channels the results came from, when it knows, and makes **Keep private** the default. Others in the channel see that the agent is waiting for you, but not the results or the channel names. After you decide, the agent continues and can use more tools.

You can't take back a **Share**.

In a direct message or the Agents pane there's no second step, because only you can see the conversation.

## Connect your account to a tool provider

Some MCP servers need you to sign in with your own account before their tools appear.

**From the conversation.** When you ask for something that needs a provider you haven't connected, the agent shows a **Connect** card that explains why. Select **Connect** and sign in in the window that opens. The agent continues automatically when you're done. Select **Skip** to continue without it. If you close the window early, select **Connect** again. If your session with a provider expires partway through a conversation, the agent shows the card again.

**From the Tools menu.** In the Agents pane, start a new chat and open **Tools**. The menu lists the providers the selected agent can use.

- Select **Connect** or **Disconnect** next to a provider that uses OAuth.
- Turn providers on or off for yourself. Your choices are saved to your account.
- Select **Refresh tool providers** to rediscover tools after an admin changes providers or you connect one.

Use web or desktop to connect. The mobile apps can't start MCP OAuth.

## Agents that use a service account

For an agent your admin set to use a shared service account, you're never asked to connect and the **Tools** menu isn't shown. External tools act as the shared account, not as you, so ask your admin what the agent can reach.

## When a tool fails

The agent can recover from a failed call, for example by trying another approach or explaining the failure. After three failed tool calls in a row, it stops calling tools, describes the latest error, and asks you for what's missing. A successful call resets the count. If failures repeat, check that you've connected the provider and that you can access the thing the tool targets.

After a set number of tool rounds (30 unless your admin changed it), the agent stops calling tools and answers with what it has.

## Ask the agent a question back

When the agent needs a decision before it continues, it asks a multiple-choice question in the conversation. Select an option, type your own answer under **Something else…**, or select **Skip**.
