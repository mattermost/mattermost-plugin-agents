# Agents product view — implementation plan

Port three things from the Agents vision prototype
(`mattermost/mattermost-proto-playground` PR #62) into
`mattermost/mattermost-plugin-agents`:

1. An **Agents product view** with a left sidebar (agents, their chats, Custom Prompts).
2. A **single-thread agent chat** in the center pane — one root post plus its
   replies, rendered as a continuous chat.
3. **Custom Prompts** as a full center-pane page, not only the modal reachable
   from the channel composer.

All changes, including this plan document, live on a new branch cut from the
**latest** `origin/master` (`git fetch origin master` first). Plugin branch:
`agents-product-view` (created). The host change (Phase 5b) likewise gets its own
branch from the host repo's latest `origin/master`. Create branches without
upstream tracking of `master`. Never push to `master`; open a PR.

---

## 0. Read this before writing code

### Repo conventions that apply to every phase

From `/AGENTS.md` and `webapp/AGENTS.md`:

- **A fresh checkout does not compile.** Run `make apply` first (generates
  `server/manifest.go`).
- **compass-ui for components, styled-components for styling.** Use
  `@mattermost/compass-ui` for every interactive control that has a compass
  equivalent. Use styled-components only for layout, `--spacing-*` spacing, and
  restyling compass components (e.g. `styled(IconButton)`). Never inline
  `style={{...}}`.
- **Use `@mattermost/compass-ui` components** (buttons, icon buttons, tabs,
  modals, menus) rather than hand-rolled interactive elements. Anything
  clickable must be keyboard-operable and have an accessible label.
- **Spacing uses compass `--spacing-*` tokens**, not raw px.
- **Port the prototype's compass-ui usage directly.** The prototype
  (`~/Documents/GitHub/mattermost/mattermost-proto-playground`) is built on
  compass-ui, so wherever it uses a compass component, use the same component
  with the same props and variants. Do not re-create it or substitute another.
  Translate only the prototype's own custom markup and styling.
  **Version caveat:** the plugin pins `@mattermost/compass-ui` `0.1.0-alpha.14`
  (`webapp/package.json`); the design checkout
  (`~/Documents/GitHub/mattermost/compass-design`) is at `alpha.22`. Before
  porting a component or prop, confirm it exists in the installed
  `webapp/node_modules/@mattermost/compass-ui` `.d.ts`. If it doesn't, stop and
  report; the user is bumping the pin themselves (its own change, with
  `make check-locks`), so re-check the installed version before the audit and
  before each port; a styled stand-in only if a component is still missing
  after the bump.
- Host CSS variables like `--border-light` are whole shorthands:
  `border-bottom: var(--border-light)`, never `1px solid var(--border-light)`.
- **File names are `snake_case.tsx`.** Components are PascalCase.
- **All new user-facing strings go through i18n** (`FormattedMessage` or
  `intl.formatMessage`). Never hand-edit `webapp/src/i18n/en.json` —
  `make check-style-fix` re-extracts it.
- The `agents/*` ESLint rules resolve styled-components declared **in the same
  file** to their DOM tag. Styled components imported from another file are
  invisible to them. Declare any styled wrapper that carries a handler in the
  same file as the handler. Use a bare `styled.button` only where compass-ui has
  no fit, and say why in a comment.
- Async loaders must ignore responses from superseded requests, so a stale
  response never overwrites newer state.
- Plugin modals sit at z-index 2000, their menus at 2100. Use
  `TooltipAboveModals` from `@/components/dropdown` inside modals.
- Popover menus animate through compass `PopoverMenu` `open`/`onExited`; a
  `Dropdown` must stay mounted until `onExited` (`useMenuTransition`).
- **No `Co-Authored-By` lines** in commits. Commit subject is one succinct line.
  Use the GitHub PR template for the PR body.
- Before the PR: `make check`. If it fails, isolate with `make check-style`,
  `make test`, `make check-shards`, `make check-i18n`, `make check-locks`.
- If you add an e2e spec, assign it in `e2e/scripts/ci-test-groups.mjs` in the
  same change (`make check-shards` validates this).

### Server-side rules, if any phase touches Go

- Every **state-changing** route needs an audit event constant and registry row
  in `api/audit_events.go`. Never put content (prompts, messages, tool args)
  in an audit record — object identifiers only.
- Thread `ctx context.Context` as the first parameter through entry points.
- Persist references to agents, bots, users, channels **by ID, never by name**.
- Fail closed: a missed lookup returns an error, never a default or first match.
- JSON returned to the webapp encodes empty slices as `[]`, not `null`.

---

## 1. What already exists — do not rebuild it

This is the single most important section. The plugin already implements most of
what the prototype mocked. Reuse it.

### The Agents product is already registered

`webapp/src/index.tsx`:

```js
if ((registry as any).registerProduct) {
    (registry as any).registerProduct(
        AGENTS_ROUTE, ProductSwitcherIconAI, agentsProductLabel,
        AGENTS_ROUTE, AgentsPage,
    );
}
```

- `AGENTS_ROUTE = /plug/${manifest.id}/agents` (from
  `webapp/src/components/agents/agents_page.tsx`).
- `AgentsPage` currently renders only `AgentsList`.
- `AgentsPage` already carries two host workarounds worth preserving: it adds
  `channel-view` to `#root` (product views don't load `ChannelController`, so
  the global header loses themed colors), and it pins `overflow-y` on scrolling
  ancestors.

### Agent CRUD already exists

In `webapp/src/components/agents/`: `agents_list.tsx`, `agent_config_view.tsx`,
`agent_row.tsx`, `agent_version_history_modal.tsx`, `delete_agent_dialog.tsx`,
`tabs/`, `use_agent_version_history.ts`, `version_snapshot.tsx`.

The prototype's "New Agent" modal and "Agent Settings" overlap with
`agent_config_view.tsx`. **Reuse and restyle it. Do not port the prototype's
version.**

### Chat already works — in the RHS

`webapp/src/components/rhs/rhs.tsx` is the reference implementation for
everything the center pane needs:

| Need | Existing mechanism |
|---|---|
| Thread list | `getAIThreads()` from `@/client` → `AIThread[]` |
| Render a thread | host `ThreadViewer` with `rootPostId` |
| Start a new chat | `AdvancedTextEditor` on the bot's DM channel, `rootId=''` |
| Select a thread | `dispatch({type: 'SELECT_AI_POST', postId})` |
| Read state | `updateRead(userId, teamId, postId, Date.now() + 24h)` |
| Conversation id for a thread | `useConversationIdForThread(selectedPostId)` |
| Agent list | `useBotlist()` → `{bots, activeBot, setActiveBot}` |

`AIThread` shape (declared in `rhs.tsx`):

```ts
export interface AIThread {
    id: string;
    channel_id: string | null;
    bot_id: string;
    root_post_id: string | null;
    title: string;
    turn_count: number;
    update_at: number;
}
```

Host components come from `webapp/src/mm_webapp.ts`, which reads
`window.Components`: `ThreadViewer`, `AdvancedTextEditor`, `CreatePost`,
`Timestamp`, `PostMessagePreview`.

`webapp/src/components/rhs/rhs_new_tab.tsx` shows the new-chat flow: if the bot
has no `dmChannelID`, call `getBotDirectChannel(currentUserId, botId)` to create
it, update the bots list in Redux, then render `AdvancedTextEditor` with
`channelId={botChannelId}`, `rootId={''}`, `location={'RHS_COMMENT'}`,
`isThreadView={true}`, and read the new post id from
`afterSubmit(result.created.id)`.

### The thread model already matches the design

`conversations/handle_messages.go`, `handleDMs`:

```go
responseRootID := post.Id
if post.RootId != "" {
    responseRootID = post.RootId
}
```

So **every top-level post in an agent DM already starts its own thread**, and
`conversation/service.go` keys a conversation by `(RootPostID, BotID, UserID)`.
One root post plus its replies is already one conversation. **No server model
change is needed for the single-thread center pane.**

Titles are generated asynchronously after the first turn
(`Service.GenerateTitle`), so a brand-new thread's title is empty for a moment.

`GET /conversations/{id}` (`api/api_conversation.go`) returns the conversation
plus its turns, with privacy filtering and `approval_state` per turn.

### Custom Prompts is complete on the server

`api/api_custom_prompts.go` already has create, list, update, delete, pins, and
render. It is user-scoped, audited, and license-gated:

- Listing includes shared prompts only when the license allows
  `enterprise.CapSharedPrompts`; below that, only the caller's own prompts.
- Creating or flipping a prompt to shared requires `CapSharedPrompts`.
- Pinning someone else's prompt requires `CapSharedPrompts`.
- Only the creator can update or delete.

**No server work is required for the Custom Prompts page.**

Webapp side: `webapp/src/components/custom_prompts/`

- `custom_prompts_management.tsx` (~21KB) — all list/create/edit/pin/delete
  logic *and* the modal shell, in one component. Mounted via
  `registerRootComponent`, shown by the Redux flag `ShowCustomPromptsModalHandler`.
- `custom_prompt_form.tsx` — already separate, already supports `readOnly`,
  `stickyFooter`, `onSave`, `onDiscard`, `onDelete`.
- `custom_prompts_dropdown.tsx` — the composer menu, registered via
  `registerAIActionMenuItemComponent`. Its "Manage prompts" item sets the Redux flag.
- Redux: selectors `getCustomPrompts`, `getPinnedPromptIds`,
  `getShowCustomPromptsModal`; actions `fetchCustomPrompts`, `fetchPinnedPromptIds`.
- Client: `createCustomPrompt`, `updateCustomPrompt`, `deleteCustomPrompt`,
  `setCustomPromptPin`, `renderCustomPrompt`.

### Do not port these from the prototype

- Scripted tool-connect cards and fake OAuth — the real MCP OAuth and tool
  approval flows already exist.
- In-memory `AgentsContext` sessions — server conversations replace them.
- The prototype's demo fixtures (`agentsData.ts`, Priya/Sentinel/Matty scripts).
- `IncidentChannel`, `ChannelsHome`, the whole `agents-docs/` prototype,
  `MattyPanel`/`MattyFab`, `AgentComputerPip`, `PlaybookRunRhs`, the
  `src/walkthrough/` runtime.

### Scope limit: the Channels-side DM is probably not buildable in the plugin

The prototype also puts an Agents category in the Channels LHS and replaces the
Channels center pane with its own chat. Both are host-owned surfaces. The plugin
registry very likely cannot do either. **Treat the Channels DM redesign as out of
scope for this branch** and file host requests separately. Everything below lives
inside the plugin-owned Agents product view.

---

## 2. Target shape

```
/plug/mattermost-ai/agents                      → All agents (existing AgentsList)
/plug/mattermost-ai/agents/prompts              → Custom Prompts page
/plug/mattermost-ai/agents/chat/:botId          → New chat with that agent
/plug/mattermost-ai/agents/chat/:botId/:rootId  → One thread, as a chat
```

Routes are the preferred design because they give deep links. **They depend on
`registerProduct` passing sub-paths to the main component — verify in Phase 0.**
If it doesn't, fall back to local state in `AgentsPage` and lose deep links.

LHS layout, following the prototype:

```
Agents                    [+]
  All agents
  Custom Prompts
  ──────────────
  YOUR AGENTS           [+]
   ▾ Matty              [+]
       Thread title 1
       Thread title 2
   ▸ Sentinel           [+]
```

- The `[+]` next to an agent starts a new chat with it.
- The chevron collapses that agent's thread list.
- Clicking an agent with threads opens its most recent thread; clicking one with
  none opens the new-chat state.
- **Omit the prototype's inert rows** (Skills, Connectors, Boards, Playbooks).
  Don't ship dead navigation.

---

## 3. Review findings that change the plan (reviewed against the code)


| # | Finding | Impact |
|---|---|---|
| 1 | `getAIThreads` returns **all** the user's conversations, DM and channel-mention, ordered by `UpdatedAt`; hard cap **60**, no pagination (`api/api.go:688`, `store/conversations.go:154`). `channel_id` is the DM channel for DM threads, the real channel for mention threads. | Filter DM threads with `channel_id === bot.dmChannelID`, **fail closed** if `dmChannelID` unknown. Per-agent lists truncate silently and mention traffic crowds out DM threads → add optional `limit`/`offset` (small Go change) or accept the cap. |
| 2 | **No websocket event** fires on thread creation or title generation (`conversation_updated` is listened for in `index.tsx:190` but never published). | Phase 3 "refresh on title" has no trigger. Default: refetch after `afterSubmit`, plus a delayed refetch for the title, plus refetch on `postupdate` for DM posts. New server event only if that proves flaky. |
| 3 | Plugin uses **no react-router**; navigation is `window.WebappUtils.browserHistory.push(...)`. Whether the host passes sub-paths to the `registerProduct` main component is not statically resolvable. | Routing primary approach: read `browserHistory.location` + `listen`, push via `browserHistory` (no new jest mocks). react-router only if the spike proves it works, and then every test needs a `{virtual: true}` mock (see `index.test.tsx`). |
| 4 | `updateRead` needs `currentTeamId` (`rhs.tsx:68`); product routes sit outside `/:team/`. | Spike must confirm team context; fall back to last-viewed team. |
| 5 | `agents_page.tsx` overflow-pinning + absolute wrapper assume one scroller. | Chat pane needs its own scroll region; re-check workarounds in Phase 1. |
| 6 | `e2e/tests/custom-prompts/custom-prompts.spec.ts` already covers the modal; no jest test for `custom_prompts_management`; plan lacked tests for new components/hook and any e2e for the product view. | Add tests per unit, keep the existing e2e green, add one product-view e2e with a shard entry in `e2e/scripts/ci-test-groups.mjs`. |
| 7 | `AIThread` is declared in `rhs.tsx`; importing it from a hook creates a cycle. | Move to `webapp/src/types/` first. |
| 8 | `agents/*` lint rules only see same-file styled-components. | Compass-ui first; any styled wrapper carrying a handler is declared in the same file as the handler; bare `styled.button` only where compass-ui has no fit (matters for the content/page split). |
| 9 | Sidebar has no narrow-width behavior. | Add collapse state for the host mobile breakpoint. |
| 10 | The harness asks for a `Co-Authored-By` trailer; repo `AGENTS.md` forbids it. | Repo rule wins: **no `Co-Authored-By`, no "Generated with Claude Code" line** in commits or the PR. Subagents are told this explicitly. |

### Orchestration: who does what, on which model

Principle: spend the expensive model where a wrong call is costly (unknown-host integration, final review), use a mid model for well-specified implementation, a cheap model for mechanical or read-only chores. The main session (orchestrator) stays thin: it writes briefs, reads reports, verifies diffs, and owns all commits.

| Model | Used for | Why |
|---|---|---|
| **Opus 5.5** | Phase 0 spike (host unknowns, judgment under ambiguity); Phase 3 chat page (host `ThreadViewer`/`AdvancedTextEditor` integration, read state, new-chat flow); the single final cross-cutting review (convention, authz, fail-closed, integration); host-PR review | Hardest, least-specified work and the highest-stakes review; mistakes here are costly |
| **Sonnet 5.5** | Phase 1 shell, Phase 2 extraction + tests, Phase 3a hook, Phase 3c LHS threads, Go pagination, e2e spec, Phase 4 polish | Well-specified edits against clear patterns; best cost/quality for code |
| **Haiku 5.5** | Pre-flight (`make apply`, baseline lint), i18n/lockfile drift fixing, shard-assignment check, `make check` triage reports, PR body drafting from the template | Mechanical, command-driven, low reasoning demand |

Rules for every subagent brief:
- Name the files it **owns**; it touches nothing else. No two concurrent writers share a file.
- Port prototype compass-ui usage 1:1 (same components, props, variants); check the installed `alpha.14` `.d.ts` first and report anything missing rather than substituting.
- Include the conventions block from the original plan §0 (compass-ui for interactive controls, styled-components for layout/restyling only, no inline styles, `--spacing-*`, snake_case files, i18n, same-file styled handlers, superseded-response guard).
- "Do not commit, do not switch branches." The orchestrator commits.
- Report: files changed, tests added, commands run with results, open questions. Under ~300 words.
- Verification scoped per the user's preference: per-subagent run only its own jest suites plus `eslint` on its files; **no full-suite runs** until the final gate. SCSS/CSS-only changes get stylelint + browser check, not jest.
- Parallel writers run with `isolation: "worktree"` so edits cannot collide; orchestrator merges the branches in order.

### Execution graph

```
Wave 0  (sequential, gate)
  [Haiku]  Pre-flight: branch, `make apply`, baseline `make check-style` result
  [Opus ]  Phase 0 spike  ──► findings note + routing/refresh/limit decisions ──► USER CHECKPOINT

Wave 1  (parallel, disjoint files, separate worktrees)
  [Sonnet] A: Phase 1 shell + sidebar (agents_page.tsx, product_sidebar*.tsx) + jest tests
  [Sonnet] B: Phase 2a/2b custom prompts: management tests first, then extract content (custom_prompts_*.tsx)
  [Sonnet] C: Phase 3a: move AIThread type, use_agent_threads.ts (+test), DM filter by dmChannelID, refresh strategy
  [Sonnet] D: (conditional) Go `limit`/`offset` on /ai_threads + table-driven Go test
           — only if the spike says the 60 cap matters

Wave 2  (after A merged; B2c wiring + chat can overlap, disjoint files)
  [Sonnet] B2: custom_prompts_page.tsx + wire into shell route
  [Opus  ] E: Phase 3b agent_chat_page.tsx (ThreadViewer, new-chat composer, read state)
  [Sonnet] F: Phase 3c agent_threads_list.tsx + product_sidebar.tsx "Your agents" section (needs A and C)

Wave 3  (sequential)
  [Sonnet] G: Phase 4 polish (header, composer, empty state, avatars, narrow-width collapse)
  [Sonnet] H: product-view e2e spec + `ci-test-groups.mjs` shard entry; keep custom-prompts e2e green
  [Haiku ] I: `make check-style-fix` (i18n re-extract), `make check-i18n`, `make check-locks`, `make check-shards`; report diffs

Wave 4  (gate)
  [Opus  ] J: review of the full branch diff vs. AGENTS.md rules (authz, fail-closed, error propagation, ID-not-name, audit if Go changed, a11y, i18n, test quality)
  [Sonnet] K: fix J's confirmed findings
  [Haiku ] L: `make check`, triage any failure to the failing target; draft PR body from template
  Orchestrator: commit sequence, push branch, open PR (no Co-Authored-By)
```

Merge order after parallel waves: A → C → B → D → E → F. Conflicts are expected only at `product_sidebar.tsx` and the route wiring; F owns that file.

### Phases (revised)

### Phase 0 — spike (Opus; scratch only, no production UI)

Answer, with evidence from running the plugin where possible (`make deploy` against a local server):
1. Does host `ThreadViewer` work full-width in a product view (layout, own scroll region, streaming updates, hover actions)? **If not, stop and report**; the custom post-list fallback changes the estimate.
2. Does `AdvancedTextEditor` work there, including `afterSubmit`?
3. Does `registerProduct` pass sub-paths? Test `browserHistory.location` + `listen` first (preferred), react-router second.
4. Is a team id available for `updateRead` in a product view? Fallback options.
5. Refresh strategy: confirm no event fires; try `postupdate`-driven refetch and refetch-after-submit + delayed title refetch; recommend one.
6. Does the 60-row cap matter in practice? Recommend accept vs. `limit`/`offset`.
7. `bot.dmChannelID` absence handling.
8. **Compass-ui parity audit (Sonnet, read-only):** list every compass-ui component and prop the prototype's sidebar, chat header, composer, empty state and Custom Prompts page use, and mark each as present or missing in the installed `alpha.14`. Output a table (component, props, prototype file, installed? yes/no). Missing items go to the user before Phase 1.
9. Can a plugin show the agent chat in the Channels center pane when an agent is clicked (override component, history redirect, or neither)? See Phase 5.

Output: a findings note and decisions for routing, refresh, limit. **Pause for user approval before Wave 1.**

### Phase 1 — shell and LHS (Sonnet A)
`agents_page.tsx` becomes a two-pane shell (keep `channel-view` effect; rework overflow pinning for a separate chat scroller). New `product_sidebar.tsx` (+ item component): All agents, Custom Prompts. Existing `AgentsList` stays the default destination. Jest tests for sidebar navigation and a11y labels.

### Phase 2 — Custom Prompts page (Sonnet B, B2)
1. Tests for `custom_prompts_management` first (list, tab filter, search, pin, create, edit, read-only non-owner, delete confirm, error banner) with a Redux store. Existing e2e must stay green.
2. Extract `custom_prompts_content.tsx`; modal behavior unchanged; compass-ui controls, with any handler-carrying styled wrapper declared in the same file as the handler.
3. `custom_prompts_page.tsx` with page chrome and sticky form footer; fetch on mount; license degradation identical to the modal.
No "use this prompt" action in this phase.

### Phase 3 — single-thread chat
- C: move `AIThread` to `webapp/src/types/`; `use_agent_threads.ts` groups by `bot_id`, filters `channel_id === bot.dmChannelID` (fail closed), sorts by `update_at` desc, guards superseded responses, implements the spike's refresh strategy.
- E (Opus): `agent_chat_page.tsx` — `ThreadViewer` for a selected root; new-chat state ported from `rhs_new_tab.tsx` (`getBotDirectChannel`, Redux update, `AdvancedTextEditor`, `afterSubmit` → navigate); `updateRead` on open and switch with the missing-membership tolerance and the spike's team fallback.
- F: `agent_threads_list.tsx` and the "Your agents" section: collapsible per agent, `[+]` new chat, placeholder label for empty titles.

Acceptance: thread selection shows exactly that thread; new chat appears nested under the agent; same posts visible in RHS and Channels DM (verify); thread not left unread; deep links work or fallback documented.

### Phase 4 — polish (Sonnet G)
Header, pill composer, empty state, avatars (simple first; port animated `AgentAvatar` only if design requires), sidebar collapse at narrow widths. CSS-only parts verified by stylelint + browser, not jest.

### Testing (all phases)
Jest per new component and hook; existing custom-prompts e2e kept green; one new product-view e2e (Sonnet H) assigned to the lightest `e2e-shard-*` group. Go: table-driven test for pagination if D runs.

### Deferred (unchanged)
Delete/archive chat (needs audited route), group chats, artifact/playbook RHS panels, Agent Settings alignment. (Channels LHS category is now Phase 5; only the center-pane replacement remains gated on the spike.)

### Open questions
1. Can a plugin inject an LHS category or replace the Channels center pane?
2. Channel-mention threads in the Agents LHS at all? (Plan defaults to DM only.)
3. What happens to a mention-started thread opened here?
4. Is delete/archive needed for v1?

### Commit sequence (orchestrator commits, one succinct subject each, no trailers)

```
webapp: move AIThread type to shared types
webapp: add Agents product sidebar and content layout
webapp: add tests for custom prompts management
webapp: extract custom prompts content from modal shell
webapp: add custom prompts page to Agents product
api: add limit and offset to ai_threads            (only if D runs)
webapp: add agent threads hook
webapp: add agent threads to Agents product sidebar
webapp: add single-thread agent chat view
webapp: polish agent chat header, composer, empty state
e2e: add Agents product view spec
```

### Verification

- Per subagent: its own jest suites + eslint on its files.
- Wave 3 I and Wave 4 L: `make check-style-fix`, `make check-i18n`, `make check-locks`, `make check-shards`, then `make check`.
- Manual: `make deploy`, open the Agents product from the switcher; walk list → prompts page → new chat → thread select → refresh; confirm no unread left, the thread shows in RHS and DM, and the modal still opens from the composer.
- Go (if D): `go test ./api/... ./store/...` scoped to the touched packages.
- Opus review (J) must report no unresolved blockers before the PR opens.

---

## 4. Phase 5 — Agents category in the Channels sidebar (added after review)

**Decisions (user):**
- Agents live in their **own sidebar category**, not under Direct Messages.
- Sync on **login / team join + new bot DM** (self-healing, idempotent).
- **Respect manual moves:** place a bot DM only the first time we see it for that user; never re-assert.
- Membership: bots the user **already has a DM with**, plus the **default agent always** (create its DM channel if missing so it has something to place).
- Clicking an agent opens the **new agent-style chat in the main center pane** (not the stock DM view).

**Server (Go, new small package called from `server/` hooks):**
- Plugin API available: `CreateChannelSidebarCategory`, `UpdateChannelSidebarCategories` (categories are per user **per team**; DMs appear in every team's sidebar, so sync each team).
- Hooks: `UserHasLoggedIn`, `UserHasJoinedTeam`, plus a call where a bot DM is created (`getBotDirectChannel` path / `handleDMs`).
- Persist by ID: KV record of the category ID per (user, team); KV record of "already placed" per (user, channel). Never find the category by display name.
- Idempotent and conditional on prior state (HA peers, double login): re-read the user's categories, add only unplaced channels, no blind writes.
- Fail closed: any lookup/API error → log and skip; never move channels on a miss, never fall back to the first category. Propagate errors, no swallowed empties.
- Resolve "default agent" and "bots this user may access" through the existing bot-access code (read it first; do not duplicate its authorization).
- No new route → no audit registry row. If an admin backfill route is ever added it needs one. Server log only; no content in records.
- Category display name localized (server `i18n/en.json` is hand-curated).
- Tests: table-driven; real-implementation style per repo rules; cases: first login, existing DMs, default agent with no DM, user already moved a bot (respected), category deleted by user (recreate once, then respect), API error (no mutation), concurrent sync.

**Center pane (gating unknown → Phase 0 item 9):** whether a plugin can show the single-thread chat when the user clicks an agent in the Channels sidebar.
- Option A (preferred if it works): a registered center-channel/channel-view component override for bot DM channels, staying inside Channels with the sidebar visible.
- Option B (likely fallback): `browserHistory.listen` redirect from `/:team/messages/@<agent>` to `/plug/mattermost-ai/agents/chat/:botId`. Works from a plugin, but it leaves the Channels view and its sidebar for the Agents product LHS.
- Option C: keep the stock DM view for now and file a host request.
Report which option is real before any UI work on this; if only B/C, confirm with the user.

### Phase 5b — host (core webapp) workstream, if the spike finds no plugin-only route

Host checkout: `~/Documents/GitHub/mattermost/mattermost` (`webapp/channels`). Plugin registry methods live in the host's plugin registry; the plugin already guards optional ones with `(registry as any).registerProduct`.

1. **Spike item 9 reads the host source (read-only first):** how the center channel view is chosen for a DM route, what the plugin registry already offers (center-pane / channel-view / route override hooks), and where an extension point would slot in. Output: yes/no on a plugin-only route, with file evidence.
2. **If no existing hook, design the host change:** a registry method, e.g. `registerChannelViewOverride(match(channel) => boolean, Component)`, where the host renders the plugin component in the center pane in place of the post list/composer when `match` is true, keeping the Channels sidebar, header and navigation. Include: props passed (`channel`, `team`, current user), precedence when several plugins match, fail-safe (a throwing `match` or component falls back to the stock view), a11y/focus handoff, mobile behavior, and a feature flag/capability so older hosts simply lack the method.
3. **We build it directly in the sibling host repo.** Once the spike confirms no existing hook, create a branch (e.g. `plugin-channel-view-override`) in `~/Documents/GitHub/mattermost/mattermost` and open a PR there, as its own change, separate from the plugin PR:
   - Work in its own session/worktree rooted in the host repo, so plugin and host edits never share a checkout. Check the host's tree is clean and which branch it is on before branching; never touch its `master`/default branch.
   - Read the host repo's own contributor/agent instructions (CLAUDE.md/AGENTS.md, webapp lint/test/i18n/type-check commands, PR template) before writing code; its conventions replace this plan's plugin-specific ones. Commit-trailer rules follow the host repo's instructions.
   - Scope: the registry method, the center-pane render hook with the fail-safe fallback, TypeScript types, unit tests, and plugin-registry docs. Nothing agent-specific lands in the host; the plugin owns the matching logic.
   - Sequencing: host PR and plugin PR are independent (the plugin feature-detects), so neither blocks the other; link them in both PR bodies. The plugin's center-pane behavior can only be tested end-to-end against a host build that includes the host branch, so plan that local verification (plugin deployed to a server running the host branch).
   - Pushing the host branch and opening its PR is within scope; merging either PR is not.
4. **Plugin side is decoupled:** register the override only when the method exists (`if (registry.registerChannelViewOverride)`), and match only agent bot DM channels (by bot ID from `useBotlist`, never by name). On hosts without it the stock DM view stays, so the sidebar category (Phase 5) still ships and works.
5. **Models:** host-source reading and design → Opus; host implementation and tests → Sonnet (Opus for the render-hook integration itself); host PR review → Opus, run against the host diff using the host repo's conventions.

Interim behavior until the host change ships: stock DM view (Option C), optionally the Option B redirect behind an off-by-default setting.

**Models:** Phase 0 item 9 → Opus (spike). Go sync package → Opus (race/fail-closed/authorization sensitive). Go tests and webapp wiring → Sonnet. The final Opus review (J) gets this package explicitly.

**Commit:** `api: sync Agents sidebar category for bot DMs` (+ `webapp: open agent DMs in agent chat view` if Option A/B).

---

## Appendix — provenance

Verified by reading `mattermost-plugin-agents` at commit `865f5b1`:
`/AGENTS.md`, `webapp/AGENTS.md`, `conversations/AGENTS.md`,
`webapp/src/index.tsx`, `webapp/src/mm_webapp.ts`,
`webapp/src/components/agents/agents_page.tsx`,
`webapp/src/components/rhs/rhs.tsx`,
`webapp/src/components/rhs/rhs_new_tab.tsx`,
`webapp/src/components/custom_prompts/custom_prompts_management.tsx`,
`webapp/src/components/custom_prompts/custom_prompts_dropdown.tsx`,
`api/api_custom_prompts.go`, `api/api_conversation.go`,
`conversation/service.go`, `conversations/handle_messages.go`,
plus directory listings for `webapp/src/components/agents/` and
`webapp/src/components/rhs/`.

**Not read, so treat as unverified:** the host `ThreadViewer` and
`AdvancedTextEditor` implementations, the `getAIThreads` server handler,
`LLMBotPost`, the agent settings tabs, and the full 1,900-line `AgentChat.tsx`
in the prototype.
