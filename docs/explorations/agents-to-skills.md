<!--
Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
See LICENSE.txt for license information.
-->

# Agents → Skills: an exploration

> **Status: personal exploration / brainstorm.** This is a diverging design doc,
> not a proposal. It deliberately does **not** recommend an option. Its job is to
> lay out the design space with enough precision — grounded in the current code —
> to decide whether the idea is worth pitching. The blocking question (what does
> an @-mention with skills look like?) is treated as the crux, not papered over.
>
> Provenance: 1:1 discussion about whether "agents" as a feature — agents as
> Mattermost users, each with its own @-mention — was the wrong pattern, and
> whether we should reduce it back to a single agent ("Matty") plus "skills."

---

## Part 1 — How things work today (grounded)

Everything below is verified against `mattermost-plugin-agents` (master),
`mattermost` core, and `enterprise` as of this writing. File references are
plugin-repo-relative unless prefixed with `mattermost/` or `enterprise/`.

### 1.1 An "agent" is a six-part bundle

The single most useful observation for this whole exploration: today's agent
(`llm.BotConfig`, `llm/configuration.go`) is a bundle of six separable things:

| # | Concern | Fields on `BotConfig` | Notes |
|---|---|---|---|
| 1 | **Identity** | `Name` (the @-handle, immutable after create), `DisplayName`, `BotUserID` | 1:1 with a real Mattermost bot user account; avatar lives on the user profile, not the config |
| 2 | **Behavior** | `CustomInstructions` (capped at `MaxCustomInstructionsRunes` = 16384) | Injected into the system prompt (`prompts/standard_personality_without_locale.tmpl`, the `{{.CustomInstructions}}` block) |
| 3 | **Model binding** | `ServiceID`, `Model`, `EnableVision`, `ReasoningEnabled/Effort`, `ThinkingBudget`, `StructuredOutputEnabled`, `MaxToolTurns` | Services (`llm.ServiceConfig`) are already a separate admin-owned object; the agent picks one |
| 4 | **Capability grants** | `DisableTools`, `EnabledNativeTools`, `EnabledMCPTools` allowlist, `AutoEnableNewMCPTools`, `MCPDynamicToolLoading` | Which tools/MCPs this agent may use |
| 5 | **Audience ACL** | `ChannelAccessLevel`+`ChannelIDs`, `UserAccessLevel`+`UserIDs`+`TeamIDs` | Who may talk to it, where (`bots/permissions.go`) |
| 6 | **Ownership/admin** | `CreatorID`, `AdminUserIDs` | Who may edit it (`api/api_agents.go` `canManageAgent`); plus system permissions `manage_own_agent` / `manage_others_agent` |

The "skills" idea is, at bottom, an unbundling proposal: keep #1 singular
(Matty), move #2 + #4 (+ maybe #5/#6) into a new shareable object, and decide
where #3 goes. Framing it this way keeps the later options honest — every
option must say where each of the six concerns lands.

### 1.2 Agents are Mattermost users

- Agents V2 stores agents in the `Agents_UserAgents` table (`store/agents.go`);
  `bots.MMBots.EnsureBots` (`bots/bots.go`) creates/patches a real Mattermost
  bot account per agent and builds its LLM client. User-created agents come in
  via `POST /agents` (`api/api_agents.go` `handleCreateAgent`), which creates
  the Mattermost bot first, then the agent row.
- `config.DefaultBotName` names the default agent ("Matty" is only a
  conventional seed name — `webapp/src/components/system_console/bots.tsx` —
  with no special code path).
- Without a multi-LLM license, self-service agents are capped at
  `FreeTierAgentLimit = 1` (`api/api_agents.go`).

### 1.3 How a conversation reaches an agent

Server-side entry is the `MessageHasBeenPosted` hook →
`conversations.handleMessages` (`conversations/handle_messages.go`):

1. Skip self/remote/system/webhook/wrangler posts, and other bots unless they
   set `activate_ai`.
2. **Mention path**: `bots.GetBotMentioned(post.Message)` (`bots/bots.go`)
   scans the raw markdown text per bot using its own copy of core's mention
   tokenizer (`bots/mentions.go` `userIsMentionedMarkdown`). First matching
   bot wins → `handleMentions` → usage-restriction check → conversation.
3. **DM path**: `GetBotForDMChannel` → `handleDMs`.
4. Otherwise, possibly an ephemeral "mention an agent" nudge
   (`conversations/agent_mention_reminder.go`, with a "loop in" action —
   `conversations/loop_in_agent.go`).

Two facts here matter a lot later:

- **The plugin already does its own text parsing.** Core's mention machinery is
  for *notifications*; the plugin independently re-parses `post.Message`. Any
  plugin-invented syntax after the mention (e.g. `!skill` tokens) is invisible
  to core and entirely parseable by the plugin without core changes.
- **Routing is identity-selection and nothing else.** The only thing the
  mention decides is *which config bundle* (per §1.1) handles the message.

### 1.4 Prompt assembly

System prompt = template (`prompts/direct_message_question_system.tmpl` →
`standard_personality_without_locale.tmpl`) rendering identity, tool guidance,
MCP dynamic-loading workflow, then `{{.CustomInstructions}}`, then requester and
channel context. Assembly happens per turn in
`conversations/handle_messages.go` and `conversations/conversations.go` via
`prompts.Format(...)` with the `llm.Context` built by `llmcontext.Builder`.

### 1.5 Tools & MCP attachment chain

`llmcontext.Builder.getToolsStoreForUser` (`llmcontext/llm_context.go`)
assembles the per-conversation tool store in a fixed pipeline:

1. `bot.DisableTools` → no tools.
2. Built-ins (`mmtools/provider.go`): web search (unless the provider's native
   web search is enabled), `ask_user_question` when interactive.
3. MCP tools for the user (`mcp.MCPToolProvider.GetToolsForUser`), already
   filtered by **admin policy** (`config/mcp_config.go`, per-tool
   `ask` / `auto_run_in_dm` / `auto_run_everywhere` policies via `GetToolPolicy`).
4. **Per-agent allowlist** (`FilterMCPToolsByEnabledAllowlist` against
   `EnabledMCPTools`, unless `AutoEnableNewMCPTools`).
5. **Per-user disables** (`mcp/user_preferences.go` `DisabledServers`, toggled
   from the RHS Tools popover).
6. If `MCPDynamicToolLoading`: a strict registry plus the `search_tools` /
   `load_tool` meta-tools (`mcp/meta_tools.go`) so schemas load just-in-time.

Note the shape: **grant layers already compose** (admin → agent → user), and
the plugin already solved "too many tools bloat the context" with
description-indexed, model-driven just-in-time loading. Both patterns transfer
directly to skills.

### 1.6 Access control & sharing

- Usage: `bots/permissions.go` `CheckUsageRestrictions` (channel level) and
  `UsageRestrictionsForUserConfig` (user/team level), enforced on mentions,
  DMs, and the RHS bot list.
- Management: creator + `AdminUserIDs` + system permissions; non-managers never
  see `CustomInstructions` (`api/api_agents.go` `sanitizeAgentForUser`).
- "Sharing an agent" today = widening `UserAccessLevel`/`UserIDs`/`TeamIDs`
  and/or adding agent admins. There is no share/install object.

### 1.7 Automations

There is **no automations feature inside this plugin**. What exists is an MCP
tool bridge to the separate `com.mattermost.channel-automation` plugin
(`mcpserver/tools/automations.go`):

- Triggers: `message_posted`, `schedule`, `membership_changed`,
  `channel_created`, `user_joined_team` (`AutomationTrigger`).
- Actions: `send_message`, `send_dm`, and — the relevant one — `ai_prompt`
  (`AIPromptActionConfig`): an inline `system_prompt` + `prompt` +
  `provider_type`/`provider_id` (which agent/service runs it) +
  `allowed_tools` + guardrails.

That `ai_prompt` action is *already* almost exactly "a skill with a trigger" —
except the skill body (prompt + tool list + agent choice) is inlined into each
automation instead of being a named, shared, reusable object. The "automations
become skills with a trigger" reframing is therefore largely a normalization:
extract the inline bundle into a referenced entity.

### 1.8 The closest existing precursors to "skills"

| Precursor | What it is | What it lacks vs. a skill |
|---|---|---|
| **Custom Prompts** (`customprompts/store.go`, `docs/features/custom_prompts.md`) | User-authored *message* templates: name, description, template, `IsShared`, per-user pinning; rendered into the composer, optionally prefixed with an agent @-mention | It's a user-turn draft, not behavior: no system-prompt injection, no tool grants, no trigger, no ACL beyond public/private |
| **MCP dynamic tool loading** (`mcp/meta_tools.go`) | Model-driven, description-matched, just-in-time capability loading | Operates on tools, not on prompt/behavior bundles |
| **`ai_prompt` automation action** (§1.7) | Prompt + tool allowlist + agent, fired by a trigger | Inline and per-automation; not named, shared, or reusable |
| **Per-agent MCP grants** (§1.5 step 4) | The "which MCPs come with this behavior" half of a skill | Fused to an agent identity |
| **Service-level completions** | Headless LLM use with no agent at all: core rewrite (`mattermost/server/channels/app/agents.go` `ServiceCompletion`), enterprise autotranslation (`enterprise/autotranslation/provider/agents/provider.go` calls `ServiceCompletion` with a service ID and its own inline system prompt) | Shows that "agent" is only needed when there's a conversational identity; features that just need a model already skip agents entirely |

Also worth noting: this very repo consumes the industry "skills" pattern as
engineering tooling (`.agents/skills/agent-browser/SKILL.md`, `skills-lock.json`
— name + description that a model matches on, plus instructions loaded on
demand). The pattern the 1:1 describes is the pattern coding agents already
standardized on.

### 1.9 The RHS and management UI (plugin-owned)

- RHS registered via `registry.registerRightHandSidebarComponent(RHS, RHSTitle)`
  (`webapp/src/index.tsx`); root at `webapp/src/components/rhs/rhs.tsx`.
- **Agent picker**: `BotDropdown` (`webapp/src/components/bot_selector.tsx`),
  selection persisted as preference `agents/selected_agent`, default fallback
  logic in `webapp/src/bots.tsx` (`resolveActiveBot`).
- New chat = a DM with the selected agent's bot user; thread history
  (`GET /ai_threads`) is cross-agent, labeled per row by bot.
- Tools popover (`webapp/src/components/rhs/tool_provider_popover.tsx`): per-user
  MCP server toggles + OAuth connect, filtered to the active agent's grants.
- Pinned custom prompt buttons on the new-chat tab
  (`webapp/src/components/custom_prompts/rhs_prompt_buttons.tsx`).
- Agent CRUD is a full product page (`registry.registerProduct`, route
  `/plug/mattermost-ai/agents`, `webapp/src/components/agents/agents_page.tsx`)
  with a three-tab editor (Configuration / Access / MCPs).

The 1:1's claim "in the RHS this is clean — it's a selection-menu problem" is
confirmed by the code: every RHS surface involved is plugin-owned and already
has picker/popover components to evolve.

### 1.10 Core touchpoints (where agents leak into `mattermost`)

- **Mention tokenization** (`mattermost/server/channels/app/mention_parser_standard.go`
  `ProcessText`): tokens split on anything that isn't a letter, digit, or
  `:` `.` `-` `_` `@`. So `-` survives inside a token and `!` splits it.
- **Username charset** (`mattermost/server/public/model/user.go`
  `validUsernameChars = ^[a-z0-9\.\-_]+$`, max 64): `@matty-purchasing` is a
  legal username *and* parses as one mention token today; `@matty!purchasing`
  can never be a username and tokenizes as a mention of `@matty` followed by
  the plain word `purchasing`.
- **Autocomplete**: core's `AtMentionProvider`
  (`mattermost/webapp/channels/src/components/suggestion/at_mention_provider/at_mention_provider.tsx`)
  renders a dedicated **Agents** group with an `AgentTag`, pins the default
  agent at the top on empty prefix, and gets its agent list server-side —
  `mattermost/server/channels/api4/user.go` attaches `autocomplete.Agents`
  from `App.GetUsersForAgents`, which resolves **bridge agents to real users
  by username**. There is **no plugin API to extend or re-shape at-mention
  autocomplete**; this surface is core-owned.
- **Agents bridge**: core holds an `AgentsBridge` interface
  (`mattermost/server/channels/app/agents_bridge.go`) implemented over this
  plugin's `public/bridgeclient` (`GetAgents`, `GetServices`,
  `AgentCompletion`, `ServiceCompletion`, min plugin version pinned). Core
  consumers today: at-mention autocomplete, `GET /api/v4/agents`, the message
  rewrite action, the **Recap** modal
  (`mattermost/webapp/channels/src/components/create_recap_modal/` uses an
  `AgentDropdown` + `getDefaultAgent`), and enterprise autotranslation
  (service-level only).
- **Post attribution**: a post's author is a real user ID. Display overrides
  (`override_username`/`override_icon_url`) are honored on the webhook path
  only, and hardened mode rejects client-set overrides. The plugin sets post
  type `custom_llmbot` (`streaming/post_modifier.go` — "must be the only place
  we add this type") and owns its body rendering
  (`webapp/src/components/llmbot_post/llmbot_post.tsx`) — but the post
  *header* (avatar, username) is core-rendered from the authoring bot user.
  One bot user cannot present as multiple visual personas today.

### 1.11 Current-model summary

One sentence: **an agent is a named Mattermost user that bundles a prompt, a
model binding, tool grants, and an ACL; the @-mention or DM channel picks the
bundle; everything else (tools pipeline, conversations, RHS) is keyed off that
choice.** Skills already half-exist as four disconnected fragments (custom
prompts, per-agent MCP grants, dynamic tool loading, `ai_prompt` automations)
that this idea would unify.

---

## Part 2 — What is a "skill", concretely?

### 2.1 The core object (mostly uncontested)

A skill, per the 1:1 framing, is a named, shareable bundle:

- **Name + description** — for humans in pickers *and* for the model when
  routing by intent (exactly the role `SKILL.md` descriptions and MCP tool
  descriptions play today).
- **Instructions** — the system-prompt fragment; "the knowledge to do the
  thing is the prompt in the skill." Direct descendant of
  `CustomInstructions`.
- **Capability grants** — which MCP servers/tools and native tools come with
  it ("which MCPs are automatically enabled with it"). Direct descendant of
  `EnabledMCPTools`/`EnabledNativeTools`. Admin approval policies (§1.5 step 3)
  stay orthogonal: a skill grants *availability*, never approval bypass.
- **Access/sharing** — who can see/enable it ("which people can use a shared
  skill, enable/disable"). Descends from custom-prompt `IsShared` + agent
  `UserAccessLevel`/`TeamIDs`, but becomes install/enable-shaped rather than
  "may talk to the bot"-shaped.
- **Optional trigger** — making "automations = skills with a trigger"
  (§1.7 triggers, plus a new one discussed in §2.4).

### 2.2 The contested fields

These are where the definition genuinely forks; each is an open decision, and
several later options hinge on them.

**(a) Model binding.** Does a skill carry `ServiceID`/`Model`/reasoning
config?

- *In the skill*: preserves today's per-agent multi-backend capability
  ("purchasing runs on the cheap model, coding on the frontier model") without
  agents. But now two active skills can demand conflicting backends in one
  conversation — needs conflict semantics (precedence? refuse? one-call-per-skill?).
- *Not in the skill* (conversation-level model picker, like every chat
  product): clean composition; loses the "the *author* pinned the right model
  for this behavior" property that agents give operators today, unless
  re-added as a soft "recommended model/requirements" hint.
- *Middle ground*: skills declare **requirements** (needs vision, needs long
  context, needs tools) and a resolver picks a satisfying service. New
  machinery; the fallback-chain resolver (`llm.ResolveFallbackChain`,
  `llm/configuration.go`) is a small existing precedent for service-graph
  resolution.

**(b) Identity.** Can a skill *have* an identity (avatar, display name,
its own @-handle), or is identity exclusively the one agent's? This is the
whole Part 4 discussion; flagging here that "skill" definitions that smuggle
identity back in are really "agent with different billing."

**(c) Audience ACL.** Skills clearly need *sharing* ACLs (who can enable).
Do they also need *usage-place* ACLs (this skill only works in these
channels), which agents have today (`ChannelAccessLevel`)? Automations-as-skills
suggests yes (triggers are channel-scoped); pure conversational skills might
not need it. Carrying both makes the object heavier.

**(d) Knowledge/files.** Industry skills bundle files alongside the prompt.
The plugin has embeddings/search infrastructure (`embeddings/`, `search/`)
that a skill could bind a document set to. Probably a v2-of-skills extension;
noting it because it strengthens the "skills are the durable noun"
argument — prompts alone may be too thin to carry a whole product reframing.

**(e) Memory/state.** Out of scope here, but a recurring adjacent request
("the agent that knows our project"). Skills-as-static-prompts don't answer it;
worth acknowledging so the pitch isn't oversold.

### 2.3 Mapping today's objects onto skills

| Today | Under "skills" | Mapping difficulty |
|---|---|---|
| Agent `CustomInstructions` | Skill instructions | Mechanical |
| Agent `EnabledMCPTools` / `EnabledNativeTools` | Skill capability grants | Mechanical |
| Agent `UserAccessLevel`/`TeamIDs`/`ChannelAccessLevel` | Skill sharing ACL (+ maybe place ACL) | Mostly mechanical; semantics shift from "may talk to bot" to "may enable skill" |
| Agent `ServiceID`/`Model`/reasoning | Contested — §2.2(a) | Fork in the road |
| Agent identity (bot user, @-handle, avatar, DM history) | **The unresolved part** — Part 3/4 | Hard |
| Agent `CreatorID`/`AdminUserIDs`, `manage_own_agent` | Skill ownership/admin, `manage_own_skill`(?) | Mechanical, plus permission-schema migration |
| Custom prompt | Either "skill with only a user-turn starter" or a separate surviving feature | Product decision; two overlapping share/pin systems would be confusing |
| Automation `ai_prompt` action (other plugin) | Skill reference + trigger | Cross-plugin refactor; channel-automation is a separate codebase |
| Per-user MCP server disables | Per-user skill enables/disables | Same preference pattern (`mcp/user_preferences.go`) |
| Default bot (`DefaultBotName`) | *The* agent | Trivial — unless multiple agents survive (Part 4), in which case it persists |
| Free-tier agent limit / multi-LLM license gate | ??? — skills need their own packaging story | Non-trivial commercial question, easy to forget |

### 2.4 Activation semantics (new machinery, mostly latent today)

However invocation is solved (Part 3), skills need runtime semantics that
agents never needed, because agents are mutually exclusive and skills stack:

- **Stacking**: multiple active skills = union of tool grants (the pipeline in
  §1.5 already unions layers) + concatenated instructions. Instruction-order,
  contradiction, and token-budget rules are needed — `CustomInstructions` is
  capped at 16k runes *per agent* precisely to bound the system prompt; N
  stacked skills multiply that. The proven mitigation is in-repo: load skill
  *descriptions* always, full instructions just-in-time, exactly like
  `search_tools`/`load_tool` (§1.5 step 6). A `search_skills`/`load_skill`
  meta-tool pair is the obvious mechanical extension.
- **Scope**: is a skill active for one message, a thread/conversation, or
  sticky per user? (RHS conversations have an entity — `LLM_Conversations` —
  that could carry active-skill state; channel mentions are per-thread.)
- **The "intent trigger"**: the 1:1 example — "someone asks for a
  company-style presentation → the company-style skill comes up automatically"
  — reads two ways, and they are very different features:
  1. *Within an addressed conversation* (user already @-mentioned or DM'd the
     agent): this is just model-driven skill routing; cheap, private, and the
     `load_tool` precedent applies directly.
  2. *Ambient in channels* (nobody addressed the agent): this is an
     automation `message_posted` trigger with a classifier — every message gets
     evaluated. Cost, latency, and privacy posture are categorically
     different; today the plugin deliberately does nothing unaddressed except
     the mention-reminder nudge (§1.3).
  The pitch should say which one it means. (1) is the safe reading; (2) is
  the exciting-and-scary one.
- **Trust**: activating a peer's shared skill injects *their hidden text* into
  *your* trusted @Matty's system prompt, and grants their chosen tools to your
  conversation. Custom prompts have a version of this, but they're visible
  user-turn text reviewable before sending. Skills need a vetting/consent
  story (the admin MCP UI's vetted-tool-seed machinery,
  `api/api_mcp.go` `handleGetVettedToolSeed`, is a precedent for "curated
  catalog" thinking). This is also where "which people can use a shared skill"
  from the 1:1 lands: sharing is not just distribution, it's a capability and
  prompt-injection boundary.

---

## Part 3 — The crux: what does the @-mention look like?

This is the piece that has blocked the proposal ("I don't know what that looks
like"). Constraints from Part 1 worth holding while reading the candidates:

- The plugin parses message text itself (§1.3) → **any textual convention
  works functionally with zero core changes**; what needs core is
  *autocomplete, highlighting, and notification semantics*.
- Core at-mention autocomplete is not plugin-extensible (§1.10) → any
  first-class composer affordance is core webapp (and mobile app) work.
- Mentions are also written by **non-humans** — automations, integrations,
  custom prompt templates (which literally prepend `@botname` to rendered
  text today), and mobile clients. A selection mechanism that only exists in
  the desktop composer UI leaves those callers behind; plain text reaches
  everything.

### Candidate M0 — No selection: say what you want, the model routes

`@matty make me a company-style presentation` — Matty's runtime matches the
request against skill descriptions and loads what fits (the `load_tool`
pattern, one level up). No new syntax, no new UI.

- **Naive users**: best possible story; this is "how essentially every other
  agent already works." Nothing to learn, nothing to mis-pick.
- **Power users**: worst determinism. No way to guarantee the purchasing skill
  (with its tool grants) is active; retry-by-rephrasing is the only override.
  Also weak *discoverability*: nothing tells you which skills exist.
- **Multi-backend**: only works if model binding is *not* per-skill, or if the
  router may silently switch backends mid-conversation (unsettling).
- **Feasibility**: near-term; plugin-only. Adds an eval burden (routing
  quality is now a product surface — the `evals/` harness would need routing
  suites) and a transparency need ("Matty used: Purchasing" chips — plugin
  can render these in `custom_llmbot` posts it already owns).
- **Failure mode**: wrong-skill activations erode exactly the trust that makes
  Matty "the default, trusted agent."

### Candidate M1 — Textual modifiers after the mention

`@matty !purchasing !company-style draft the RFP` (bang-tokens; combinable), or
`@matty +purchasing`, or `@matty [purchasing]`.

- **Mechanics**: core tokenizes `!purchasing` as the plain word `purchasing`
  (`!` splits, §1.10) — invisible to notifications, harmless. The plugin
  strips and interprets the tokens. Works today with no core change.
- **Naive users**: invisible until taught; the syntax is a power-user dialect.
  Typos (`!purchasing` vs `!purchase`) silently degrade to plain text unless
  the plugin answers with "unknown skill" feedback (it can — it owns the
  response).
- **Power users / automations / templates**: excellent. Deterministic,
  copy-pasteable, works from mobile, works in custom-prompt templates and
  automation-composed messages, greppable in history ("what did I invoke?").
- **Discoverability/autocomplete**: the real cost. There is no plugin hook to
  add a `!`-triggered suggestion provider; first-class autocomplete means core
  composer work (+ mobile). Without it, M1 is functional but hidden.
- **Bikeshed hazard**: the token charset interacts with the tokenizer — `!` is
  clean *because* it splits; a `@matty-purchasing`-style suffix is a different
  candidate (M2) with different physics.

### Candidate M2 — Skill-scoped handles: `@matty-purchasing`

The mention itself encodes agent + skill. Tokenizer-legal today (`-` survives;
it's also a legal username charset). Two sub-variants with very different
costs:

**M2a — Real generated bot users per shared skill.** Publishing/enabling a
skill auto-provisions `@matty-purchasing` as an actual bot account whose
config *is* the skill (a compiled artifact of skill + base agent).

- This is... today's model, re-derived, with better authoring. That's not
  necessarily damning: it keeps core untouched (mentions, autocomplete via the
  existing bridge agents group, notifications, DM channels, profile popovers
  all just work), gives per-skill visual identity for free, and fixes the
  *authoring* fragmentation even if it keeps the *mention-space*
  fragmentation. But it abandons the "one @-mention" goal, and fleets of
  auto-generated bots recreate "which agent?" confusion with a naming scheme
  on top.

**M2b — Virtual handles (no user behind them).** `@matty-purchasing` resolves
to Matty + purchasing skill at parse time.

- Plugin-side detection is easy (it already scans text per-bot; scanning per
  skill-handle is the same loop). But core knows nothing: no blue highlight,
  no autocomplete entry, no notification keyword, no profile popover, and the
  "out of channel mention" machinery treats it as a nonexistent user. Making
  virtual handles first-class means teaching core a **mentionable non-user
  entity** — new model, new autocomplete source (the bridge already injects
  agents into autocomplete, so there's a seam: `autocomplete.Agents` in
  `api4/user.go` could grow skill handles), new rendering, mobile support.
  That is a deep core investment for what is ultimately syntax.
- Also collides with real usernames: nothing stops a human registering
  `matty-purchasing` today (usernames allow it), so the namespace needs
  reservation rules.

### Candidate M3 — UI picker on mention ("type @matty and a picker comes up")

Composer affordance: after selecting @matty from autocomplete, a second-stage
picker (or a chip row) offers skills; selections attach to the message.

- **Naive users**: strong — discoverable, browsable, no syntax. Matches the
  1:1's popup path.
- **Where does the selection *live*?** Fork with consequences:
  - *As post props*: invisible metadata. Channel readers can't see what was
    invoked without extra rendering; automations/integrations/mobile can't
    produce it without the same UI; and hardened-mode-style prop skepticism
    applies (core increasingly distrusts client-set props).
  - *As canonical text* (picker inserts `!purchasing` or `@matty-purchasing`):
    the picker becomes **sugar over M1/M2**, which keeps one canonical,
    universally-producible form. This hybrid is how slash-command autocomplete
    already behaves (UI assists; text is the artifact).
- **Feasibility**: this is core webapp work (the at-mention provider is
  core-owned) plus mobile app work; the plugin cannot build it alone. Core
  already special-cases agents in autocomplete (Agents group, default pinned,
  `AgentTag`), so there's precedent for agent-aware composer UX — but a
  two-stage mention picker is a new interaction pattern for Mattermost.

### Candidate M4 — Don't overload the mention; use a different surface

`/matty purchasing: draft the RFP` (slash command with skill arg +
autocomplete, which *is* plugin-extensible today), or an "Ask Matty" post/
channel action that opens a scoped composer, or skills chosen on the *reply*
("Matty answered plainly; here's a 'redo with Purchasing' chip on the
response").

- Keeps the mention pure (@matty = the agent, full stop) and puts selection
  where richer UI is allowed. Slash commands get argument autocomplete for
  free (existing core facility, plugin-registerable).
- Cost: bifurcates invocation ("mention for simple, command for scoped"), and
  slash commands are historically low-discoverability and awkward on mobile.
  Post-hoc skill chips on responses are plugin-owned (it renders
  `custom_llmbot` posts) and cheap — likely valuable under *any* candidate as
  the correction mechanism.

### Candidate M5 — Layered hybrid (what most products converge to)

Default = M0 (model routes; naive users never pick anything). Deterministic
override = M1 textual tokens (power users, automations, templates). Optional
sugar = M3-as-text-inserter when/if core composer investment is justified.
Response-side transparency + correction chips (M4's tail end) throughout.

- Listed as a candidate, not a recommendation — but note that it's less a
  compromise than a *sequencing*: M0 and M1 are independently shippable
  plugin-only, M3 is a later core investment that doesn't change the canonical
  form if M1 tokens are the artifact.
- Its open risk: three ways to do one thing is its own confusion tax, and the
  naive/power boundary ("when did I graduate to needing `!`?") is exactly the
  kind of seam users fall into.

### Comparison against the audiences

| | M0 model-routed | M1 text tokens | M2a real bots | M2b virtual handles | M3 UI picker | M4 other surface |
|---|---|---|---|---|---|---|
| Naive user | ★ best | hidden | today's confusion | needs core to be visible | ★ discoverable | low discoverability |
| Power user determinism | ✗ | ★ | ★ | ★ | ★ (if text-canonical) | ★ |
| Automations/integrations/templates | ✗ (can't express) | ★ plain text | ★ plain text | ★ plain text | ✗ unless text-canonical | partial |
| Mobile today | ★ (nothing to build) | works, unassisted | ★ | broken w/o core+mobile work | needs mobile app work | slash: mediocre |
| Core changes required | none | none to function; core for autocomplete | none | deep (mentionable non-users) | core webapp + mobile | none (slash) |
| Multi-backend compatibility | conflicts with per-skill model pins | fine | ★ (per-bot binding survives) | fine | fine | fine |
| Failure mode | wrong skill silently | typo → plain text | bot sprawl returns | dead unhighlighted text | props invisibility (if props) | invocation bifurcation |

---

## Part 4 — Multi-LLM, personas, and the 8–12 month horizon

The pushback tensions, kept live, with what the code says about each.

### 4.1 Multiple backends / models / tool sets

Today this is *the* per-agent superpower: `ServiceID` + `Model` + reasoning
config per agent (§1.1 #3), with services already a separate admin object.
The counterpoint from the 1:1 stands up in code — in the RHS this is genuinely
a menu problem (plugin-owned UI; a model picker next to the existing
`BotDropdown` is straightforward) — and the real question is the @-mention.
Under each Part 3 candidate:

- **M0**: model binding must move off skills entirely (conversation-level or
  router-chosen), else routing implies silent backend switches.
- **M1/M2b/M3**: a skill *may* pin a service; conflicts arise only when
  stacking skills with different pins → needs a rule (last-wins, priority
  field, refuse-with-message, or per-skill sub-calls — the last one drifts
  toward the delegation mechanism in §4.3).
- **M2a**: per-bot binding survives untouched — the strongest answer to this
  tension, at the cost of being the least "skills-y" option.
- A fourth possibility orthogonal to invocation: **agents shrink to model
  bindings** ("bodies"), skills carry all behavior ("knowledge") — a small
  fixed set of agents (Matty-fast, Matty-deep) with a shared skill library.
  This serves multi-backend explicitly but keeps two nouns, and two mentions.

Also honest to note: headless consumers (rewrite, recap, autotranslation)
already bind to *services*, not agents — the multi-backend need is partially
served today without agent identity at all (§1.8, last row).

### 4.2 Personas: confusion vs. genuine need

Both sides, explicitly:

- *For reduction*: one agent acting in multiple personas is what confuses
  people today ("which agent do I pick?"); every mainstream agent product is
  single-persona + capabilities; the default-agent pinning in core
  autocomplete (§1.10) already nudges toward "just talk to the default one."
- *Against reduction*: distinct personas carry real weight —
  - **Attribution/audit**: a bot user is a *principal*. Posts, sessions, and
    audit trails hang off it. "The prod agent said/did X" is legible in a way
    "Matty (running prod skill) did X" is not, unless we build attribution
    surfaces. Core post attribution cannot decorate one bot as many personas
    (§1.10: `override_username` is webhook-only and being hardened away).
  - **Membership**: agents-as-users can be channel/team members — "add the
    triage agent to this channel" is an ACL and a mental model. Skills have no
    equivalent unless place-ACLs are added (§2.2c).
  - **Inbox**: a DM with @prod-agent is a place. Collapsing to Matty merges
    all agent relationships into one DM thread-list (the RHS conversation list
    partially compensates — it's already cross-agent).
  - The plugin *can* render "responding with: Purchasing" inside the message
    body it owns (§1.10), which covers transparency but not identity.

The unresolved middle: is persona a *rendering* problem (solvable with chips
and headers) or an *identity* problem (requires a principal)? Security/audit
folks will say identity; UX folks will say rendering. Both are right for
different agent populations, which motivates the hybrid architectures below.

### 4.3 The fleet horizon (8–12 months)

Premise accepted: today's power users are next year's general case; there will
be fleets of purpose-built agents (prod, triage, mission agents) where
distinct representation genuinely matters. How each architecture serves that
world:

- **Pure single-agent + skills**: fleets become skill libraries + triggers.
  Orchestration = "I ask my single agent to orchestrate them" — the
  **Matty-delegates-to-sub-agents tool being explored separately** is one
  concrete mechanism for this; skills compose with it naturally (a skill can
  carry the delegation instructions and the roster of sub-agents to fan out
  to). Not re-derived here. The gap: sub-agents in that model still need *some*
  runtime identity for audit, which pure-skills doesn't provide.
- **Skills-first with optional embodiment**: skills are the unit of authoring
  and sharing; an identity (bot user) can be *granted to* a skill when it
  graduates to needing one (a principal for audit, a channel member, an A2A
  endpoint). Fleet agents are embodied skills; casual behaviors never are.
  This is the architecture that treats §4.2's fork as per-skill rather than
  global — and it is also, mechanically, M2a applied selectively.
- **Status-quo-plus**: keep agents; fleets are agents (as today); skills are
  an additive sharing/composition layer *inside* agents. Serves the horizon
  trivially; does the least to fix the present confusion.

The horizon cuts both ways and should be argued both ways in any pitch: "in a
year everyone will have many agents" argues *for* strong multi-identity
support, and *also* for a single trusted front-door that hides the fleet
(because nobody will be able to pick from 40 bots by hand).

---

## Part 5 — What a migration would actually entail

Scoped for the *pure* reduction (delete agents, one Matty, skills); the hybrid
architectures shrink this list — noted inline. No effort estimates; this is a
what-moves inventory.

### 5.1 Plugin (`mattermost-plugin-agents`)

- **New skill entity**: table + store (patterns: `store/agents.go`,
  `customprompts/store.go`), CRUD API, ownership/permissions
  (`manage_own_agent` → skill equivalents), sharing/enable model, and the
  Part 2 activation semantics (stacking, scope, budget). Genuinely new: the
  skill router (M0) and/or token parser (M1) and the conflict rules.
- **Prompt assembly** (`prompts/`, `llmcontext/`): system prompt gains a
  skills section (active-skill instructions or JIT-loadable skill registry à
  la `search_tools`).
- **Tool pipeline** (`llmcontext/llm_context.go`): per-agent allowlist layer
  (§1.5 step 4) becomes union-of-active-skills layer; per-user disables
  extend to skills.
- **Conversations** (`conversation/`, `conversations/`): conversation rows
  gain active-skill state; `GetOrCreateConversation` keying stays (it's
  bot+root keyed — with one bot the key degenerates gracefully).
- **RHS webapp**: `BotDropdown` → skill enable/disable menu (the
  `ToolProviderPopover` is the closer template than the bot picker — toggles,
  not exclusive choice); Agents product page → Skills page (the three-tab
  editor maps: Configuration→instructions, MCPs→grants, Access→sharing).
- **Custom prompts**: converge or coexist (§2.3) — a product decision with UI
  consequences either way.
- **Automations**: the `ai_prompt` action refactor toward skill references is
  a **cross-plugin change** (channel-automation is a separate plugin calling
  back into this one). Needs its own compatibility plan.
- **Bridge back-compat** (`public/bridgeclient`): `GetAgents` must keep
  returning something sane to older cores (the bridge already does min-version
  pinning core-side; the plugin must keep the agent-shaped API alive for at
  least one core release cycle, likely returning the single agent).
- **Licensing/packaging**: `FreeTierAgentLimit` and the multi-LLM gate need a
  skills-shaped replacement — currently the *only* monetization hook on this
  surface. Easy to forget, commercially load-bearing.

### 5.2 Core / webapp (`mattermost`)

- **At-mention autocomplete**: Agents group collapses to one entry (fine,
  no change needed) — *unless* M2b/M3 is chosen, which is where the deep core
  work lives (mentionable non-user handles, two-stage picker, mobile).
- **Consumers of `GetAgents`**: Recap modal's `AgentDropdown`, rewrite,
  `GET /api/v4/agents` — all degrade gracefully to a one-item list, but the
  Recap agent picker probably *wants* to become a skill picker eventually
  (new bridge surface: `GetSkills`).
- **Enterprise autotranslation**: unaffected (service-level).
- **Permissions**: `manage_own_agent`/`manage_others_agent` exist in core's
  permission schema; skills need additions and a deprecation path.

### 5.3 Data migration & user-facing breakage (the hard part)

- **Agent rows → skills**: mechanical for instructions/grants/ACLs (§2.3).
- **Bot user accounts**: the painful one. Options: (a) deactivate (like agent
  deletion today) — **strands every existing DM history**: conversations live
  in DM channels with each bot user, and users keep a dead DM thread-list;
  (b) keep the bots alive as *aliases* that forward to Matty+skill (M2a-lite,
  preserves history and old @-mentions in posts, but the fleet never visually
  shrinks); (c) keep them read-only with a farewell/redirect message. There
  is no option where old `@purchasing-bot` mentions in message history point
  at anything meaningful under pure reduction.
- **Selected-agent preferences** (`agents/selected_agent`), pinned prompts
  referencing agents, automations referencing `provider_id` — all need
  remapping.
- **Trust boundary shift**: N agents with separate tool grants each is a
  coarse sandbox; one Matty with user-togglable skill grants is a different
  security posture that admins must be able to reason about (per-skill grant
  review UI, vetting — §2.4).

### 5.4 A note on staging (not a recommendation)

The inventory above is separable: skills can ship **additively** (a shareable
prompt+grants object that the *existing* default agent consumes), with
agent-deletion as a later, independent decision. That sequencing exists and
matters for risk — but choosing it *is* choosing architecture "Additive"
below, at least temporarily, and interim states have a way of becoming
permanent. Flagged as a decision, not a plan.

---

## Part 6 — The tension matrix

Coherent end-state bundles (invocation choice mostly orthogonal; assume M5-ish
for A/B unless noted):

- **A. Full reduction** — one agent; skills carry behavior+grants+sharing+
  triggers; no per-skill identity.
- **B. Skills-first, optional embodiment** — skills primary; a skill can be
  granted a bot identity when it needs to be a principal/member.
- **C. Additive skills** — agents stay; skills are a new shareable layer both
  agents and conversations can attach.
- **D. Null hypothesis** — no new noun; fix confusion with default-agent
  routing + delegation (the separately-explored sub-agents tool) + better
  agent management.

| Tension | A: Full reduction | B: Optional embodiment | C: Additive | D: Null |
|---|---|---|---|---|
| Multi-LLM/backends | Must solve conversation-level or per-skill model binding + conflicts | Embodied skills keep bindings; unembodied need A's answer | Untouched (agents keep bindings) | Untouched |
| "Which agent?" confusion | Eliminated by construction (one front door) | Mostly eliminated; embodied skills reintroduce *some* choice | **Worsened** (agents *and* skills to understand) unless UI hides one | Addressed only via routing/defaults |
| Persona/audit/membership needs | Unserved (rendering-level chips only) | Served per-skill, on demand | Served (agents remain principals) | Served |
| Fleet horizon (8–12 mo) | Fleets = skills+triggers+delegation; audit gap | Strongest story: fleet agents = embodied skills | Fleets = agents as today | Fleets = agents as today |
| @-mention problem | Must be solved (Part 3) — load-bearing | Must be solved for unembodied skills | Optional (skills can be RHS/DM-only at first) | N/A |
| Migration risk | Highest (DM history, bots, bridge, license) | High but incremental | Lowest | None |
| New-noun budget | Net −1 nouns *if* agents & automations truly deleted | Net 0 (skills in, agents demoted not deleted) | **Net +1** (everything coexists) | 0 |
| "Trusted Matty" story | Strongest | Strong | Diluted (Matty is one agent among many) | Depends on defaults |
| Naive user (day 1) | Best (with M0/M5) | Good | Unchanged | Unchanged |
| Power user (day 1) | Loses per-agent determinism unless M1+ | Keeps most | Keeps all | Keeps all |

Nothing in this table is decisive, which is the honest finding: the idea's
value hinges on (i) whether the @-mention crux has an acceptable answer, and
(ii) whether persona/audit needs are per-skill (→ B is coherent) or global
(→ the reduction is fighting reality).

---

## Part 7 — Open questions & decisions needed

Questions that need answers before this could become a real proposal —
grouped by who can answer them.

### Decisions to make (product/strategy — asking for these explicitly)

1. **Is deletion actually on the table?** Full reduction (A) vs. additive (C)
   vs. embodiment (B) are different pitches with different risk. The 1:1
   framing says "delete agents as a feature"; §5.3 says the deletion is where
   nearly all the migration pain lives. Which endpoint is the pitch?
2. **What's the core-change budget?** M2b/M3 (virtual handles, mention
   picker) require core + mobile investment; M0/M1/M4 are plugin-only. If the
   answer is "plugin-only for now," the @-mention design space shrinks to
   M0/M1/M4/M5-without-picker — worth knowing before polishing candidates.
3. **Does a skill carry a model binding?** (§2.2a). This single schema
   decision determines whether the multi-backend tension is solved, punted to
   a conversation-level picker, or handed to a resolver. It also decides
   whether option A can serve today's "cheap model for X, frontier for Y"
   operators at all.
4. **Which reading of the intent trigger?** In-conversation routing vs.
   ambient channel classification (§2.4). The second is a different product
   (and privacy posture) hiding inside a bullet point.
5. **Custom prompts: converge or coexist?** Two shareable, pinnable prompt
   objects is one too many; but custom prompts are user-turn drafts and skills
   are behavior — merging them may conflate composing-help with capability.

### Questions to investigate (answerable with more digging, not opinion)

6. What do the *existing* multi-agent deployments actually use per-agent
   config for? (Telemetry/field data: how many agents per install, how often
   do they differ in service/model vs. only in instructions/tools?) If most
   fleets differ only in prompt+tools, skills subsume them; if they differ in
   backends, §4.1 is load-bearing.
7. How much of the "which agent?" confusion is *selection* confusion vs.
   *capability* confusion ("I don't know what any of them can do")? Skills
   fix the first by construction; the second needs descriptions/discovery
   regardless of architecture (and D would fix it too).
8. What does the separately-explored delegation tool assume about sub-agent
   identity? If it needs real principals, B's embodiment mechanism and the
   delegation roster should be co-designed rather than converging late.
9. Cross-plugin blast radius of the automations refactor (§5.1): who owns
   channel-automation's roadmap, and can `ai_prompt` grow a skill reference
   without a lockstep release?
10. What would routing-quality evals look like (M0)? The `evals/` harness
    exists; a routing suite is a precondition to trusting model-driven
    selection with tool grants attached.

### The sharpened version of the crux (where this doc lands)

The @-mention question decomposes into two independently answerable halves:

- **Canonical form**: what is the *artifact* in the message that selects
  skills — nothing (M0), text tokens (M1/M2), or metadata (M3-props)? Text is
  the only form every producer (humans, mobile, automations, templates) can
  emit today; metadata is the only form that's invisible-by-default; nothing
  is the only form naive users will actually use.
- **Assistance layer**: how do humans discover and produce that form —
  unassisted, composer UI (core work), or model-side routing?

Framing it as canonical-form × assistance separates the plugin-shippable
decision from the core-investment decision — which is likely the most useful
single reframe this exploration produced, and the first thing to pressure-test
if this moves toward a pitch. Deliberately left open: which combination to
pick.
