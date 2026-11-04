// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {ToolApprovalStage, ToolCall, ToolCallStatus} from './tool_types';

/** The tool calls in a round that still need a per-call decision from this viewer. */
export function selectDecisionToolCalls(
    toolCalls: ToolCall[],
    approvalStage: ToolApprovalStage,
    canApprove: boolean,
): ToolCall[] {
    if (!canApprove) {
        return [];
    }

    if (approvalStage === 'call') {
        // Calls that pass the auto-execution policy run server-side once the rest of the batch resolves.
        return toolCalls.filter((call) =>
            call.status === ToolCallStatus.Pending && !call.would_auto_execute,
        );
    }

    if (approvalStage !== 'result') {
        return [];
    }

    // User-interaction results are decided at answer time and auto-executed
    // results at write time, so neither needs a share/keep-private decision.
    return toolCalls.filter((call) =>
        !call.user_interaction &&
        !call.decided &&
        (call.status === ToolCallStatus.Success ||
        call.status === ToolCallStatus.Error ||
        call.status === ToolCallStatus.AutoApproved),
    );
}

/**
 * A persisted call-stage round whose pending calls would all auto-execute but
 * was interrupted; it needs a single "Run tools" confirmation.
 */
export function isInterruptedAutoApprovalRound(
    toolCalls: ToolCall[],
    approvalStage: ToolApprovalStage,
): boolean {
    if (approvalStage !== 'call') {
        return false;
    }
    const pending = toolCalls.filter((call) => call.status === ToolCallStatus.Pending);
    return pending.length > 0 && pending.every((call) => call.would_auto_execute);
}

/** True when this viewer owes the round any kind of decision. */
export function needsViewerDecision(
    toolCalls: ToolCall[],
    approvalStage: ToolApprovalStage,
    canApprove: boolean,
): boolean {
    if (!canApprove) {
        return false;
    }
    return selectDecisionToolCalls(toolCalls, approvalStage, canApprove).length > 0 ||
        isInterruptedAutoApprovalRound(toolCalls, approvalStage);
}
