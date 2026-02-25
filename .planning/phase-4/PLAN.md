# Phase 4: Frontend Changes - Implementation Plan

## Overview

Phase 4 updates the frontend to recognize auto-approved tool calls and provide appropriate UI feedback. When tools from Mattermost-approved MCP servers are auto-executed (Phase 3), the frontend should:

1. **Skip the call-stage approval UI** (no Accept/Reject buttons for execution)
2. **Show an informational indicator** that tools were auto-approved as READ-only
3. **Still show the result-stage approval UI** (Share/Keep private) as normal

The frontend detects auto-approval via the `auto_approved_tool_call` post property set by the backend (Phase 3).

---

## Data Flow from Backend (Phase 3 Recap)

When all tool calls in a batch are auto-approvable, the backend:

1. Sets `auto_approved_tool_call = "true"` on the post props
2. Auto-executes the tools via `HandleToolCall` with all IDs pre-accepted
3. `HandleToolCall` sets `pending_tool_result = "true"` once tools complete

So the frontend will see posts transition through these states:

**State A** (brief, during auto-execution):
```json
{
    "pending_tool_call": "[{redacted tool calls}]",
    "pending_tool_call_redacted": "true",
    "auto_approved_tool_call": "true"
}
```

**State B** (after auto-execution completes):
```json
{
    "pending_tool_call": "[{redacted tool calls with results}]",
    "pending_tool_call_redacted": "true",
    "auto_approved_tool_call": "true",
    "pending_tool_result": "true"
}
```

In State A, `toolApprovalStage` is `'call'` but no user action is needed.
In State B, `toolApprovalStage` is `'result'` and the user must approve sharing.

---

## Step 4.1: Detect Auto-Approved State in `llmbot_post.tsx`

### File: `webapp/src/components/llmbot_post/llmbot_post.tsx`

The `LLMBotPost` component determines `toolApprovalStage` and passes it along with `canApprove` to `ToolApprovalSet`. We need to add detection of the auto-approved state and pass it down.

#### Changes

**Add `isAutoApproved` derived state** (after line 112, near the other post prop reads):

```typescript
const isAutoApproved = String(props.post.props?.auto_approved_tool_call).toLowerCase() === 'true';
```

**Pass `isAutoApproved` to `ToolApprovalSet`** (around line 513-521):

Change from:
```tsx
<ToolApprovalSet
    postID={props.post.id}
    toolCalls={resolvedToolCalls}
    approvalStage={toolApprovalStage}
    canApprove={requesterIsCurrentUser}
    canExpand={requesterIsCurrentUser}
    showArguments={showToolArguments}
    showResults={showToolResults}
/>
```

To:
```tsx
<ToolApprovalSet
    postID={props.post.id}
    toolCalls={resolvedToolCalls}
    approvalStage={toolApprovalStage}
    canApprove={requesterIsCurrentUser}
    canExpand={requesterIsCurrentUser}
    showArguments={showToolArguments}
    showResults={showToolResults}
    isAutoApproved={isAutoApproved}
/>
```

---

## Step 4.2: Update `ToolApprovalSet` to Skip Call-Stage UI When Auto-Approved

### File: `webapp/src/components/tool_approval_set.tsx`

When `isAutoApproved` is true and `approvalStage` is `'call'`, the component should NOT show Accept/Reject buttons. Instead, it shows an informational banner indicating tools were auto-executed.

#### Interface Changes

Add `isAutoApproved` to the props interface (line 34-42):

```typescript
interface ToolApprovalSetProps {
    postID: string;
    toolCalls: ToolCall[];
    approvalStage: ToolApprovalStage;
    canApprove: boolean;
    canExpand: boolean;
    showArguments: boolean;
    showResults: boolean;
    isAutoApproved?: boolean;  // NEW
}
```

#### New Styled Component

Add after the existing `StatusBar` styled component (around line 31):

```typescript
const AutoApprovedBanner = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 12px;
    margin-top: 4px;
    background: rgba(var(--online-indicator-rgb), 0.08);
    border-radius: 4px;
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const AutoApprovedIcon = styled.div`
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--online-indicator);
    width: 16px;
    height: 16px;
`;
```

#### Logic Changes

The key insight: when `isAutoApproved` is true and stage is `'call'`, `canApprove` should be treated as `false` for the call stage, so no decision buttons appear.

**In the component body** (line 49, inside the component function), add:

```typescript
// When auto-approved during call stage, suppress approval buttons
const effectiveCanApprove = props.isAutoApproved && isCallStage ? false : props.canApprove;
```

Then replace all references to `props.canApprove` with `effectiveCanApprove` in the component. Specifically:

1. **`decisionToolCalls` memo** (line 64-77): Change `props.canApprove` to `effectiveCanApprove`
2. **`handleToolDecision` function** (line 138): Change `props.canApprove` to `effectiveCanApprove`

**In the JSX return**, add the auto-approved banner. Add it before the `decisionToolCalls.map(...)` block, inside `ToolCallsContainer`:

```tsx
return (
    <ToolCallsContainer>
        {props.isAutoApproved && isCallStage && (
            <AutoApprovedBanner>
                <AutoApprovedIcon>
                    <ShieldOutlineIcon size={16}/>
                </AutoApprovedIcon>
                <FormattedMessage
                    id='ai.tool_call.auto_approved_banner'
                    defaultMessage='These read-only tools were auto-executed per approved server policy.'
                />
            </AutoApprovedBanner>
        )}

        {decisionToolCalls.map((tool) => (
            // ... existing code ...
        ))}
        {/* ... rest of existing code ... */}
    </ToolCallsContainer>
);
```

**Add import** for `ShieldOutlineIcon`:

```typescript
import {ShieldOutlineIcon} from '@mattermost/compass-icons/components';
```

#### Complete Modified `decisionToolCalls` Memo

```typescript
const decisionToolCalls = useMemo(() => {
    if (!effectiveCanApprove) {
        return [];
    }

    if (isCallStage) {
        return props.toolCalls.filter((call) => call.status === ToolCallStatus.Pending);
    }

    return props.toolCalls.filter((call) =>
        call.status === ToolCallStatus.Success ||
        call.status === ToolCallStatus.Error,
    );
}, [props.toolCalls, effectiveCanApprove, isCallStage]);
```

#### Effect on Auto-Submit for Rejected Tools

The existing `useEffect` (line 116-136) that auto-submits when all tools are rejected also references `props.canApprove`. Change to `effectiveCanApprove`:

```typescript
useEffect(() => {
    if (isCallStage || !effectiveCanApprove) {
        return;
    }
    // ... rest unchanged ...
}, [decisionToolCalls.length, isCallStage, isSubmitting, effectiveCanApprove, props.postID, props.toolCalls, submitDecisions]);
```

---

## Step 4.3: Update `ToolCard` for Auto-Approved Visual Indicator

### File: `webapp/src/components/tool_card.tsx`

When a tool was auto-approved, the tool card header should show a subtle indicator instead of the processing spinner.

#### Interface Changes

Add `isAutoApproved` to `ToolCardProps` (line 298-309):

```typescript
interface ToolCardProps {
    tool: ToolCall;
    isCollapsed: boolean;
    isProcessing: boolean;
    onToggleCollapse: () => void;
    onApprove?: () => void;
    onReject?: () => void;
    canExpand: boolean;
    showArguments: boolean;
    showResults: boolean;
    approvalStage?: ToolApprovalStage;
    isAutoApproved?: boolean;  // NEW
}
```

#### New Styled Components

Add after the existing `SmallRejectedIcon` (around line 120):

```typescript
const AutoApprovedBadge = styled.span`
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 0 6px;
    height: 18px;
    border-radius: 9px;
    background: rgba(var(--online-indicator-rgb), 0.12);
    font-size: 10px;
    font-weight: 600;
    line-height: 14px;
    color: var(--online-indicator);
    white-space: nowrap;
`;
```

#### Logic Changes

In the component destructuring (line 311-322), add `isAutoApproved`:

```typescript
const ToolCard: React.FC<ToolCardProps> = ({
    tool,
    isCollapsed,
    isProcessing,
    onToggleCollapse,
    onApprove,
    onReject,
    canExpand,
    showArguments,
    showResults,
    approvalStage = 'call',
    isAutoApproved = false,
}) => {
```

**In the header**, after `<ToolName>{displayName}</ToolName>`, add the badge:

```tsx
<ToolCallHeader
    isCollapsed={isCollapsed}
    $canExpand={canExpand}
    onClick={canExpand ? onToggleCollapse : undefined}
>
    {canExpand && (
        <StyledChevronIcon>
            {isCollapsed ? <ChevronRightIcon size={16}/> : <ChevronDownIcon size={16}/>}
        </StyledChevronIcon>
    )}
    <StatusIcon>
        {showProcessingSpinner && <SmallSpinner/>}
        {!showProcessingSpinner && isSuccess && <SmallSuccessIcon size={16}/>}
        {!showProcessingSpinner && isError && <SmallErrorIcon size={16}/>}
        {!showProcessingSpinner && isRejected && <SmallRejectedIcon size={16}/>}
    </StatusIcon>
    <ToolName>{displayName}</ToolName>
    {isAutoApproved && (
        <AutoApprovedBadge>
            <FormattedMessage
                id='ai.tool_call.auto_approved'
                defaultMessage='Auto-approved'
            />
        </AutoApprovedBadge>
    )}
</ToolCallHeader>
```

#### Pass `isAutoApproved` from `ToolApprovalSet`

In `tool_approval_set.tsx`, pass the prop to each `ToolCard`. Both the `decisionToolCalls.map` and `nonDecisionToolCalls.map` blocks:

```tsx
{decisionToolCalls.map((tool) => (
    <ToolCard
        key={tool.id}
        tool={tool}
        isCollapsed={isToolCollapsed(tool)}
        isProcessing={isSubmitting}
        onToggleCollapse={() => toggleCollapse(tool.id)}
        onApprove={() => handleToolDecision(tool.id, true)}
        onReject={() => handleToolDecision(tool.id, false)}
        canExpand={props.canExpand}
        showArguments={props.showArguments}
        showResults={props.showResults}
        approvalStage={props.approvalStage}
        isAutoApproved={props.isAutoApproved}
    />
))}

{nonDecisionToolCalls.map((tool) => (
    <ToolCard
        key={tool.id}
        tool={tool}
        isCollapsed={isToolCollapsed(tool)}
        isProcessing={false}
        onToggleCollapse={() => toggleCollapse(tool.id)}
        canExpand={props.canExpand}
        showArguments={props.showArguments}
        showResults={props.showResults}
        approvalStage={props.approvalStage}
        isAutoApproved={props.isAutoApproved}
    />
))}
```

---

## Step 4.4: ToolCallStatus Enum - Decision

### File: `webapp/src/components/tool_types.ts`

After analysis, adding an `AutoApproved` status to the TypeScript enum is **NOT recommended** for these reasons:

1. **Backend alignment**: The Go `ToolCallStatus` iota enum (in `llm/tools.go`) uses integer values `0-4`. Adding a new value `5` to the Go side changes serialization and must be coordinated across backend and frontend simultaneously.
2. **The auto-approved state is a post-level property**, not a per-tool-call status. All tools in a batch are either auto-approved or not (all-or-nothing). The `auto_approved_tool_call` post prop already captures this.
3. **Auto-approved tools still transition through standard statuses**: `Pending -> Accepted -> Success/Error`. The auto-approval only skips the user's manual acceptance step; the tool call status values remain the same.

**Decision: Do NOT add `AutoApproved` to `ToolCallStatus`. Use the post property `auto_approved_tool_call` as the sole indicator, which is already detected in `llmbot_post.tsx` and passed down as `isAutoApproved` prop.**

No changes to `tool_types.ts`.

---

## UI Flow Summary

### Standard Tool Call (Non-Auto-Approved) in Channel

```
1. Tool calls appear with Pending status
2. User sees Accept/Reject buttons (call stage)
3. User approves -> tools execute
4. User sees Share/Keep private buttons (result stage)
5. User approves sharing -> bot continues
```

### Auto-Approved Tool Call in Channel

```
1. Tool calls appear (briefly in Pending, quickly move to Success/Error)
2. Banner: "These read-only tools were auto-executed per approved server policy."
3. Each tool card shows "Auto-approved" badge
4. No Accept/Reject buttons in call stage
5. Transitions to result stage automatically
6. User sees Share/Keep private buttons (result stage) - UNCHANGED
7. User approves sharing -> bot continues
```

### Auto-Approved Tool Call in DM

No change. DMs already auto-execute all tools without any approval UI. The `auto_approved_tool_call` prop is not set for DMs.

---

## i18n Message IDs

All new user-facing text uses `react-intl` `FormattedMessage` with proper i18n IDs:

| ID | Default Message | Location |
|----|----------------|----------|
| `ai.tool_call.auto_approved_banner` | `These read-only tools were auto-executed per approved server policy.` | `tool_approval_set.tsx` |
| `ai.tool_call.auto_approved` | `Auto-approved` | `tool_card.tsx` |

These follow the existing `ai.tool_call.*` naming convention used throughout the codebase.

---

## Files Changed Summary

| File | Change Type | Description |
|------|-------------|-------------|
| `webapp/src/components/llmbot_post/llmbot_post.tsx` | MODIFY | Add `isAutoApproved` detection from post props, pass to `ToolApprovalSet` |
| `webapp/src/components/tool_approval_set.tsx` | MODIFY | Add `isAutoApproved` prop, suppress call-stage approval when auto-approved, add `AutoApprovedBanner` styled component, add `ShieldOutlineIcon` import |
| `webapp/src/components/tool_card.tsx` | MODIFY | Add `isAutoApproved` prop, add `AutoApprovedBadge` styled component |
| `webapp/src/components/tool_types.ts` | NO CHANGE | No new enum value needed |

---

## Exact Change Specifications

### 1. `webapp/src/components/llmbot_post/llmbot_post.tsx`

**Add after line 112** (`const hasPendingToolResult = ...`):
```typescript
const isAutoApproved = String(props.post.props?.auto_approved_tool_call).toLowerCase() === 'true';
```

**Modify lines 513-521** (the `<ToolApprovalSet>` JSX). Add one new prop:
```diff
 <ToolApprovalSet
     postID={props.post.id}
     toolCalls={resolvedToolCalls}
     approvalStage={toolApprovalStage}
     canApprove={requesterIsCurrentUser}
     canExpand={requesterIsCurrentUser}
     showArguments={showToolArguments}
     showResults={showToolResults}
+    isAutoApproved={isAutoApproved}
 />
```

### 2. `webapp/src/components/tool_approval_set.tsx`

**Add import** (top of file, with other compass-icons imports - this file doesn't have any yet, add a new import line):
```typescript
import {ShieldOutlineIcon} from '@mattermost/compass-icons/components';
```

**Add styled components** after `StatusBar` (around line 31):
```typescript
const AutoApprovedBanner = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 12px;
    margin-top: 4px;
    background: rgba(var(--online-indicator-rgb), 0.08);
    border-radius: 4px;
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const AutoApprovedIcon = styled.div`
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--online-indicator);
    width: 16px;
    height: 16px;
`;
```

**Modify interface** (add `isAutoApproved`):
```diff
 interface ToolApprovalSetProps {
     postID: string;
     toolCalls: ToolCall[];
     approvalStage: ToolApprovalStage;
     canApprove: boolean;
     canExpand: boolean;
     showArguments: boolean;
     showResults: boolean;
+    isAutoApproved?: boolean;
 }
```

**Add `effectiveCanApprove`** in the component body, right after `const isCallStage = ...` (line 62):
```typescript
const effectiveCanApprove = props.isAutoApproved && isCallStage ? false : props.canApprove;
```

**Replace `props.canApprove`** with `effectiveCanApprove` in three places:
1. `decisionToolCalls` useMemo (line 65): `if (!effectiveCanApprove) {`
2. `decisionToolCalls` useMemo dependency array (line 77): `[props.toolCalls, effectiveCanApprove, isCallStage]`
3. `handleToolDecision` function (line 139): `if (!effectiveCanApprove || ...`
4. Auto-submit useEffect (line 117): `if (isCallStage || !effectiveCanApprove) {`
5. Auto-submit useEffect dependency array (line 136): replace `props.canApprove` with `effectiveCanApprove`

**Add auto-approved banner in JSX** as first child of `ToolCallsContainer`:
```tsx
{props.isAutoApproved && isCallStage && (
    <AutoApprovedBanner>
        <AutoApprovedIcon>
            <ShieldOutlineIcon size={16}/>
        </AutoApprovedIcon>
        <FormattedMessage
            id='ai.tool_call.auto_approved_banner'
            defaultMessage='These read-only tools were auto-executed per approved server policy.'
        />
    </AutoApprovedBanner>
)}
```

**Pass `isAutoApproved` to ToolCard** in both `.map()` calls:
```diff
 <ToolCard
     key={tool.id}
     tool={tool}
     ...
     approvalStage={props.approvalStage}
+    isAutoApproved={props.isAutoApproved}
 />
```

### 3. `webapp/src/components/tool_card.tsx`

**Add styled component** after `SmallRejectedIcon` (around line 120):
```typescript
const AutoApprovedBadge = styled.span`
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 0 6px;
    height: 18px;
    border-radius: 9px;
    background: rgba(var(--online-indicator-rgb), 0.12);
    font-size: 10px;
    font-weight: 600;
    line-height: 14px;
    color: var(--online-indicator);
    white-space: nowrap;
`;
```

**Modify interface** (add `isAutoApproved`):
```diff
 interface ToolCardProps {
     tool: ToolCall;
     isCollapsed: boolean;
     isProcessing: boolean;
     onToggleCollapse: () => void;
     onApprove?: () => void;
     onReject?: () => void;
     canExpand: boolean;
     showArguments: boolean;
     showResults: boolean;
     approvalStage?: ToolApprovalStage;
+    isAutoApproved?: boolean;
 }
```

**Add to destructuring** (line 311-322):
```diff
 const ToolCard: React.FC<ToolCardProps> = ({
     ...
     approvalStage = 'call',
+    isAutoApproved = false,
 }) => {
```

**Add badge in JSX header** after `<ToolName>{displayName}</ToolName>` (line 418):
```tsx
<ToolName>{displayName}</ToolName>
{isAutoApproved && (
    <AutoApprovedBadge>
        <FormattedMessage
            id='ai.tool_call.auto_approved'
            defaultMessage='Auto-approved'
        />
    </AutoApprovedBadge>
)}
```

---

## What Does NOT Change

1. **Result approval stage** (`'result'`): The Share/Keep private buttons and result review callout render identically for auto-approved and standard tools. The `isAutoApproved` flag does NOT suppress result-stage approval.
2. **DM flow**: DMs never show approval UI anyway. No changes.
3. **`tool_types.ts`**: No new enum values.
4. **API calls**: `doToolCall()`, `doToolResult()`, `getToolCallPrivate()`, `getToolResultPrivate()` remain unchanged.
5. **WebSocket handling**: The `tool_call` websocket event handler in `llmbot_post.tsx` remains unchanged. It already correctly updates tool call state regardless of auto-approval.

---

## Edge Cases

1. **Brief flash during auto-execution**: Between the backend setting `auto_approved_tool_call` and `HandleToolCall` completing, the frontend sees call stage with auto-approved. The banner displays correctly. Once tools complete and `pending_tool_result` is set, the stage transitions to `'result'` and the banner disappears (replaced by normal result approval UI).

2. **Non-requester users**: Non-requester users see the tool cards without any approval buttons (existing behavior via `canApprove={requesterIsCurrentUser}`). They will also see the auto-approved badge on each tool card, which is informational. They will NOT see the banner (because `isCallStage` with `effectiveCanApprove=false` means no decision tools, and the banner is gated on `isAutoApproved && isCallStage`). Actually, the banner should be visible to all users for informational purposes. Let's gate it only on `props.isAutoApproved && isCallStage` (not on `canApprove`), so all channel members see the informational banner.

3. **Plugin restart mid-execution**: If the plugin restarts between setting `auto_approved_tool_call` and completing execution, the post stays in call stage with `auto_approved_tool_call=true` but `pending_tool_result` never gets set. The user will see the auto-approved banner with no buttons. This is a known acceptable edge case from Phase 3 (the tools remain in pending state). A page refresh would show the same state. The user can regenerate to retry.

4. **Mixed approval in batch**: Phase 3 uses all-or-nothing. If ANY tool is not auto-approvable, `auto_approved_tool_call` is NOT set, and the standard full approval flow is used. The frontend never needs to handle partial auto-approval.

---

## Testing Considerations

### Manual Testing Scenarios

1. **Auto-approved tool call in channel**:
   - Configure an approved MCP server with READ tools
   - Trigger a tool call in a channel that uses only approved READ tools
   - Verify: Banner appears saying tools were auto-executed
   - Verify: Each tool card shows "Auto-approved" badge
   - Verify: No Accept/Reject buttons during call stage
   - Verify: Result approval (Share/Keep private) appears normally after execution

2. **Standard tool call in channel** (regression):
   - Trigger a tool call that is NOT auto-approvable
   - Verify: No banner, no badge
   - Verify: Accept/Reject buttons appear normally
   - Verify: Result approval works normally

3. **DM tool call** (regression):
   - Trigger any tool call in a DM
   - Verify: No banner, no badge, no approval UI (existing behavior)

4. **Non-requester viewing auto-approved tools**:
   - As a different user, view a post with auto-approved tools
   - Verify: Banner is visible (informational)
   - Verify: Badges are visible
   - Verify: No approval buttons

### Future E2E Tests (Phase 6)

Playwright tests should cover:
- Auto-approved tools show badge and banner
- Result approval UI still appears for auto-approved tools
- Standard approval flow unchanged for non-approved tools
- Selector: `[data-testid="auto-approved-banner"]` (if test IDs are added)

---

## Implementation Summary (Completed by implementer-4)

All Phase 4 frontend changes have been implemented as specified in this plan.

### Files Modified

1. **`webapp/src/components/llmbot_post/llmbot_post.tsx`**
   - Added `isAutoApproved` derived from `props.post.props?.auto_approved_tool_call` (line 113)
   - Passed `isAutoApproved` prop to `ToolApprovalSet` component (line 522)

2. **`webapp/src/components/tool_approval_set.tsx`**
   - Added `ShieldOutlineIcon` import from `@mattermost/compass-icons/components`
   - Added `AutoApprovedBanner` and `AutoApprovedIcon` styled components
   - Added `isAutoApproved?: boolean` to `ToolApprovalSetProps` interface
   - Added `effectiveCanApprove` logic to suppress call-stage approval when auto-approved
   - Replaced all `props.canApprove` references with `effectiveCanApprove` in `decisionToolCalls` memo, auto-submit useEffect, and `handleToolDecision`
   - Added auto-approved banner JSX (with shield icon and i18n message) as first child of `ToolCallsContainer`
   - Passed `isAutoApproved` prop to all `ToolCard` instances

3. **`webapp/src/components/tool_card.tsx`**
   - Added `AutoApprovedBadge` styled component
   - Added `isAutoApproved?: boolean` to `ToolCardProps` interface
   - Added `isAutoApproved = false` to component destructuring
   - Added badge JSX after `ToolName` in the header

### i18n Strings Added

| ID | Default Message |
|----|----------------|
| `ai.tool_call.auto_approved_banner` | `These read-only tools were auto-executed per approved server policy.` |
| `ai.tool_call.auto_approved` | `Auto-approved` |

### No Changes Made To

- `webapp/src/components/tool_types.ts` (no new enum values needed, as planned)
- Result-stage approval flow (unchanged)
- DM flow (unchanged)
- API calls and WebSocket handling (unchanged)
