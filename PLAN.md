# Implementation Plan: Per-Tool Admin Filtering & User Tool API

> **Source Spec:** `/Users/nickmisasi/workspace/planning/projects/mattermost-plugin-agents/ideas/003-per-tool-admin-filtering/spec.md`
> **Worktree:** `~/workspace/worktrees/mattermost-plugin-ai-mattermost-vetted-tools`
>
> **This is a REWRITE of PR #520.** The existing `ApprovedMCPServer` struct, approved servers panel, and related code are removed and replaced with a unified per-tool config model on `ServerConfig`. Preserve: `ServerOrigin` on Tool/ToolCall, `ToolCallStatusAutoApproved`, streaming auto-approval infrastructure, vetted tool classifications (as seed data).

---

## Global Requirements

### Frontend Implementation: Figma-Driven Development (MANDATORY)

**All frontend tasks MUST use the `/figma-implement-design` skill** when implementing UI components. This is non-negotiable. The skill translates Figma nodes into production-ready code with 1:1 visual fidelity.

For every frontend task:
1. **Implementation Engineers** must invoke `/figma-implement-design` with the Figma URL and node ID specified in the task
2. Use the Figma MCP tools (`get_design_context`, `get_screenshot`) to extract exact design tokens, spacing, typography, and component structure
3. Adapt the Figma output to the project's existing conventions: **styled-components**, **CSS variables** (`var(--center-channel-bg)`, `var(--button-bg)`, etc.), **React 17**, **TypeScript**
4. Reuse existing plugin components where specified (see Component Reuse section)

### Visual QA: Browser Screenshot Comparison (MANDATORY)

**QA Team Members** must perform visual comparison for every frontend task:
1. Take browser screenshots of the implemented UI using Chrome DevTools MCP tools (`take_screenshot`)
2. Fetch the corresponding Figma screenshot using the Figma MCP tool (`get_screenshot`) with the node ID specified in the task
3. Compare side-by-side: layout, spacing, typography, colors, component sizing, alignment
4. File bugs for any visual deviation

### Component Reuse Reference

Existing plugin components to leverage (do NOT rebuild):
- **Tabs:** `TabsContainer`, `TabButton`, `TabContent` from `system_console/mcp_servers.tsx`
- **Form fields:** `TextItem`, `SelectionItem`, `ComboboxItem`, `BooleanItem` from `system_console/item.tsx`
- **Buttons:** `PrimaryButton`, `TertiaryButton`, `DestructiveButton`, `Button`, `ButtonIcon` from `assets/buttons.tsx`
- **Other:** `Pill`/`GrayPill`, `Panel`, `ToolCard`, `Checkbox`, `Dropdown`, `DotMenu`, `LoadingSpinner`
- **MCP Tools Viewer (existing):** `mcp_tools_viewer.tsx` — current read-only tool list. Will be heavily refactored but use as starting point.
- **Bot Selector Dropdown:** `bot_selector.tsx` — pattern for RHS header dropdowns/popovers

### Figma Design Registry

| ID | Name | Figma URL | Node ID | Used In |
|----|------|-----------|---------|---------|
| Design A | System Console — Per-Tool Admin Options | https://www.figma.com/design/7IQdy7Kujc13gWNLbRAUBC/AI-Copilot---Agents-UX?node-id=2258-95245&m=dev | `2258:95245` | Phase 2 |
| Design B | User Tool Provider Toggles (Copilot RHS) | https://www.figma.com/design/7IQdy7Kujc13gWNLbRAUBC/AI-Copilot---Agents-UX?node-id=3029-131086&m=dev | `3029:131086` | Phase 3 |

Figma file key: `7IQdy7Kujc13gWNLbRAUBC`

### Technical Context

- **This rewrites PR #520** — remove `ApprovedMCPServer`, `approved_servers.go`, `approved_servers_builtin.go`, `approved_servers_panel.tsx`, `approved_servers_test.go`. Keep `ServerOrigin`, `ToolCallStatusAutoApproved`, streaming auto-approval infrastructure.
- **Styling:** styled-components 6.x with CSS variable theming
- **State:** Redux 4.x with react-redux 8.x
- **Build:** Webpack 5, TypeScript 4.9, React 17
- **Icons:** `@mattermost/compass-icons`
- **Tests (backend):** Go standard testing
- **Tests (E2E):** Playwright + testcontainers + Smocker (LLM mocks)

---

## Phase 1: Backend — Data Model & API

**Goal:** New per-tool config model on `ServerConfig`, tool filtering in discovery, user-facing tool list API, and refactored auto-approval. Remove all `ApprovedMCPServer` code.

### Tasks

#### 1.1 — Remove `ApprovedMCPServer` Code
Delete the following files and all references:
- `/mcp/approved_servers.go` — `ApprovedMCPServer` struct, `ApprovedMCPServersConfig`, `IsToolAutoApproved()`, `GetAutoApprovedToolNames()`, `MergeApprovedServers()`, `matchesURLPattern()`
- `/mcp/approved_servers_builtin.go` — `BuiltinApprovedServers()`, hardcoded server definitions
- `/mcp/approved_servers_test.go` — all tests
- Remove `ApprovedServers []ApprovedMCPServer` field from MCP config
- Remove `ApprovedMCPServers()` method from config container
- Remove wiring in `/server/main.go` that sets `ToolAutoApprover` using `ApprovedMCPServers()`
- Remove `conversations/mcp_auto_approval.go` (will be rebuilt in 1.4)
- Update imports everywhere

#### 1.2 — Add `MCPToolConfig` and `ToolConfigs` to `ServerConfig`
In `/mcp/mcp.go`:
```go
type MCPToolConfig struct {
    Name    string `json:"name"`
    Policy  string `json:"policy"`  // "auto_run" | "ask"
    Enabled bool   `json:"enabled"`
}

type ServerConfig struct {
    Name        string            `json:"name"`
    Enabled     bool              `json:"enabled"`
    BaseURL     string            `json:"baseURL"`
    Headers     map[string]string `json:"headers,omitempty"`
    ToolConfigs []MCPToolConfig   `json:"tool_configs,omitempty"`
}
```

Add helper methods:
- `GetToolPolicy(toolName string) (policy string, enabled bool)` on `ServerConfig` — returns policy + enabled for a tool, defaults to `"ask"`, `false` if not found
- `IsToolAutoRun(toolName string) bool` — convenience: returns true if policy == "auto_run" && enabled

#### 1.3 — Vetted Tool Seed Function
Create `/mcp/vetted_tools.go`:
- `SeedVettedToolConfigs(baseURL string) []MCPToolConfig` — if `baseURL` matches a known vetted host (Atlassian, GitHub, Figma, Mattermost embedded), return pre-populated `ToolConfigs` with READ-only tools as `policy: "auto_run", enabled: true`. All other known tools as `policy: "ask", enabled: true`.
- Move the vetted tool classification lists here (from the deleted `approved_servers_builtin.go`)
- `IsVettedHost(baseURL string) bool` — hostname matching helper
- This is called **once at server creation time** in the admin UI (or API), not at runtime

#### 1.4 — Refactor Auto-Approval in Streaming
Refactor `/streaming/streaming.go` and related code:
- Replace `ToolAutoApprovalChecker` interface:
  ```go
  type ToolPolicyChecker interface {
      GetToolPolicy(serverBaseURL string, toolName string) (policy string, enabled bool)
  }
  ```
- `areAllToolCallsAutoApprovable()` → check `GetToolPolicy()` returns `"auto_run"` for each tool
- Wire in `/server/main.go`: implement `ToolPolicyChecker` by looking up `ServerConfig` by matching `BaseURL`, then calling `serverConfig.GetToolPolicy(toolName)`
- Rebuild `conversations/mcp_auto_approval.go` to use the new interface

#### 1.5 — Tool Filtering in Discovery
In `/mcp/client_manager.go` or context building (`/llmcontext/`):
- After `GetToolsForUser()` returns tools, filter by `ServerConfig.ToolConfigs`:
  - If tool has config entry with `enabled: false` → exclude
  - If tool has NO config entry → exclude (new unconfigured tools are disabled by default)
- This ensures only admin-enabled tools reach the LLM context

#### 1.6 — User-Facing Tool List API
Add `GET /api/mcp/tools` endpoint in `/api/api_mcp.go` (new file):
- Requires authenticated user (`MattermostAuthorizationRequired`)
- Calls `GetToolsForUser(requesterUserID)` to discover tools
- Groups tools by `ServerOrigin`
- For each server, looks up OAuth status from KV store (`mcp_oauth_token_v1_{userID}_{serverID}`)
- Applies admin filtering (exclude disabled tools)
- Returns response shape:
  ```json
  {
    "servers": [{
      "name": "Jira",
      "serverOrigin": "https://mcp.atlassian.com",
      "authenticated": true,
      "authEmail": "user@example.com",
      "tools": [
        {"name": "get_jira", "description": "...", "enabled": true, "policy": "auto_run"}
      ]
    }]
  }
  ```

#### 1.7 — User Provider Toggle State
- Store per-user provider preferences in KV: key `user_tool_providers_{userID}`, value JSON `{"disabled_servers": ["https://mcp.atlassian.com"]}`
- Add `GET /api/mcp/user-preferences` and `PUT /api/mcp/user-preferences` endpoints
- Apply in tool filtering: if user has disabled a server, exclude all its tools from LLM context (Copilot RHS only — in-channel uses agent's `EnabledTools`)

#### 1.8 — Backend Tests
- Unit tests for `MCPToolConfig`, `GetToolPolicy()`, `IsToolAutoRun()`
- Unit tests for `SeedVettedToolConfigs()` — all 4 vetted hosts, non-vetted host returns nil
- Unit tests for tool filtering (enabled/disabled/unconfigured tools)
- Unit tests for refactored auto-approval (auto_run policy triggers auto-approval, ask policy doesn't)
- Integration tests for `GET /api/mcp/tools` endpoint (auth status, grouping, filtering)
- Integration tests for user provider preferences (save, load, apply in filtering)

**Checkpoint:** Full backend working. Auto-approval uses `ServerConfig.ToolConfigs`. Tool filtering applied. User-facing API returns correct data. All `ApprovedMCPServer` code removed. All tests pass.

---

## Phase 2: Admin UI — Merged Tools Tab

**Goal:** Replace the 3-tab layout (Configuration | Tools | Approved Servers) with 2 tabs (Configuration | Tools). The merged Tools tab shows expandable MCP server rows with per-tool policy dropdowns and enable/disable toggles.

### Tasks

#### 2.1 — Remove Approved Servers Tab & Panel
- Delete `/webapp/src/components/system_console/approved_servers_panel.tsx`
- In `/webapp/src/components/system_console/mcp_servers.tsx`: remove the "Approved Servers" tab from the tab bar and its content rendering
- Remove `BUILTIN_APPROVED_SERVERS` constant from frontend (no longer needed — seed happens on backend)

#### 2.2 — MCP Server Row Component
**MUST use `/figma-implement-design` with Design A (node `2258:95245`).**

Create `/webapp/src/components/system_console/mcp_tool_row.tsx`:
- **MCP server row (collapsed):** avatar/icon, server name, "X/Y tools enabled" subtitle, gear icon button, main toggle switch (SwitchSelector), expand/collapse chevron
- **MCP server row (expanded):** reveals per-tool rows below on light gray background
- Follow the `MCP Row` component pattern from Design A

#### 2.3 — Per-Tool Row Component
**MUST use `/figma-implement-design` with Design A (node `2258:95245`).**

Create `/webapp/src/components/system_console/mcp_tool_config_row.tsx`:
- Tool name in monospace (`Menlo`, 13px) + description below
- **Execution policy dropdown:** "Auto Run" / "Ask Everytime" — use `Dropdown` component
- **Enabled toggle:** Small `SwitchSelector`
- Expand/collapse chevron (for future tool schema display)
- Follow the `MCP Parameter Row` component pattern from Design A

#### 2.4 — Merged Tools Tab
**MUST use `/figma-implement-design` with Design A (node `2258:95245`).**

Refactor `/webapp/src/components/system_console/mcp_tools_viewer.tsx`:
- Replace read-only tool list with interactive MCP server rows (2.2) containing per-tool config rows (2.3)
- Fetch tool discovery from `GET /admin/mcp/tools` (existing endpoint)
- Join discovered tools with `ServerConfig.ToolConfigs` from the config store
- For each discovered tool: show its config (policy + enabled) or defaults ("ask" / disabled) if unconfigured
- "Refresh" and "Clear Cache" buttons preserved
- "+" Add an MCP Connector" button at bottom

#### 2.5 — Vetted Server Seed on Server Add
When an admin adds a new MCP server in the Configuration tab:
- After saving, check if the server's BaseURL matches a vetted host
- If so, call the backend to seed `ToolConfigs` (or seed client-side using a shared vetted tool list)
- Pre-populated tools appear immediately in the Tools tab
- Add frontend client method: `seedVettedTools(serverBaseURL)` or include in config save response

#### 2.6 — Config Persistence
- On save: serialize `ServerConfig.ToolConfigs` as part of the config blob
- Use existing `savePluginConfig()` client method
- Verify round-trip: save → reload page → config persists correctly

**Checkpoint:** System Console shows 2 tabs. Tools tab has interactive per-tool configuration. Vetted servers pre-fill on creation. Config persists correctly. Visual QA against Design A.

---

## Phase 3: User-Facing Tool Provider Toggles (Copilot RHS)

**Goal:** Users can toggle MCP providers on/off in the Copilot sidebar via a popover triggered from the RHS header.

### Tasks

#### 3.1 — Tool Provider Button in RHS Header
In `/webapp/src/components/rhs/rhs_header.tsx`:
- Add a button (icon: wrench/tool or similar from compass-icons) next to the existing bot selector dropdown
- Button shows active provider count badge (e.g., "3" for 3 enabled providers)
- Clicking opens the tool provider popover (3.2)

#### 3.2 — Tool Provider Popover
**MUST use `/figma-implement-design` with Design B (node `3029:131086`).**

Create `/webapp/src/components/rhs/tool_provider_popover.tsx`:
- Floating popover (262x~196px, white, 4px border-radius, elevation shadow)
- List of MCP server providers, each row: 24px avatar, provider name (14px), small toggle switch
- Toggles call `PUT /api/mcp/user-preferences` to persist
- Fetch initial state from `GET /api/mcp/user-preferences`
- Only shows servers that have at least one enabled tool (per admin config)

#### 3.3 — Redux Integration
- Add state for user tool provider preferences
- Actions: `FETCH_USER_TOOL_PREFERENCES`, `UPDATE_USER_TOOL_PREFERENCES`
- API client methods: `getUserToolPreferences()`, `updateUserToolPreferences(prefs)`
- Selector: `getDisabledServers(state)` — returns list of servers the user has toggled off

#### 3.4 — Apply User Toggles in Copilot Requests
When the Copilot sends a message:
- Include user's disabled servers in the request context
- Backend filters tools accordingly (from 1.7)
- Note: this only applies to Copilot RHS. In-channel @mentions use agent's `EnabledTools` — no user toggle applied.

**Checkpoint:** Users can toggle providers on/off in Copilot sidebar. Preferences persist across sessions. Disabled providers' tools excluded from Copilot LLM context. Visual QA against Design B.

---

## Phase 4: E2E Testing

**Goal:** Comprehensive Playwright tests for system console tool config flow AND real API tests verifying auto-run/ask-every-time policies actually work at tool call time.

### Test Infrastructure

#### 4.1 — Test Helpers
Create `/e2e/helpers/tool-config.ts`:
- `ToolConfigHelper` page object: locators for Tools tab, MCP server rows, per-tool rows, policy dropdowns, toggles
- `navigateToToolsTab()`, `expandServer(name)`, `setToolPolicy(toolName, policy)`, `toggleTool(toolName, enabled)`, `getToolPolicy(toolName)`
- API helper: programmatic tool config read/write via plugin config API

#### 4.2 — Test Container Setup
Create `/e2e/helpers/tool-config-container.ts`:
- `RunToolConfigTestContainer()` factory
- Provisions MM server with: plugin installed, 2+ mock MCP servers with known tools, 1 mock server matching vetted host pattern (for seed testing)
- Mock MCP servers expose predictable tool lists

### System Console E2E Tests (Mocked LLM)

#### 4.3 — Tools Tab Displays Servers & Tools
`/e2e/tests/tool-config/tools-tab-display.spec.ts`:
- Navigate to System Console → Agents → Tools tab
- Verify MCP servers listed with correct tool counts
- Expand a server → verify individual tools shown with name + description
- Verify policy dropdown shows "Auto Run" or "Ask Everytime"
- Verify toggle reflects enabled/disabled state

#### 4.4 — Per-Tool Policy Change
`/e2e/tests/tool-config/policy-change.spec.ts`:
- Expand server → change tool policy from "Ask Everytime" to "Auto Run" → save
- Reload page → verify policy persisted
- Change back → save → verify

#### 4.5 — Per-Tool Enable/Disable
`/e2e/tests/tool-config/tool-toggle.spec.ts`:
- Expand server → disable a tool → save
- Reload → verify tool shows as disabled
- Verify disabled tool does NOT appear in `GET /api/mcp/tools` response (check via API helper)

#### 4.6 — Vetted Server Seed
`/e2e/tests/tool-config/vetted-seed.spec.ts`:
- Add new MCP server with BaseURL matching a vetted host (e.g., mock server aliased to `mcp.atlassian.com`)
- Navigate to Tools tab → expand server → verify tools pre-filled with "Auto Run" policy for READ-only tools
- Verify non-vetted server shows tools as "Ask Everytime" / disabled by default

#### 4.7 — Tab Merge Verification
`/e2e/tests/tool-config/tab-layout.spec.ts`:
- Navigate to System Console → Agents
- Verify only 2 tabs: "Configuration" and "Tools"
- Verify NO "Approved Servers" tab exists

### Real API Tool Calling Tests (API Key Gated)

#### 4.8 — Auto Run Policy Works
`/e2e/tests/tool-config/real-api/auto-run-policy.spec.ts`:
- Skip if no API keys configured
- Configure a tool as `policy: "auto_run", enabled: true`
- Trigger a bot interaction that invokes that tool in a DM
- Verify tool executes WITHOUT user approval prompt (auto-run behavior)
- Verify `ToolCallStatusAutoApproved` in post props

#### 4.9 — Ask Every Time Policy Works
`/e2e/tests/tool-config/real-api/ask-policy.spec.ts`:
- Skip if no API keys configured
- Configure a tool as `policy: "ask", enabled: true`
- Trigger a bot interaction that invokes that tool in a DM
- Verify tool shows as PENDING (requires user approval)
- Approve the tool call → verify execution completes

#### 4.10 — Disabled Tool Excluded
`/e2e/tests/tool-config/real-api/disabled-tool.spec.ts`:
- Skip if no API keys configured
- Configure a tool as `enabled: false`
- Trigger a bot interaction → verify the disabled tool is NOT called by the LLM (not in tool list provided to model)
- Verify via mock request inspection or post content

#### 4.11 — Auto Run in Channel (Two-Stage)
`/e2e/tests/tool-config/real-api/channel-auto-run.spec.ts`:
- Skip if no API keys configured
- Configure a tool as `policy: "auto_run", enabled: true`
- @mention bot in a channel (not DM)
- Verify tool call skips call approval (auto-approved)
- Verify tool results still require result-sharing approval (channel safety)
- Approve results → verify LLM continuation

#### 4.12 — User Provider Toggle (Copilot RHS)
`/e2e/tests/tool-config/user-toggle.spec.ts`:
- Open Copilot RHS → open tool provider popover
- Verify providers listed with toggles
- Disable a provider → send message → verify that provider's tools are NOT used
- Re-enable provider → send message → verify tools available again

### Mocked LLM Tool Interaction Tests

#### 4.13 — Tool Call with Policy Verification (Mocked)
`/e2e/tests/tool-config/mock-api/tool-call-policies.spec.ts`:
- Configure mock LLM to return tool calls
- Tool A: `auto_run` policy → verify auto-executes
- Tool B: `ask` policy → verify shows pending approval
- Tool C: `enabled: false` → verify not in tool list at all
- Verify correct WebSocket events and post prop updates for each case

**Checkpoint:** Full E2E test suite covers system console UI, config persistence, policy enforcement (auto-run vs ask), disabled tool exclusion, channel two-stage flow, and user provider toggles. CI green.

---

## Phase 5: In-Browser QA

**Goal:** Manual QA against a live Mattermost instance. Validate against Figma designs. File bugs back into earlier phases.

### Environment

- **Mattermost server:** `http://localhost:8065`
- **Credentials:** `nickmisasi` / `Password123@`
- **Deploy plugin:** `MM_SERVICESETTINGS_SITEURL=http://localhost:8065 make deploy`
- **QA agents** should use Chrome DevTools MCP tools for screenshots and interaction

### Setup

#### 5.1 — Deploy & Verify
- Run `MM_SERVICESETTINGS_SITEURL=http://localhost:8065 make deploy` from the worktree root
- Navigate to `http://localhost:8065` → login as `nickmisasi` / `Password123@`
- Navigate to System Console → Agents plugin → verify plugin is active
- Verify MCP section shows "Configuration" and "Tools" tabs (NO "Approved Servers" tab)

### System Console QA

#### 5.2 — Tools Tab Layout
- Navigate to Tools tab
- Verify MCP servers listed with correct tool counts
- Verify expandable rows (click chevron → tools appear on gray background)
- **Visual comparison:** Take screenshot via Chrome DevTools, compare against Figma Design A ([node 2258:95245](https://www.figma.com/design/7IQdy7Kujc13gWNLbRAUBC/AI-Copilot---Agents-UX?node-id=2258-95245&m=dev)) using `get_screenshot`

#### 5.3 — Per-Tool Policy Configuration
- Expand a server → verify each tool shows policy dropdown and toggle
- Change a tool from "Ask Everytime" to "Auto Run" → save
- Reload page → verify policy persisted
- Change back → save → verify round-trip

#### 5.4 — Per-Tool Enable/Disable
- Disable a tool via toggle → save → reload → verify disabled
- Re-enable → save → verify enabled
- Verify disabled tools do NOT appear in user-facing tool list (check via Copilot RHS or API)

#### 5.5 — Vetted Server Seed
- If a vetted server (Atlassian, GitHub, Figma, or Mattermost embedded) is configured:
  - Navigate to Tools tab → expand server
  - Verify READ-only tools pre-filled as "Auto Run"
  - Verify WRITE tools pre-filled as "Ask Everytime"

#### 5.6 — New Unconfigured Tools
- If a server exposes a tool not yet in `ToolConfigs` (e.g., server added a new tool since last config):
  - Verify it appears as disabled / "Ask Everytime" by default
  - Verify admin must explicitly enable it

### Copilot RHS QA

#### 5.7 — Tool Provider Popover
- Open Copilot RHS (channel header button)
- Locate tool provider button in RHS header (next to bot selector)
- Click → verify popover appears with provider list
- **Visual comparison:** Screenshot vs Figma Design B ([node 3029:131086](https://www.figma.com/design/7IQdy7Kujc13gWNLbRAUBC/AI-Copilot---Agents-UX?node-id=3029-131086&m=dev))

#### 5.8 — User Provider Toggle Functionality
- Toggle a provider OFF in the popover
- Send a message that would invoke that provider's tools
- Verify the disabled provider's tools are NOT used
- Toggle provider back ON → send another message → verify tools available again

#### 5.9 — Provider Toggle Persistence
- Toggle a provider OFF → close the popover → close RHS → reopen RHS → reopen popover
- Verify the toggle state persisted

### Tool Calling QA (Live)

#### 5.10 — Auto Run Policy (DM)
- Ensure a tool is configured as "Auto Run" + enabled
- Open Copilot RHS → send a prompt that triggers that tool
- Verify tool executes automatically without approval prompt

#### 5.11 — Ask Every Time Policy (DM)
- Ensure a tool is configured as "Ask Everytime" + enabled
- Open Copilot RHS → send a prompt that triggers that tool
- Verify tool shows as pending, requiring user approval
- Approve → verify tool executes and LLM continues

#### 5.12 — Auto Run in Channel (Two-Stage)
- Ensure a tool is configured as "Auto Run" + enabled
- @mention the bot in a channel
- Verify tool call auto-approved (skips call approval)
- Verify tool RESULTS still require sharing approval (channel safety)
- Approve results → verify response posted

#### 5.13 — Disabled Tool Not Called
- Disable a tool in System Console → save
- Trigger a prompt that would normally use that tool
- Verify the tool is NOT invoked (not in LLM context)

### Cross-Browser & Visual QA

#### 5.14 — Visual Comparison (All Screens)
- Screenshot every key screen and compare against Figma:
  - Tools tab (collapsed servers) vs Design A
  - Tools tab (expanded server with per-tool rows) vs Design A
  - Tool provider popover vs Design B
- Flag any spacing, color, typography, or layout deviations

#### 5.15 — Cross-Browser
- Spot check in Firefox (in addition to Chrome) for layout/styling consistency

**Checkpoint:** All QA items pass or bugs filed and remediated. Sign-off for merge.

---

## Phase 6: Polish & Edge Cases

**Goal:** Refinements after QA pass. Fix bugs found in Phase 5.

### Tasks

- **6.1** Tool discovery error handling: server unreachable, OAuth expired, connection timeout — show clear status in Tools tab
- **6.2** Loading states: spinner while tools are being discovered, skeleton rows during load
- **6.3** Empty states: no MCP servers configured, server has no tools, all tools disabled
- **6.4** Search/filter in Tools tab (if many tools across servers)
- **6.5** Tooltip on policy dropdown explaining "Auto Run" vs "Ask Everytime" behavior
- **6.6** Edge case: server removed from Configuration tab while Tools tab is open
- **6.7** Edge case: tool discovered on first load but not on subsequent (server changed tools) — handle gracefully, don't lose config for missing tools
- **6.8** Fix any bugs filed during Phase 5 QA
