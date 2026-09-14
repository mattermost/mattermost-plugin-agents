# Multiplayer Tool Calling — Authentication Model

## 1. Overview

When an Agent answers a question in a direct message, the authentication story is simple: only one human is in the conversation, so only one person can approve tools, see arguments, and read results. Channels break that assumption. The moment an Agent participates in a public or private channel, several people can read the conversation, several people might want to act on it, and several different identities could in principle be used to run a tool.

**Multiplayer tool calling** is the set of behaviors that make Agents safe to use in shared channels. It is not a single feature — it is a coordinated set of rules covering who can approve a tool call, whose credentials run it, what other people in the channel see, and how those defaults can be tightened or relaxed by admins per tool.

This document is the canonical reference for those rules. It is aimed at:

- **Admins** deciding how to configure per-tool approval policies for their workspace.
- **Power users and prompt authors** who want to understand why other channel members can or cannot see what they see.
- **Security and compliance reviewers** evaluating whether Agents is safe to enable in regulated channels.

End-user instructions (how to click **Accept** or **Reject** in the moment) live in the [user guide](../user_guide.md#use-tools). This document explains the model behind those clicks.

## 2. The four roles

Every tool call in a channel involves up to four distinct roles. These terms are used consistently throughout the rest of this document.

| Role | Definition |
|---|---|
| **Initiator** | The human user who triggered the Agent — for example, by `@`-mentioning the bot or by sending a message in an Agents-pane thread. The initiator is recorded as `UserID` on the conversation row in the `LLM_Conversations` DB table. |
| **Approver** | The user permitted to click **Accept** or **Reject** on a pending tool-call card. In multiplayer tool calling, **the approver is always the initiator** — never another channel member, never an admin. |
| **Executor** | The identity Mattermost uses when actually running the tool — opening the HTTP request, hitting the MCP server, reading channel posts, etc. For agents using per-user authentication (the default), the executor inherits the initiator's user identity and the initiator's per-user OAuth tokens for OAuth-backed MCP servers. For agents configured with **service account authentication**, external MCP servers use admin-configured service account credentials and Mattermost (embedded) and plugin tools still run as the initiator (§3). |
| **Observer** (or **onlooker**) | Any other channel member who can read the channel post containing the Agent's response, but who did not trigger it. Observers are read-only with respect to the tool call: they cannot approve, cannot reject, and (by default) cannot see tool arguments or private results. |

Concrete example: Maya `@`-mentions `@copilot` in `~team-eng` and asks it to look up a Jira ticket. Raj is also in `~team-eng` and watches the conversation unfold.

- **Initiator:** Maya
- **Approver:** Maya (Raj cannot click **Accept** even though he can see the card)
- **Executor:** Maya's identity / Maya's Jira OAuth token
- **Observer:** Raj

These roles do not change mid-conversation. If Maya hands the keyboard to a teammate, the teammate is still posting as Maya from Mattermost's perspective and is therefore still the initiator.

## 3. The credential model

Every agent runs its tools in one of two authentication modes. The mode is a property of the agent — set by whoever manages the agent, on its **MCPs** tab — not a per-call choice.

### Per-user authentication (the default)

When a tool runs after approval, Mattermost executes it **as the initiator**. Two specific things this means:

1. **Mattermost-side identity:** the user context passed into tool execution is built from the initiator's user record, channel membership, and team membership. Tools that read channel posts, search users, or call back into Mattermost see only what the initiator is permitted to see.
2. **OAuth-backed MCP servers:** for any MCP server that requires per-user OAuth, the OAuth token loaded for the call is the initiator's token, looked up by the initiator's user ID and the server name.

The Agent bot's own credentials are **not** used to run third-party tools in this mode. The bot is a delivery surface — it posts the response and the tool cards — but the side effects of tool execution belong to the initiator. This is intentional: it keeps audit trails meaningful, it prevents privilege escalation through the bot, and it makes per-user OAuth scopes the right place to enforce who can do what.

A few consequences fall out of this mode:

- Two different users in the same channel running the same tool against the same MCP server may get different results, because they have different OAuth scopes. That is correct behavior, not a bug.
- A tool call that succeeds for one initiator may fail for another with a 401 or 403. OAuth 401 responses from MCP servers are caught and wrapped as `OAuthNeededError` in the client, surfacing as a tool auth error that prompts the initiator to re-authenticate.

For MCP servers that do not use OAuth (for example, a server fronted by a shared API key configured by an admin), the credential is whatever the server itself was configured with. The initiator-as-executor rule still controls **whether** the call happens; the server's static credentials control **what** the call can do.

### Service account authentication

An agent can instead be switched to **service account authentication** — the **Use service accounts for authentication** setting on the agent's MCPs tab. The setting is all-or-nothing for that agent's **external** MCP servers and changes who the executor is there:

- **External MCP servers:** tool calls are sent with the admin-configured **Service Account Authentication** headers from the server's System Console configuration (for example, a personal access token) instead of the initiator's OAuth token. Servers with no service account headers configured are **excluded from the agent's tool catalog entirely** — the agent fails closed rather than falling back to anyone's personal credentials.
- **Embedded Mattermost tools and plugin-registered MCP servers:** tool calls run as the **initiator**, the same as in per-user mode. The initiator's channel membership and permissions are the access boundary inside Mattermost.
- **No per-user OAuth anywhere:** users are never prompted to connect accounts for these agents, per-user tool provider preferences don't apply, and the per-user **Tools** menu is not shown.

**The approval contract does not change: the human approves; external MCP servers execute as the service account and Mattermost/plugin tools execute as the initiator.** The initiator is still the only person who can accept or reject a pending tool call (§4), per-tool policies (§5) still decide which calls need approval, and the Share / Keep Private flow (§6) still belongs to the initiator. What changes is whose credentials perform the side effect on external MCP servers once consent is given.

This mode deliberately flattens permissions on external MCP servers: every user who can use the agent acts with the same service account access there. Mattermost and plugin tools still see only what the initiator can see. The external system's audit log shows the service account, not the human; Mattermost's token usage logs record the triggering user alongside the acting identity (`acting_user_id`, `tool_auth_mode`) so the two can be correlated. Only system administrators can enable the setting. While it is enabled, managers may edit non-sensitive config; sensitive fields stay system-admin-only — see [What's editable vs locked](managing_agents.md#whats-editable-vs-locked). Anyone who can manage the agent can still turn the setting off or delete the agent. It requires the same license as remote MCP servers; without that license the agent's tool calls keep using per-user credentials. Configuration and hardening guidance live in the [admin guide](../admin_guide.md#service-account-authentication).

## 4. Approval ownership

> **The conversation initiator is the only person who can approve or reject a pending tool call. No other channel member, including admins, can approve on the initiator's behalf.**

This is enforced server-side, not just visually. When the **Accept** or **Reject** button is clicked, Mattermost checks that the clicking user's ID matches the conversation's recorded `UserID`. If it does not, the request is rejected with an error before any tool runs.

The reasoning behind initiator-only approval:

- **Side effects belong to the requester.** In per-user authentication mode, tools run with the initiator's credentials and OAuth scopes (see §3), so only the initiator has the standing to consent to side effects executed under their identity. In service account mode, initiator-only approval remains the consent contract even though the service account performs the action.
- **Multiplayer approval would be confusing under partial trust.** If three members of a channel could each approve the same tool call, the question "whose Jira token did this run with?" becomes ambiguous and the audit trail becomes harder to reason about.
- **Admins are not implicit approvers.** A workspace admin watching the channel does not automatically gain the ability to consent on the initiator's behalf. Admin authority lives in the per-tool policy (§5), not in the per-call approval.

Initiator-only approval applies in both credential modes (§3). For a service account agent, the initiator is still the person asking for the side effect, so their consent is still the one that matters; the admin consented to the credential itself when they enabled it on the agent.

### What if the initiator is offline or never returns?

Pending tool calls remain pending. They do not auto-approve after a timeout, and they do not transfer to anyone else. Practically, this means a tool call left unaddressed is a no-op: the Agent's response stops at the unapproved card, the tool never runs, and side effects never happen. The conversation can be re-triggered later, in which case the new conversation row creates a new initiator and a new approval flow.

### Mixed approval streams

A single Agent response can include multiple tool calls. If some of those calls are governed by an `auto_run` policy (§5) and others require approval, the auto-approved tools render with an **Auto-approved** badge and execute immediately, while the pending ones still show **Accept** / **Reject** buttons for the initiator. The badge applies per-tool, not per-response. (This is the behavior fixed in PR #645; earlier builds rendered the entire stream as either pending or approved.)

## 5. Per-tool policy interaction

Admins configure each tool with one of three policy values:

| Policy | Meaning |
|---|---|
| `ask` | Tool requires explicit Accept from the initiator before every call, in both DMs and channels. |
| `auto_run_in_dm` | Tool runs automatically without a prompt **only when the initiator is in a direct message with the Agent**. In channels, the tool falls back to `ask` behavior. |
| `auto_run_everywhere` | Tool runs automatically without a prompt in DMs **and** in channels. |

> **Note on naming:** the value `auto_run_in_dm` is sometimes shortened to "auto_run" in conversation. The canonical configuration value, including the DM scope, is `auto_run_in_dm`.

The decision tree at the start of each tool call is:

```
is the conversation a DM?
├── yes → policy is auto_run_in_dm OR auto_run_everywhere → run silently
│         policy is ask                                   → show Accept/Reject card
└── no  → policy is auto_run_everywhere                   → run silently
          policy is auto_run_in_dm OR ask                 → show Accept/Reject card
```

In other words, `auto_run_everywhere` is a **strict superset** of `auto_run_in_dm`. A tool that is safe enough to fire automatically in a DM is not necessarily safe enough to fire automatically in a channel that other people are watching, so admins must opt in to the `everywhere` scope explicitly.

### Which policy fits which kind of tool?

The intent behind the three values, in plain language:

- `ask` is the safe default. Use it for any tool that writes data, sends notifications, costs money, or whose output should never be revealed to the initiator without explicit consent.
- `auto_run_in_dm` is the right setting for read-only tools whose output is fine to surface privately but might leak context if it appeared in a shared channel without the initiator's confirmation. Examples: searching the initiator's own email, listing their own calendar events.
- `auto_run_everywhere` is for read-only tools whose results are unambiguously safe to share with everyone in any channel. The vetted-provider seeding flow (PR #520) installs the embedded Mattermost built-in tools (`read_post`, `read_channel`, `get_channel_info`, `get_channel_members`, `get_team_info`, `get_team_members`, `search_posts`, `search_users`, `get_user_channels`) at `auto_run_in_dm` by default. Admins must explicitly promote any of these to `auto_run_everywhere` if they want them to skip approval in channels — and given the privacy implications described in section 6, that decision should be deliberate.

The policy list is not user-editable from the chat surface. Admins set it in the agent's tool configuration; per-call approvals do not promote a tool from `ask` to `auto_run_*`.

## 6. Channel privacy: the Share / Keep Private two-step

In a DM, when a tool runs, its arguments and results are visible to the only human in the conversation — the initiator — and there is nothing to decide. Channels add a second decision: even after a tool has run, its arguments and result may not be appropriate for everyone in the channel to see.

Multiplayer tool calling handles this with a two-step flow:

1. **Step 1: approve the call.** The initiator clicks **Accept** (or the policy auto-approves). The tool runs with the executor's credentials — the initiator's by default, or the agent's service account on external MCP servers (§3).
2. **Step 2: share the result.** Once the tool returns, the initiator sees a follow-up control with **Share** and **Keep Private** options.
   - **Share** marks the tool result as visible to the channel. Other channel members see the arguments and the result, and the Agent's follow-up response incorporates the result openly.
   - **Keep Private** marks the result as private to the initiator. Other channel members do not see the arguments or the result, and the Agent's follow-up response is generated without leaking that content into the channel-visible reply.

DMs auto-share by definition: there is no other human in the room, so there is no second step.

A few important properties of the two-step flow:

- **Only the initiator can answer the Share / Keep Private prompt.** Like approval, the visibility decision is keyed off the conversation's `UserID`.
- **Keep Private does not delete the result.** It is stored on the conversation and remains available to the Agent for generating the follow-up response. It is filtered out of channel-visible rendering for non-initiator members.
- **The decision is recorded once.** Re-clicking **Share** or **Keep Private** after the decision is made is idempotent.
- **A tool governed by `auto_run_everywhere` skips both steps — the tool auto-executes and its results are immediately marked as shared with the channel. There is no Share / Keep Private decision for `auto_run_everywhere` tools.** Admins setting a tool to `auto_run_everywhere` should treat the tool's results as unconditionally visible to every channel member, with no per-call privacy control.

## 7. The onlooker experience

Observers — channel members who did not trigger the Agent — see a deliberately filtered view of the tool flow. The intent is that an onlooker can follow the conversation and see that something is happening, but they cannot read sensitive arguments, cannot approve, and cannot pre-empt the initiator's privacy choice.

| What the onlooker sees | What the onlooker doesn't see |
|---|---|
| The Agent's prose response to the channel | Tool arguments — replaced with a placeholder while the call is pending or kept private (PR #681) |
| The tool name, so they know which tool ran | The contents of any tool result that the initiator chose **Keep Private** for |
| The **Auto-approved** badge for tools that ran without a prompt | Active **Accept** / **Reject** buttons — the buttons are inert for non-initiators, or hidden, depending on UI state |
| The eventual Agent response built from approved + shared content | Anything the Agent generates from kept-private tool results that would leak the underlying data |

In other words: by default, onlookers see that a conversation is happening, not what was passed to a third-party API. The Share / Keep Private decision (§6) gives the initiator the option to widen visibility deliberately.

The hidden-parameter placeholder (PR #681) was added specifically because earlier builds rendered tool arguments verbatim to every channel member as soon as the card was posted. That created a leak window before the initiator had even clicked Accept. The placeholder closes that window.

## 8. Tool card UI semantics

The tool-call card is the single piece of UI that ties this whole model together. A few details worth pinning down:

- **Buttons are labelled Accept / Reject, not Approve / Reject.** This was changed in PR #664 to better reflect that the user is making a personal decision about a side effect, not granting approval on behalf of the channel.
- **"No parameters required" replaces empty-object rendering.** When a tool takes no arguments, the card shows the explicit string "No parameters required" rather than `{}` (PR #596 / commit `289dd21d`). This is a UI clarification only; it does not change auth or execution.
- **The card is bound to the post and the conversation, not just the post.** PR #642 threaded the originating `postId` through the markdown renderer so the tool card always finds its conversation, including in edge cases like edited posts and rendered-image regressions.
- **Mixed streams render correctly.** A response that contains both auto-approved tools and pending-approval tools renders each tool with its own state (PR #645). There is no longer a single "this whole response is approved" or "this whole response is pending" rendering bug.
- **Bulk controls.** When multiple tools are pending in the same response, the card may surface **Accept all** and **Reject all** controls in addition to per-tool buttons. Bulk controls obey the same initiator-only rule as the individual buttons.

## 9. Bot-triggered flows

A subtle case: what happens if the message that triggers the Agent was itself posted by a bot (for example, a webhook-driven post that `@`-mentions an Agent), or if the Agent is invoked by another automated integration?

The behavior, per PR #611: **automated invokers are treated as not-eligible-to-approve**, and tool-calling flows that would require approval are not initiated. There is no "the bot approved on its own behalf" path, because that would defeat the entire initiator-only model.

The plugin distinguishes two kinds of automated posts:

- **Bots without the `activate_ai` post prop** produce no tool processing at all. The mention handler returns immediately, and the Agent does not enter the tool-calling pipeline.
- **Bots that opt in via the `activate_ai` post prop** (PR #611's intended use case) are filtered down to `auto_run_everywhere`-only tools before the runner starts. Any tool whose policy is `ask` or `auto_run_in_dm` is removed from the available toolset for that turn. Only `auto_run_everywhere` tools remain and execute.

In practice this means:

- A bot-posted message that asks an Agent to do something tool-heavy does not produce a tool-approval card with no human attached to it.
- Tools governed by `auto_run_everywhere` may still execute under bot-triggered flows because they do not require approval at all. This is consistent with the policy model: `auto_run_everywhere` is the admin's pre-declared statement that this tool is safe to run without a human in the loop.
- Tools governed by `ask` or `auto_run_in_dm` will not run when the trigger is automated, because there is no eligible human approver.

This is the intentional behavior. If a workflow needs an Agent to invoke `ask`-policy tools on a recurring schedule, the right answer is for an admin to either (a) reclassify those tools as `auto_run_everywhere` after a security review, or (b) keep a human in the loop. Bots cannot promote themselves into the approver role.

## 10. Operational guidance for admins

Configuring the per-tool policy list is the main lever admins have over multiplayer behavior. A few recommendations:

- **Default new tools to `ask`.** It is the conservative choice and the easiest to relax later. Going the other direction — discovering a tool you wanted to require approval for has been auto-running in channels — is much worse.
- **Reserve `auto_run_everywhere` for read-only tools with built-in permission enforcement.** The embedded Mattermost search tools ship at `auto_run_in_dm` by default. They fit the `auto_run_everywhere` profile in principle — Mattermost server enforces per-user permissions on every result — but the choice to promote them is left to the admin so that channel privacy expectations are explicit. Tools that hit external APIs almost never belong in this category unless you have a strong reason.
- **Treat `auto_run_in_dm` as the "convenience in private, friction in public" mode.** Use it for tools that are safe for the initiator to see results from privately but where you don't want results splashed into a channel without explicit consent.
- **Don't use policy as a substitute for credential scope.** Policy controls the prompt; the underlying tool still runs as the initiator — or, for service account agents, as the service account on external MCP servers and as the initiator for Mattermost and plugin tools. Lock down scopes in the OAuth provider (and scope service account tokens minimally), not in the Agents policy.
- **Audit which tools are seeded by default.** PR #520 adds vetted-provider seeding so that built-in MCP tools come pre-configured. Review the seeded list when you upgrade to v2 to make sure the defaults match your risk tolerance.

The channel tool-calling capability itself is gated by the workspace setting **Enable Channel Mention Tool Calling**, which is experimental at the time of the v2 launch. Until that setting is enabled, multiplayer tool calling is effectively limited to `auto_run_*` policies in DMs and admins do not need to think about channel privacy at all.

## 11. Tradeoffs and explicit non-goals

To make the model auditable, several things are deliberately **not** supported. These are not oversights; they are design choices.

- **No "any channel member can approve" mode.** Allowing onlookers to approve would mean a side effect is committed against the initiator's request by someone who has no standing to consent to it — with the initiator's own credentials in the default mode, or with the agent's service account in the other. That is a confused-deputy pattern. We do not offer it.
- **No per-channel admin override of the initiator-only rule.** Channel admins do not gain the ability to approve other people's pending tool calls just because they administer the channel.
- **No timeout-based auto-approval.** A pending tool call left untouched does not silently fire. It just sits there until cleared.
- **No implicit tool execution as the bot.** For per-user-authentication agents (§3), tools always run as the initiator; the bot never silently substitutes its own identity, and there is no fallback to a shared credential when the initiator's OAuth token is missing or rejected. Service account execution exists only as an explicit, admin-configured, per-agent opt-in — and even there, servers without service account credentials drop out of the agent's catalog rather than borrowing anyone else's.
- **No retroactive privacy.** Once the initiator chooses **Share**, the result is visible to channel members and the Agent's follow-up response incorporates it openly. There is no "unshare" button. The same is true for `auto_run_everywhere` tools, which are auto-shared at the moment they execute — there is no Share / Keep Private prompt to retract. (Channel-level moderation tools — deleting posts, etc. — are still available but live outside Agents.)

## 12. Related docs

- [User guide — Use tools](../user_guide.md#use-tools): the end-user-facing instructions for Accept / Reject and the Tools menu.
- [Admin guide](../admin_guide.md): per-tool policy configuration, agent management, [service account authentication](../admin_guide.md#service-account-authentication), and the **Enable Channel Mention Tool Calling** setting.
- [Channel summaries](channel_summaries.md): a related channel-aware feature with its own privacy story.
- [Providers](../providers.md): provider-side tool support, OAuth setup for MCP servers.
