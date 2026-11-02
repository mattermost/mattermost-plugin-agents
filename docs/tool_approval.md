# Tool approval and credentials

Only the person who started a conversation can approve an agent's tool calls, and tools run with that person's credentials. This page is for admins and security reviewers. To see what to click, read [Use tools and approve actions](using_tools.md).

## Roles

The **initiator** is the person who mentioned the agent or wrote in a direct message or Agents pane thread. Only the initiator can approve, and the initiator is the **executor**: tools run as them, unless the agent uses a service account. Anyone else who can read the channel is an **observer**. Observers can't approve, and by default can't see tool arguments or private results. Roles don't change mid-conversation.

## Whose credentials run a tool

An agent runs tools in one of two modes. The mode is a setting on the agent, not a per-call choice.

**Per-user authentication (default).** Tools run as the initiator.

- Mattermost tools use the initiator's user record, channel memberships, and team memberships. They see only what the initiator can see.
- OAuth-backed MCP servers use the initiator's own token. Two people running the same tool can get different results, because they have different scopes. A call can succeed for one and fail with a 401 or 403 for the other. A 401 from an MCP server prompts the initiator to authorize again.
- The agent's bot account never runs third-party tools. It only posts the reply and tool cards.
- For an MCP server without OAuth, the server's own configured credential decides what the call can do. The initiator rule still decides whether the call happens.

**Service account authentication.** External MCP servers get shared credentials, while Mattermost and plugin tools still run as the initiator. The initiator still approves each call. See [Service account authentication](mcp_integrations.md#service-account-authentication).

## Approval policies

Admins set a policy for each tool on the **Tools** tab of the MCP settings. The agent's tool grants decide what the agent may use. They never change the policy.

| Policy (System Console label) | Stored value | Behavior |
|---|---|---|
| **Ask Every Time** | `ask` | The initiator must accept every call, in direct messages and channels. |
| **Auto Run (DM)** | `auto_run_in_dm` | Runs without a prompt in a direct message with the agent. In channels it asks. |
| **Auto Run (Everywhere)** | `auto_run_everywhere` | Runs without a prompt in direct messages and channels. |

A tool with no stored setting is enabled and set to `ask`. Per call, the server decides like this:

| Conversation | Policy `ask` | Policy `auto_run_in_dm` | Policy `auto_run_everywhere` |
|---|---|---|---|
| Direct message | Ask | Run | Run |
| Channel | Ask | Ask | Run |

Some tools have no policy:

- `search_tools` and `load_tool`, the dynamic loading helpers, always run without approval.
- The built-in `CreateFile` tool always runs without approval, because it only attaches a file to the agent's own reply. `wait_for_async_work`, which schedules the agent's own follow-up, does too.
- `AskUserQuestion` always waits for the user's answer.
- The built-in web search tool (Brave, Google, or SearXNG) always asks. Native provider web search runs inside the provider, so it has no approval card.

### Choose a policy

- Use **Ask Every Time** for any tool that writes data, sends notifications, costs money, or returns something the initiator shouldn't see without consent. It's the safe default.
- Use **Auto Run (DM)** for read-only tools whose results are fine in a private conversation but might leak context in a shared channel. Examples: search the initiator's own mail, list their calendar. Mattermost seeds its own read-only tools at this level.
- Use **Auto Run (Everywhere)** only for read-only tools whose results are safe for every channel member, and that enforce per-user permissions themselves. Tools that call external APIs rarely qualify. Results of these tools are shared with the channel at once, with no per-call privacy choice.
- A policy controls the prompt. It isn't a substitute for credential scope. Limit scopes in the OAuth provider, and give service accounts the least access they need.

### Policies below Enterprise

Setting a policy that runs tools automatically needs an Enterprise license. Below Enterprise, Mattermost's own tools use the product defaults described in [Connect agents to MCP servers](mcp_integrations.md#enable-and-approve-tools), and every other tool asks. A stricter policy you stored earlier still applies, and a disabled tool stays disabled. Remote MCP tools don't run at all below Enterprise.

## Tools in channels

In a direct message or the Agents pane, tools are on. In a channel, tools are off unless an admin turns on **Enable Channel Mention Tool Calling**, which is experimental and off by default. When it's on, the rules on this page apply in any channel that isn't a direct message with the agent. The setting and its limits are in [Control agents in channels](channel_settings.md).

## Approval rules

- Only the initiator can accept or reject a pending call. The server checks that the clicking user is the conversation's recorded initiator and rejects any other request before a tool runs. Admins aren't implicit approvers. Admin authority is in the policy, not in the call.
- A pending call that nobody answers stays pending. It never approves itself or moves to someone else, and nothing runs. Starting a new conversation creates a new initiator.
- A reply can mix automatic and pending tools. Automatic ones show an **Auto-approved** badge. Pending ones keep **Accept** and **Reject**. When several wait, **Accept all** and **Reject all** follow the same initiator-only rule.
- There's no mode where any channel member can approve, no channel admin override, and no silent fallback to the bot's identity or to a shared credential when a user's token is missing or rejected. Another member approving would commit the initiator's credentials, or the service account, without the initiator's consent.

## Share or keep private

In a channel, a call that ran after approval has a second step, because its result might not suit everyone. When the tool returns, the initiator chooses:

- **Share:** other members see the arguments and the result, and the agent's follow-up uses the result openly.
- **Keep private:** the result stays on the conversation for the agent to use privately. Other members don't see the arguments or result, and the agent's channel-visible reply is written without that content.

Only the initiator can choose. The decision is recorded once, and there's no unshare. Tools set to **Auto Run (Everywhere)** skip this step and are shared immediately. Direct messages skip it too, because only the initiator is there.

## What observers see

| Observers see | Observers don't see |
|---|---|
| The agent's written reply | Tool arguments, which are replaced by a placeholder while a call is pending or kept private |
| The tool name | The result of any tool the initiator kept private |
| An **Auto-approved** badge | Working **Accept** and **Reject** buttons |
| The follow-up built from shared content | Anything the agent wrote from private results that would reveal them |

## Automated triggers

A post written by a bot, plugin, or OAuth app has no human who can approve.

- Webhook posts never trigger an agent. Bot and plugin posts do only when they set the `activate_ai` post property.
- A bot post that sets `activate_ai` can start a channel turn. With channel tool calling on, Mattermost removes every tool except those set to **Auto Run (Everywhere)** and the built-in helpers. With it off, the run has no tools.
- Any other automated post starts a run with no tools.

There's no path where a bot approves its own call. To let an automated flow use a tool, set that tool to **Auto Run (Everywhere)** after a security review, or keep a person in the loop.

## Audit

In-channel approvals are recorded in the server audit log as `toolCallApproval` and `toolResultApproval`, with identifiers only. See [Monitor Agents](observability.md#audit-logging).
