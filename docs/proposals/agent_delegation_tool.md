<!--
Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
See LICENSE.txt for license information.
-->

# Proposal: a "call another agent" delegation tool

**Status: brainstorm + rough plan. No implementation yet.**

This document explores giving an agent (referred to here as "Maddie", the user's
primary/default agent — the code equivalent is the `DefaultBotName` agent in
`config/config.go`) a tool whose effect is *call another agent*. The goal is "one
pane of glass": a user talks to a single agent, and that agent orchestrates the
other agents on the user's behalf. Sub-agents fade into the background for most
people, but power users can still DM or @mention any agent directly.

The originating idea proposed using **Mattermost itself as the delegation
surface**: Maddie literally DMs the sub-agent bot and reads the reply, with each
DM thread providing isolation. It also flagged the open problem with that naive
version: the Maddie↔sub-agent DM channel is *shared across every user who routes
through Maddie*, so history access could leak one user's data to another. This
document grounds that idea in the current code, diverges across several designs,
and converges on a recommendation.

---

## Part 1 — What the code says today

### 1.1 Tools: definition, registration, per-agent scoping

- A tool is an `llm.Tool` (`llm/tools.go`): `Name`, `Description`, `Schema`,
  `Resolver func(ctx, *llm.Context, ToolArgumentGetter) (string, error)`, plus
  `ServerOrigin` (empty for built-ins, set for MCP tools), `UserInteraction`
  (for tools answered by the user in the UI, e.g. `AskUserQuestion`), and
  `CallMetadata`.
- Built-in tools are produced by `mmtools.MMToolProvider.GetTools(bot, llmContext)`
  (`mmtools/provider.go`). Today that is web search, web fetch, and
  `AskUserQuestion` — the latter only when
  `llmContext.ToolCatalog.InteractiveUserPresent` is set.
- The per-request `llm.ToolStore` is assembled in
  `llmcontext.Builder.getToolsStoreForUser` (`llmcontext/llm_context.go`):
  built-ins + per-user MCP tools, filtered by the agent's config.
- **Per-agent tool scoping exists, but only for MCP tools.** `llm.BotConfig`
  (`llm/configuration.go`) has `DisableTools` (kill switch), `EnabledMCPTools` +
  `AutoEnableNewMCPTools` (per-agent MCP allowlist), `MCPDynamicToolLoading`
  (progressive disclosure via `search_tools`/`load_tool` meta-tools in
  `mcp/meta_tools.go`), and `EnabledNativeTools`. There is **no per-agent
  allowlist for built-in tools** — a new built-in delegation tool needs its own
  gate (a config field or context condition).
- The tool loop is `toolrunner.ToolRunner.Run` (`toolrunner/toolrunner.go`): call
  the LLM, collect tool calls, consult a `shouldExecute(llm.ToolCall) bool`
  predicate, execute approved calls, feed results back, repeat up to
  `BotConfig.EffectiveMaxToolTurns()` (default 30, cap 250). Calls that fail the
  predicate surface as **pending approval cards** in the UI, resumed through
  `Conversations.HandleToolCall` (`conversations/tool_approval.go`).
- Auto-execution policy (`Conversations.shouldAutoExecuteTool`,
  `conversations/conversations.go`): MCP meta-tools always auto-run;
  user-interaction tools never; everything else is looked up in the admin
  per-tool policy (`ask` / `auto_run_in_dm` / `auto_run_everywhere`,
  `config/mcp_config.go`, resolved by `mcp.LookupToolPolicy`). **A tool with an
  empty `ServerOrigin` (i.e. any built-in) resolves to "never auto-run"**
  (`mcp/tool_policy.go`: unknown origins return `(ask, false)`), so built-ins
  like WebSearch go through the approval card today. A delegation tool would
  inherit that "ask every time" default unless we explicitly add policy plumbing
  for it.

### 1.2 Bot accounts and DMs

- Agents and bots are the same runtime entity: `bots.Bot` (`bots/bot.go`) wraps an
  `llm.BotConfig`, a Mattermost `*model.Bot` account, and an `llm.LanguageModel`
  handle. `bots.MMBots.EnsureBots` (`bots/bots.go`) merges file-config bots and
  DB-backed agents (`store/agents.go`, table `Agents_UserAgents`) into one
  lineup and creates/patches the Mattermost bot accounts.
- DMs: `pluginAPI.Channel.GetDirect` creates/fetches a DM channel;
  `mmapi.Client.DM` posts into one as a bot. `streaming.StreamToNewDM`
  (`streaming/streaming.go`) already opens a bot→user DM and streams an LLM
  response into it (used by the thread/channel analysis flows, which deliver
  their result as a bot DM).
- Bot posts are marked by `streaming.ModifyPostForBot`
  (`streaming/post_modifier.go`): `post.UserId = botID`, post type
  `custom_llmbot`, props `llm_requester_user_id` (the human who asked) and
  `conversation_id` (ownership + turn persistence).
- Routing (`conversations/handle_messages.go` `handleMessages`): on every posted
  message the plugin (1) **refuses to respond to its own bots unconditionally** —
  `c.bots.IsAnyBot(post.UserId)` returns before anything else, with **no
  `activate_ai` bypass**; (2) refuses other bots/webhooks/plugins unless the
  post sets the `activate_ai` prop; (3) then routes @mentions via
  `GetBotMentioned` and DMs via `GetBotForDMChannel`.
- Two consequences for "Maddie DMs the sub-agent": **today the message would be
  dropped at step (1)** — one plugin bot can never trigger another. And
  `GetBotForDMChannel` (`bots/bots.go`) returns the *first* bot whose user ID
  appears in the DM channel name — a DM channel between two plugin bots matches
  both, so routing would be ambiguous even if the block were lifted.

### 1.3 How an agent turn runs

DM path (`handleDMViaConversation`, `conversations/handle_messages.go`):

1. Build `*llm.Context` via `llmcontext.Builder.BuildLLMContextUserRequest`
   (server info, sanitized `RequestingUser`, channel, the bot's persona fields
   including `CustomInstructions`) with `WithLLMContextTools` binding the tool
   store **to the requesting user's ID**.
2. Get/create a conversation entity (`conversation.Service`,
   `store/conversations.go`): rows carry `(ID, UserID, BotID, ChannelID,
   RootPostID, Operation, SystemPrompt, …)` with a partial unique index on
   `(RootPostID, BotID, UserID)` (`store/migrations/000007…`), i.e.
   **conversations are per-user even within the same thread**.
   `ChannelID` and `RootPostID` are nullable — the entity model already
   supports conversations not bound to any post or channel (and those are
   exempt from the unique index, so many can exist per bot/user pair).
3. Format the system prompt (`prompts/direct_message_question_system.tmpl` →
   `standard_personality_without_locale.tmpl`, which renders
   `CustomInstructions`).
4. `toolrunner.Run` the loop; write tool turns via
   `conversation.Service.WriteToolTurns`; stream the final text into a
   placeholder post (`streaming.Service`).

Channel mentions are similar but build thread context from the actual Mattermost
thread (`mmapi.GetThreadData` — **admin-level plugin API**, it fetches the whole
thread regardless of who can read it) and always **redact tool results that the
requester didn't explicitly share** (`conversation/convert.go`, `RedactUnshared`,
`Shared` flags) before the content reaches a channel-visible completion.

### 1.4 Programmatic turns and the "automation" model

- There is no automation engine in this repo. Automations live in the separate
  `com.mattermost.channel-automation` plugin; this plugin exposes MCP proxy CRUD
  tools for them (`mcpserver/tools/automations.go`) and, more relevantly, the
  **LLM bridge**: `POST /bridge/v1/completion/agent/:agent[/nostream]`
  (`api/api_llm_bridge.go`, inter-plugin auth only). `prepareAgentBridgeCompletion`
  already implements a headless full agent turn: caller-supplied posts +
  `user_id`, per-agent permission check (`checkBridgePermissions` →
  `bots.CheckUsageRestrictionsForUser`), an `allowed_tools` allowlist scoped from
  the agent's real tool store, and a `toolrunner.Run` with auto-execution limited
  to that allowlist. It is stateless — no conversation rows.
- Bots can trigger agents in channels only via the `activate_ai` post prop, and
  then the tool set is filtered to `auto_run_everywhere` tools only
  (`computeAllowToolsInChannel`, `conversations/bot_channel_tool_filter.go`),
  because an unattended invoker can never answer an approval card. This is the
  closest existing precedent for "an agent invoked by a non-human".

### 1.5 Identity and permissions (the part that makes or breaks delegation)

- `llm.Context.RequestingUser` (`llm/context.go`) is the identity anchor. Tool
  stores are built **for that user**; MCP tools resolve through per-user clients
  (`mcp.ClientManager.GetToolsForUser(ctx, userID)`), and the embedded MCP
  server mints a real Mattermost **session for that user**
  (`mcp/embedded_session_store.go`) so every embedded tool (`search_posts`,
  `read_channel`, `read_post`, …) executes as the user via `Client4` — server
  ACLs apply. Semantic search additionally joins `ChannelMembers` on the
  requesting user's ID in SQL (`postgres/pgvector.go`).
- The documented model (`docs/features/multiplayer_tool_calling.md`) is
  **initiator = approver = executor**: tools always run with the identity and
  OAuth tokens of the human who triggered the agent; the bot's own credentials
  are never used for tool side effects. "Service accounts are not a concept in
  this model."
- The plugin API (`mmapi.Client`) is admin-level and used for thread fetch, post
  creation, and enrichment; wherever it serves user-visible data, callers check
  permissions explicitly (e.g. `files/files.go` checks
  `HasPermissionToChannel` before returning file content).
- The agent's own access control is `bots.CheckUsageRestrictions{,ForUser}`
  (`bots/permissions.go`): per-agent user allow/block lists, team lists, channel
  lists. This is "may this user use this agent at all" — distinct from Mattermost
  data ACLs.
- The embeddings indexer **skips bot-DM channels** (`indexer/indexer.go`), and no
  existing LLM-facing tool reads data by *bot* visibility; every read path is
  scoped to the requesting user. That invariant is what currently prevents
  "the agent searched something the user can't see".

### 1.6 Existing agent-to-agent machinery

Effectively none, which is good news for a clean design:

- `list_agents` (`mcpserver/tools/agents.go`) — discovery only; returns each
  agent's ID/display name/username via the plugin's `/ai_bots` endpoint using
  the requesting user's token.
- `HandleLoopInAgent` (`conversations/loop_in_agent.go`) — reprocesses a user's
  own thread reply as if it @mentioned the thread's agent. A precedent for
  *synthetic invocation*, but same-user, same-thread.
- `activate_ai` — lets *external* bots/integrations trigger an agent mention
  (never this plugin's own bots), with tools restricted to
  `auto_run_everywhere`.
- No `subagent`/`delegate`/orchestrator concepts anywhere else (verified by
  search).

### 1.7 Constraints that shape the design

1. **Plugin bots cannot message-trigger each other today** (`handleMessages`
   ordering), and lifting that block reopens loop-prevention questions.
2. **Everything user-scoped hangs off `llm.Context.RequestingUser` + the per-user
   MCP session.** Whatever invokes the sub-agent must bind the *original human*
   there, or the sub-agent acts as somebody else (the bot), which is both a
   privilege escalation and an ACL bypass: `ensureEmbeddedSessionID` would mint
   a session for the *bot user*, and `CheckUsageRestrictionsForUser` would
   evaluate the bot's ID against the sub-agent's user ACL.
3. **Conversation entities already have per-user isolation built in** (`UserID`
   column, ownership checks in `api/api_conversation.go`) and support
   channel-less/post-less conversations. We do not need posts to have durable,
   auditable, per-user sub-conversations.
4. **Unattended turns cannot use approval-gated tools** (no one can click the
   card); the `activate_ai` precedent solves this by tool-set attenuation, and
   `InteractiveUserPresent=false` already auto-excludes user-interaction tools.
5. **Built-in tools default to "ask" approval**; a delegation tool gets a human
   confirmation card per call for free, unless/until we add policy plumbing.
6. Cost/limits plumbing exists per agent (`MaxToolTurns`, token tracking per
   bot/service, `Operation` constants in `llm/token_usage_fields.go`), so a
   nested turn can be metered and bounded with existing mechanisms.

---

## Part 2 — Brainstorm (diverge)

Five candidate architectures for the delegation transport, then the orthogonal
decisions (identity, isolation, discovery, tool placement, nested approvals)
that apply across them.

### Approach A — Literal bot↔bot DM (the naive version, taken seriously)

**Mechanism.** The tool resolver opens (or reuses) the DM channel between
Maddie's bot and the sub-agent's bot (`Channel.GetDirect`), posts the task with
`activate_ai`-style opt-in, and waits for the sub-agent's reply post. The
sub-agent's normal DM pipeline handles the message; Maddie's resolver correlates
the reply (via `responding_to`/`conversation_id` props or a completion marker)
and returns its text as the tool result.

**What it takes.** Relax the `IsAnyBot` early-return in `handleMessages` for
opted-in bot posts; disambiguate `GetBotForDMChannel` for two-bot channels; add
reply-completion signaling (streamed posts update in place, so "reply arrived"
≠ "reply finished" — the resolver must wait for stream completion); thread the
original requester's identity through post props so the sub-agent binds
`RequestingUser` to the human rather than to Maddie's bot.

**Security.** This is where it falls apart, and the 1:1 predicted it:

- *The shared-channel leak is structural.* There is exactly one Maddie↔sub-agent
  DM channel per bot pair, shared by **all** users who route through Maddie.
  Every delegated task and answer for every user accumulates there as real
  posts. Even before any "search my history" tool: the sub-agent's own context
  assembly (`GetThreadData` is admin-level; `CreateOrGetDMConversation` would
  key one conversation row to the *bot* as the user) would blend user A's
  delegation into the context of user B's next delegation in the same
  thread/channel.
- Today no tool reads by bot visibility, so a *human* couldn't fish the channel
  out via `read_channel` (they're not a member; embedded tools act as the
  user; the indexer skips bot DMs). But the design *invites* exactly the tool
  that breaks this ("search your DM history with the project agent"), and any
  future bot-scoped read, compliance export, or retention view sees a channel
  that pools every user's delegated content.
- Identity is wrong by default (constraint 2): unless carefully overridden
  everywhere, the sub-agent acts as Maddie's bot — an over-privileged,
  audit-meaningless executor.

**Tradeoffs.** Pro: maximally "Mattermost as the surface" — visible,
debuggable, retention-covered posts; zero new execution machinery. Con: the
isolation and identity problems above are not incidental, they are the
architecture; fixing them (per-user channels, identity piping, reply
correlation) turns this into Approach B while keeping bot-loop risks. Power
users keep direct access (unchanged).

**Verdict:** reject as transport. Post-based visibility is worth keeping as an
optional *rendering* of a delegation, not as its execution mechanism.

### Approach B — Per-user proxied threads in the *user's* DM with the sub-agent

**Mechanism.** Delegation surfaces where the user could already talk to the
sub-agent: the user↔sub-agent DM channel. Maddie's tool posts the task there as
a new thread — either authored by the sub-agent bot itself ("@alice asked me via
Maddie: …", then it answers in-thread, à la `StreamToNewDM`), or via a synthetic
invocation like `HandleLoopInAgent` — and the resolver reads the thread's final
answer back into Maddie's tool result.

**Security.** Isolation is inherited from DM membership: only the requesting
user and the sub-agent bot are members, so cross-user reading is impossible for
both humans and user-scoped tools. Identity can be bound correctly: the
conversation row keys to the real user; tool execution binds to the user.

**Tradeoffs.** Pro: solves the shared-channel leak *by construction*; fully
visible; the power-user story is beautiful (delegated threads live exactly where
you'd talk to the agent directly, and you can just keep typing in that thread to
follow up). Con: heavier and noisier — every delegation spawns a DM thread with
unread markers and notifications the user didn't ask for; the "who authored the
task post" question is awkward (impersonating the user is unacceptable; a bot
posting into a DM it is not a member of is possible via the admin-level plugin
API but violates DM semantics; making Maddie's bot a *member* of the user's DM
with another agent isn't a thing — DMs are two-party); reply-completion
correlation is the same plumbing as A. Complexity concentrates in Mattermost
surface mechanics rather than in the agent loop.

**Verdict:** strong candidate for the *visibility layer*, weak as the execution
transport. Best treated as a Phase 2 rendering option on top of an in-process
core.

### Approach C — Headless stateless RPC (bridge-style in-process call)

**Mechanism.** The tool resolver invokes the sub-agent exactly the way the LLM
bridge does (`prepareAgentBridgeCompletion` internals, minus HTTP): resolve the
target `bots.Bot`, check `CheckUsageRestrictionsForUser(subBot, requester)`,
build a fresh `llm.CompletionRequest` from the task text, run
`toolrunner.Run(subBot.LLM(), …)` with a scoped tool store, return
`ToolRunResult.FinalText` as the tool result. Nothing persisted, no posts.

**Security.** Identity is whatever we bind — do it right and it's exactly the
requesting user. Isolation is trivial: there is no history at all, so there is
nothing to leak across users.

**Tradeoffs.** Pro: smallest diff — the machinery exists and is tested; the
bridge already solved tool scoping and per-agent permission checks. Con: the
bridge path builds a *minimal* context (no persona system prompt, no server
info), so the sub-agent wouldn't answer as "itself" without extra work; no audit
trail beyond logs; no continuity (every delegation is amnesiac); invisible to
the user except through Maddie's tool card. Statelessness is simple but gives up
the roadmap (follow-up delegations, inspectable sub-conversations).

**Verdict:** right execution family, too little structure. Its gaps are exactly
what the conversation entity model already provides.

### Approach D — Headless delegation with persisted, per-user sub-conversations

**Mechanism.** Same in-process execution as C, but each delegation runs inside a
real conversation entity: a new `LLM_Conversations` row with `UserID = the
requesting human`, `BotID = the sub-agent`, `ChannelID = nil`, `RootPostID =
nil`, `Operation = "delegation"`, `SystemPrompt =` the sub-agent's own DM
persona prompt (`PromptDirectMessageQuestionSystem` rendered against a context
built with `WithLLMContextBot(subBot)`). The sub-agent's tool store is built via
the normal `BuildLLMContextUserRequest(subBot, requestingUser, …)` path, then
attenuated (see "nested approvals" below). `toolrunner.Run` executes the loop;
`WriteToolTurns` persists intermediate turns; the final text returns as Maddie's
tool result, prefixed with attribution ("Answer from @projects-agent: …").

The flow, end to end:

1. User DMs Maddie: "Ask the project agent what shipped last sprint."
2. Maddie's LLM emits `ask_agent(agent: "projects-agent", task: "…")`.
3. The tool resolver validates: agent exists, is not Maddie itself,
   `CheckUsageRestrictionsForUser(subBot, user)` passes, depth limit not
   exceeded.
4. It creates the delegation conversation row, builds the sub-agent's context
   *for the requesting user*, runs the sub-turn (bounded rounds + timeout),
   persists the turns.
5. Maddie receives the answer as a tool result and continues its own turn.

**Security.** Identity: the requesting user everywhere — sub-agent tools bind to
the user's MCP clients/session, semantic search filters by the user's channel
membership, OAuth tokens are the user's. Isolation: sub-conversation rows are
keyed by the real user; the existing `GET /conversations/:id` ownership check
(`api/api_conversation.go`) makes them inspectable by their owner only; no posts
exist, so there is nothing in any channel to search, index, or export across
users. Auditability: turns (including tool calls with arguments/results) are in
the DB per delegation.

**Tradeoffs.** Pro: correct-by-construction on both security axes; reuses the
newest, best-supported machinery in the repo (conversation entities, toolrunner,
context builder); leaves the door open to continuity (re-use the sub-conversation
on repeat delegations) and to a visibility layer later. Con: invisible-by-default
— the user sees only Maddie's tool card unless we add UI (mitigable: the card can
deep-link to the sub-conversation via the existing conversation API); new
plumbing is needed to hand the resolver access to services (`bots` registry,
`conversation.Service`, `llmcontext.Builder`, prompts) — built-in tools today
only get `mmapi.Client` + web search, so this needs a new wiring path (a small
`delegation` package constructed in `server/main.go`, avoiding a
`conversations → mmtools → conversations` cycle).

**Verdict:** recommended core. Details in Part 3.

### Approach E — Handoff instead of delegation (loop the sub-agent in)

**Mechanism.** Not "Maddie asks and relays" but "Maddie hands off": in a channel
thread, the tool re-invokes the pipeline as if the user had @mentioned the
sub-agent (generalizing `HandleLoopInAgent`); the sub-agent replies *visibly in
the same thread*, as itself. In DMs the equivalent is "I've asked
@projects-agent — it will reply in your DM with it" + a link.

**Security.** Cleanest of all: the sub-agent replies where the user already is,
under the exact existing rules (initiator identity, channel redaction, approval
cards answered by the initiator). Nothing new to isolate.

**Tradeoffs.** Pro: minimal new security surface; great "the agents are real
coworkers" feel; sub-agent identity stays visible (attribution for free). Con:
**it is not orchestration** — Maddie never sees the answer, cannot synthesize
across multiple sub-agents, and the "one pane of glass" becomes "one pane that
redirects you". Latency/UX in DMs is awkward (answer lands in a different
channel).

**Verdict:** not the answer to this task, but cheap and complementary —
worth keeping in mind as a separate "bring @agent into this thread" affordance.

### Cross-cutting axis 1 — Whose identity does the sub-agent run with?

| Option | Description | Assessment |
|---|---|---|
| (i) Requesting user, full | Sub-turn context binds `RequestingUser` = the human; tools resolve through their MCP clients/session/OAuth. | **Recommended.** Matches the documented initiator=executor model; no privilege change in either direction; audit stays meaningful. Delegation adds no access the user didn't already have by DMing the sub-agent themselves. |
| (ii) Maddie's bot identity | Sub-agent acts as the bot account. | **Rejected.** Privilege escalation (bot sees channels the user may not), ACL bypass (`CheckUsageRestrictionsForUser` evaluated against a bot ID), meaningless audit, and directly enables the cross-user leak. The repo's security docs explicitly rule out bot-credential tool execution. |
| (iii) Requesting user, attenuated | (i) plus a reduced tool set for the sub-turn (e.g. only tools that would auto-run in a DM). | **Recommended as the v1 nested-tool policy** layered on (i) — see axis 4. Attenuation is about *unattended execution safety*, not identity. |

One nuance for (i): the delegation must also pass the *target agent's* usage
restrictions for that user. Otherwise delegation becomes a bypass of
`UserAccessLevel`/`TeamIDs` on restricted agents ("I can't DM the finance agent,
but Maddie can ask it for me"). `CheckUsageRestrictionsForUser` at delegation
time closes this.

### Cross-cutting axis 2 — History isolation (the naive-version leak, precisely)

Where the leak actually lives in the naive design, in code terms:

1. **Context assembly, not just search tools.** In a shared bot↔bot DM, the
   sub-agent's next turn is built from that channel's thread/conversation
   history (`GetThreadData` is admin-level; a DM conversation row would key to
   the bot-as-user, collapsing all humans into one conversation). User B's
   question gets answered with user A's context *without any tool call at all*.
2. **History tools.** "Search my DM history with the sub-agent" only leaks if a
   tool reads with *bot* visibility. Today none do — every read tool acts as the
   requesting user, and the indexer skips bot DMs. The invariant to preserve:
   **never introduce a tool or code path that reads by bot membership.**
3. **Ambient surfaces.** Real posts in a shared channel are subject to
   compliance export, retention, and future features; pooling all users'
   delegated content in one channel maximizes the blast radius of any mistake.

Isolation options, given that:

| Option | Mechanism | Assessment |
|---|---|---|
| Per-user DM channels/threads (Approach B) | Membership-based isolation. | Correct but drags in surface mechanics; right for visibility, not execution. |
| Per-user conversation entities, no posts (Approach D) | `UserID` column + API ownership checks; nothing exists in any channel. | **Recommended.** Strongest isolation with the least machinery; audit included. |
| Fully ephemeral (Approach C) | Nothing stored. | Secure but unauditable and roadmap-limiting. |
| Shared channel + "just don't add history tools" (Approach A) | Policy, not structure. | Rejected — one future tool or index change away from a breach. |

### Cross-cutting axis 3 — How Maddie discovers sub-agents (progressive disclosure)

- **Static catalog in the tool schema/description.** At tool-store build time,
  enumerate agents the *requesting user* passes `CheckUsageRestrictionsForUser`
  for (minus the current agent), intersected with the agent's configured
  delegate allowlist if set. Cheap and predictable; fine for the realistic
  low-dozens agent count. Description carries each agent's display name,
  username, and (v2) a per-agent "what to delegate to me" blurb.
- **`list_agents` + parameterized `ask_agent`.** Discovery already exists as an
  MCP tool; `ask_agent` validates the target server-side regardless of what the
  schema advertised. Good defense-in-depth pairing with the static catalog.
- **MCP dynamic loading.** If the tool ships as an embedded MCP tool (axis 5),
  agents with `MCPDynamicToolLoading` get `search_tools`/`load_tool` disclosure
  for free.
- **Per-agent delegation allowlist (config).** A `BotConfig` field (e.g.
  `DelegateAgentIDs`, empty = none, sentinel = all) mirrors the
  `EnabledMCPTools` pattern and gives admins the "Maddie is the router;
  the project agent can't delegate onward" control.

Recommendation: static catalog ∩ user permissions ∩ per-agent allowlist, with
server-side re-validation in the resolver. Progressive disclosure of *content*
("what is this agent good at") can ride the agent's display name +
custom-instruction summary later.

### Cross-cutting axis 4 — Nested tool calls and approvals

The sub-turn runs unattended mid-tool-call; nobody can answer a sub-agent's
approval card in v1. Options:

- **Attenuate (recommended v1):** build the sub-agent's store normally, then
  keep only tools whose policy would auto-run in a DM
  (`mcp.IsToolPolicyAutoRunInDM`), plus MCP meta-tools. `InteractiveUserPresent`
  stays false, so `AskUserQuestion` is excluded automatically
  (`mmtools/provider.go`). Strip `ask_agent` itself (or enforce a depth counter
  in `llm.Context`/`ToolRuntimeContext`) to prevent recursion. This mirrors the
  `activate_ai` precedent (`bot_channel_tool_filter.go`), except delegation can
  use the more generous DM policy tier because the delegated output returns to
  a surface the initiator controls.
- **Bubble approvals up (later):** surface the sub-agent's pending call through
  the *parent's* approval flow (the initiator is the same person). Powerful but
  requires suspending/resuming a nested `toolrunner` across an HTTP round trip —
  meaningful new state machinery (`HandleToolCall` currently resumes only
  post-anchored parent turns).
- **Deny all tools in sub-turns:** simplest, but guts the value — sub-agents are
  useful *because* of their tools.

### Cross-cutting axis 5 — Where the tool lives

- **Built-in (`mmtools`-style) tool, new `delegation` package for the executor
  (recommended):** the resolver needs `*llm.Context` (requesting user, parent
  channel, recursion depth) and in-process services (bots registry,
  `conversation.Service`, `llmcontext.Builder`, prompts). Built-ins receive
  `*llm.Context` natively. Requires: a new wiring path in `server/main.go` and a
  per-agent gate (new `BotConfig` field), since built-ins have no per-agent
  allowlist today. Also keeps delegation *off* the external MCP surfaces —
  an HTTP/stdio MCP client triggering full nested agent loops is a cost- and
  abuse-amplification surface we shouldn't open by default.
- **Embedded MCP tool (`mcpserver/tools`):** would inherit per-tool admin policy
  UI (`ask`/`auto_run_in_dm`/`auto_run_everywhere` + vetted seeds), the
  per-agent `EnabledMCPTools` allowlist, dynamic loading, and user-level
  disablement — all for free. But MCP resolvers get an `MCPToolContext`
  (authenticated `Client4` + user ID), not the LLM context; the executor would
  need a callback endpoint (like `/search/raw`) or an injected service, and the
  tool would automatically appear on external MCP servers unless explicitly
  gated (`Available:`). Viable, and worth revisiting once the policy story
  matters more, but more moving parts for v1.

---

## Part 3 — Converge: recommendation and plan

### 3.1 Recommendation

**Approach D — a built-in `ask_agent` tool that runs the sub-agent in-process as
a persisted, per-user, channel-less sub-conversation — with identity option (i)
+ (iii): act strictly as the requesting user, with an attenuated (auto-run-in-DM
only) tool set for the sub-turn.** Phase the Mattermost-visible surface (B) and
approval bubbling in later, on top of this core.

Why this bundle:

- It answers the 1:1's security concern *structurally* rather than by policy:
  there is no shared channel, no post, and no history object that isn't keyed to
  the requesting user. The "search your own DM history with the sub-agent"
  attack has no object to attack.
- It preserves the product idea's essentials: one pane of glass (Maddie relays
  and synthesizes), sub-agents keep their identities and personas (the sub-turn
  uses the sub-agent's own persona prompt, custom instructions, model, and tool
  grants), and power users lose nothing — direct DM/mention paths are untouched.
- It is the smallest design that doesn't paint us into a corner. Stateless RPC
  (C) would be marginally less code but forfeits audit and continuity; DM
  transport (A/B) spends its complexity budget on surface mechanics and (for A)
  is structurally unsafe.
- It composes with everything that exists: `toolrunner` for the loop,
  conversation entities for storage/audit/ownership, `llmcontext` for identity
  binding, `CheckUsageRestrictionsForUser` for agent ACLs, tool policy tiers for
  attenuation, the pending-approval card for the delegation call itself.

What the user sees in v1: Maddie's response contains a tool card ("Asking
@projects-agent: *what shipped last sprint?*") that the user approves (built-in
default) — or auto-runs once policy plumbing lands — followed by Maddie's
synthesized answer citing the sub-agent. The card links to the delegation
sub-conversation (owner-only, via the existing conversation API) for full
transparency.

### 3.2 Security model (summary)

| Concern | Rule |
|---|---|
| Executor identity | Always the original requesting user: `RequestingUser`, MCP user clients, embedded session, OAuth tokens. Never the bot. Consistent with `docs/features/multiplayer_tool_calling.md`. |
| Agent access | Delegation requires `CheckUsageRestrictionsForUser(subAgent, requester)`; delegation can never reach an agent the user couldn't DM directly. |
| Sub-turn tools | Attenuated to DM-auto-run-eligible tools; no user-interaction tools (automatic via `InteractiveUserPresent=false`); no `ask_agent` (depth limit 1 in v1). |
| History isolation | Sub-conversations are rows keyed by the requesting user, channel-less and post-less; readable only by their owner through the existing ownership-checked API. No new bot-visibility read path anywhere. |
| Channel contexts | If the parent turn is a channel mention, any parent-thread content forwarded into the task inherits the existing redaction rules (`RedactUnshared`); v1 may simply restrict delegation to DMs to sidestep this (open question). |
| Approval | The delegation call itself is a normal tool call: built-in default is a per-call approval card answered by the initiator; policy relaxation is an explicit later step. |
| Injection | Sub-agent output re-enters Maddie as a tool result (already treated as untrusted); the task text enters the sub-agent as user content. No identity elevation exists for injection to exploit, since both sides run as the same human. |
| Audit & cost | Delegation turns persist with tool calls/results; sub-turn LLM usage meters under the sub-agent's own service/bot via existing token tracking, with a new `Operation` constant (`delegation`). Bounded by rounds (min of sub-agent's `MaxToolTurns` and a delegation cap) and a wall-clock timeout. |

### 3.3 Implementation sketch

New/changed components (no code yet; names indicative):

1. **`delegation/` package (new).** `Service` holding the executor: resolve
   target bot (`bots.MMBots`), permission check, recursion/depth guard, build
   sub-context (`llmcontext.Builder.BuildLLMContextUserRequest(subBot, user,
   nil, WithLLMContextTools…)` + tool attenuation via the policy checker),
   render the sub-agent's persona system prompt (`prompts`), create the
   delegation conversation (`conversation.Service`, `Operation: "delegation"`),
   run `toolrunner.Run` with `WriteToolTurns`, drain the stream, return final
   text. Thread `ctx` throughout; add a `telemetry` span (`"delegate to
   agent"`) reusing attribute keys.
2. **The tool.** `ask_agent` (naming open) with args `{agent (username or ID),
   task, context?}`; schema via `llm.NewJSONSchemaFromStruct`. Registered onto
   eligible agents' tool stores; because it needs the `delegation.Service`, it
   is provided through a small provider constructed in `server/main.go` (not by
   widening `MMToolProvider`'s dependencies), added in
   `llmcontext.Builder.getToolsStoreForUser` next to the built-ins.
3. **Config.** New `BotConfig` fields: delegation enable/allowlist (e.g.
   `DelegateAgentIDs []string` with an "all" sentinel), persisted in
   `store/agents.go` (+ migration) and editable in the webapp agent editor
   (`webapp/src/components/agents/`). Plugin-level feature flag for rollout.
4. **Prompts.** A short delegated-task preamble template in `prompts/` framing
   the sub-turn ("You are being consulted by another assistant on behalf of
   @user; answer self-contained, no follow-up questions"), layered on the
   sub-agent's normal persona prompt. Update the tool description with strong
   guidance (when to delegate vs. answer directly; task must be
   self-contained).
5. **Limits & metering.** New `llm.Operation` constant; delegation round cap
   and timeout; metrics counter (`metrics/`); log line per delegation
   (requester, source agent, target agent, conversation ID).
6. **Webapp (minimal v1).** The generic tool card already renders name + args;
   optional polish: friendly rendering for `ask_agent` and a link to the
   delegation conversation.
7. **Recursion guard.** Depth marker carried in the sub-context (e.g. on
   `ToolRuntimeContext`), and `ask_agent` excluded from sub-turn stores.
8. **Tests.** Table-driven unit tests (executor: permission denial, unknown
   agent, self-delegation, depth limit, attenuation correctness — i.e. an
   `ask`-policy tool never auto-runs inside a sub-turn); an eval in `evals/`
   for Maddie's delegation behavior; an e2e spec with two configured agents
   (assigned to a shard in `e2e/scripts/ci-test-groups.mjs`); i18n extraction
   for new user-facing strings.

Phasing:

- **Phase 1 (core):** items 1–8, DM-surface only (delegation available when the
  parent turn is a DM), fresh sub-conversation per delegation, per-call
  approval card, depth 1.
- **Phase 2 (visibility & policy):** admin policy for `ask_agent`
  (auto-run-in-DM tier), optional Mattermost-visible artifact (post the
  delegated exchange as a thread in the *user's* DM with the sub-agent — the
  Approach B rendering), channel-mention support with redaction rules,
  sub-conversation continuity within a parent conversation.
- **Phase 3 (orchestration maturity):** approval bubbling to the parent flow,
  parallel/async delegations, richer agent capability descriptions for routing
  (per-agent "delegation blurb").

### 3.4 Open questions to settle before coding

1. **Approval default.** Ship v1 with the built-in "ask every time" card
   (safest, zero plumbing) or invest immediately in policy config so admins can
   set auto-run-in-DM? Recommendation: ship with ask; measure friction.
2. **Channel mentions.** Allow delegation from channel-mention turns in v1?
   The result becomes channel-visible via Maddie's reply, and parent-thread
   forwarding needs redaction review. Recommendation: DM-only v1.
3. **Continuity.** Fresh sub-conversation per delegation (simple, stateless
   tasks) vs. reusing one sub-conversation per (parent conversation, target
   agent) so follow-up delegations have context. Recommendation: fresh in v1;
   revisit with real usage.
4. **Parent-context forwarding.** Does Maddie pass any parent conversation
   content beyond the task text (and if so, is it the LLM's job to inline it
   into `task`, or a structured `context` arg with server-side redaction)?
   Recommendation: LLM-inlined task text only in v1 — what the model saw, it
   may forward; nothing server-injected.
5. **Tool naming and framing.** `ask_agent` vs `delegate_to_agent` vs
   `consult_agent`; and whether the sub-agent's answer should be attributed in
   Maddie's visible reply by convention (prompt guidance) or mechanically.
6. **Per-agent-pair policy granularity.** Is a flat delegate allowlist enough,
   or do admins need per-pair rules (Maddie→finance requires approval,
   Maddie→projects auto-runs)? Recommendation: flat list v1.
7. **Licensing/packaging.** Delegation only matters with multiple agents, which
   is already license-gated (multi-LLM). Confirm whether the tool itself needs
   an additional gate.
8. **Sub-turn budget interaction.** Cap as `min(subAgent.MaxToolTurns,
   delegationCap)`; does the parent's round budget also decrement per
   delegation? Recommendation: parent spends one round per delegation like any
   tool call, sub-turn budget independent but capped lower (e.g. 10) +
   wall-clock timeout.
9. **Embedded-MCP future.** If/when delegation should be callable by external
   MCP clients (e.g. an IDE assistant asking a Mattermost agent), revisit axis
   5 and expose a gated MCP variant backed by the same `delegation.Service`.
