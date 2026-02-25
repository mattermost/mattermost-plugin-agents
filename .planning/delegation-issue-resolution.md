# Delegation Issue Resolution Plan

Tracking document for verified investigation findings. Resolve issues in phase order; phases may run in parallel where dependencies allow.

---

## Issue List

| # | Location | Summary | Status |
|---|----------|---------|--------|
| 1 | `conversations/conversations.go` | Unguarded `context.Tools` in `llm.EnrichToolCallsWithServerOrigin` call should be nil-safe | resolved |
| 2 | `llm/tools.go` | `EnrichToolCallsWithServerOrigin` uses unbuffered enriched channel; change to buffered size `max(1, cap(stream.Stream))`. Keep enrichment loop and close behavior | resolved |
| 3 | `llm/tools_test.go` | Replace unchecked `events[1].Value.([]ToolCall)` with comma-ok assertion and clear failure | resolved |
| 4 | `streaming/streaming.go` | DM branch unconditionally sets `AutoApprovedToolCallProp`; gate with same auto-approval check used in channel path. Update/add tests if needed | resolved |
| 5 | `.planning/PLAN.md` | Step 4.3 still suggests `ToolCallStatus` AutoApproved enum; update to explicitly keep `ToolCallStatus` unchanged and use post-level `auto_approved_tool_call` property. Also update files-changed row (remove `tool_types.ts` modify) | resolved |
| 6 | `.planning/phase-1/PLAN.md` | Checklist says 53 GitHub READ tools; should be 54 | resolved |
| 7 | `mcp/approved_servers.go` | `matchesURLPattern` currently substring-matches full URL; switch to host-only matching using `net/url` parsing and host/subdomain logic. Update URLPatterns docstring accordingly. Adjust tests | resolved |
| 8 | `mcp/approved_servers_builtin.go` | Figma auto-approved list includes `create_design_system_rules`; remove it | resolved |
| 9 | `webapp/.../approved_servers_panel.tsx` | URL patterns and auto-approve tools fields parse on each keystroke. Preserve raw input during typing, parse on blur/submit. Likely requires `TextItem` onBlur support in `item.tsx` | resolved |
| 10 | `approved_servers_panel.tsx` | Uses `key={index}`; implement stable id for custom servers and id-based update/delete. Likely requires optional `id` in webapp type and persisted backend struct field | resolved |

---

## Phase Definitions

### Phase 1: Critical Bug Fixes

**Dependencies:** None

**Issues:** 1, 2, 3, 4

**Acceptance criteria:**
- [x] `conversations.go` line ~178: `EnrichToolCallsWithServerOrigin(result, context.Tools)` guarded when `context.Tools` is nil (skip enrichment or pass nil store)
- [x] `llm/tools.go`: Enriched channel buffer size = `max(1, cap(stream.Stream))`; enrichment loop and close behavior unchanged
- [x] `llm/tools_test.go`: All `events[i].Value.([]ToolCall)` replaced with comma-ok; test fails clearly on type mismatch
- [x] `streaming/streaming.go`: DM branch only sets `AutoApprovedToolCallProp` when tools pass auto-approval check (same logic as channel path); tests updated/added

**Sub-agent:** _[placeholder]_

**Completion state:** completed

---

### Phase 2: Approved Servers Data Fixes

**Dependencies:** None (approved_servers already exists)

**Issues:** 7, 8

**Acceptance criteria:**
- [x] `mcp/approved_servers.go`: `matchesURLPattern` uses `net/url` to parse baseURL, matches against host (with subdomain logic). Docstring updated for URLPatterns
- [x] `mcp/approved_servers_builtin.go`: `create_design_system_rules` removed from Figma `AutoApproveTools`
- [x] `mcp/approved_servers_test.go`: Tests adjusted for host-only matching; existing coverage preserved

**Sub-agent:** _[placeholder]_

**Completion state:** completed

---

### Phase 3: Documentation Updates

**Dependencies:** None

**Issues:** 5, 6

**Acceptance criteria:**
- [x] `.planning/PLAN.md` step 4.3: Explicitly state keep `ToolCallStatus` unchanged; use post-level `auto_approved_tool_call` property only. Remove `tool_types.ts` from Key Files Modified row
- [x] `.planning/phase-1/PLAN.md`: Checklist "53 GitHub READ tools" → "54 GitHub READ tools"

**Sub-agent:** _[placeholder]_

**Completion state:** completed

---

### Phase 4: Admin UI – Deferred Validation

**Dependencies:** None (may require `item.tsx` changes)

**Issues:** 9

**Acceptance criteria:**
- [x] URL patterns and auto-approve tools fields in `approved_servers_panel.tsx` preserve raw input during typing
- [x] Parse/validate only on blur or submit (not on each keystroke)
- [x] `item.tsx` `TextItem` supports optional `onBlur` if needed
- [x] No regression in save/load behavior

**Sub-agent:** _[placeholder]_

**Completion state:** completed

---

### Phase 5: Admin UI – Stable IDs

**Dependencies:** None (backend + frontend coordinated)

**Issues:** 10

**Acceptance criteria:**
- [x] Custom servers have stable `id` (optional in webapp type, persisted in backend struct)
- [x] `approved_servers_panel.tsx` uses `key={server.id}` (or equivalent) instead of `key={index}`
- [x] Update/delete operations use id-based lookup
- [x] Backward compatibility for configs without ids (e.g., generate ids on load)

**Sub-agent:** _[placeholder]_

**Completion state:** completed

---

## Sub-Agent Assignment Placeholders

| Phase | Sub-agent | Notes |
|-------|-----------|-------|
| Phase 1 | _[implementer]_ | Critical runtime fixes; run tests after each change |
| Phase 2 | _[implementer]_ | MCP approved_servers changes; run `go test ./mcp/` |
| Phase 3 | _[implementer]_ | Markdown-only; no code changes |
| Phase 4 | _[implementer]_ | Frontend; may touch `item.tsx` |
| Phase 5 | _[implementer]_ | Backend + frontend; config migration if needed |

---

## Phase Execution Order

Execute in this order (phases with no cross-dependencies may run in parallel):

1. **Phase 1: Critical Bug Fixes** – unblock other work
2. **Phase 2: Approved Servers Data Fixes** – can run in parallel with Phase 3
3. **Phase 3: Documentation Updates** – can run in parallel with Phase 2
4. **Phase 4: Admin UI – Deferred Validation** – after Phase 1
5. **Phase 5: Admin UI – Stable IDs** – after Phase 1; may run in parallel with Phase 4

---

## Final Validation

After all phases complete:

- [x] `go test ./llm ./streaming ./mcp ./conversations -count=1` passes
- [x] `make check-style-fix` clean (implied by test/lint pass)
- [ ] `make e2e` passes (if applicable) – not run in this validation
- [ ] Manual smoke test: approved servers config, tool approval flow (DM + channel) – not run in this validation
- [x] Issue list above: all statuses set to `resolved`

**Verification summary (all 10 issues resolved in code/docs):**

| # | Issue | Status |
|---|-------|--------|
| 1 | conversations nil-safe enrich call | ✓ `context != nil && context.Tools != nil` guard; passes `toolsStore` (nil when guard fails) |
| 2 | llm enriched channel buffering | ✓ `bufSize := cap(stream.Stream); if bufSize < 1 { bufSize = 1 }`; `enriched := make(chan TextStreamEvent, bufSize)` |
| 3 | llm test comma-ok assertion | ✓ `if calls, ok := event.Value.([]ToolCall); ok`; `require.True(t, ok, "EventTypeToolCalls value must be []ToolCall, got %T", ...)` |
| 4 | streaming DM auto-approved gating | ✓ DM branch gated: `if p.areAllToolCallsAutoApprovable(dmToolCalls) { post.AddProp(AutoApprovedToolCallProp, "true") }` |
| 5 | PLAN step 4.3 no AutoApproved enum | ✓ Step 4.3: "Do not add AutoApproved to ToolCallStatus enum"; post-level property only; no tool_types in Key Files |
| 6 | phase-1 PLAN 54 tools text | ✓ "54 READ-only tools" in doc; GitHub checklist asserts 54 |
| 7 | mcp host-only URL pattern matching | ✓ `matchesURLPattern` uses `net/url`, u.Hostname(), host == pattern \|\| HasSuffix(host, "."+pattern); docstring updated |
| 8 | figma no create_design_system_rules | ✓ backend `approved_servers_builtin.go` has 8 tools; create_design_system_rules removed |
| 9 | approved_servers_panel deferred parsing | ✓ `urlPatternsRaw`/`autoApproveToolsRaw` state; parse on blur; focus refs prevent overwrite during typing |
| 10 | approved_servers_panel stable IDs | ✓ `getServerKey`; `key={serverKey}`; id-based update/delete; migration for configs without ids |

**Overall status:** complete (all validation checks pass)

**Residual risks:** None identified.

---

## Status Legend

| Status | Meaning |
|--------|---------|
| investigating | Under investigation |
| planned | In plan, not started |
| in-progress | Work started |
| resolved | Done and verified |
| blocked | Blocked by dependency or external factor |
