// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {ToolCall, ToolCallStatus} from '../tool_types';

import {Round} from './turn_content_utils';

/** An intermediate assistant text snippet — text that was followed by tool calls. */
export interface ActivityTextItem {
    kind: 'text';
    id: string;
    roundId: string;
    text: string;
}

/** A single tool call, at whatever status it currently holds. */
export interface ActivityToolItem {
    kind: 'tool';
    id: string;
    roundId: string;
    toolCall: ToolCall;
}

export type ActivityItem = ActivityTextItem | ActivityToolItem;

export interface PostActivity {

    /**
     * The rounds that fold into the activity area: every round up to and
     * including the last one that has tool calls, plus — while
     * `foldTrailingText` is set — the text-only rounds that trail it.
     */
    activityRounds: Round[];

    /**
     * The rounds left over. Their text is the answer and renders as the normal
     * post message.
     */
    answerRounds: Round[];

    /** Every activity item in chronological order. */
    items: ActivityItem[];

    toolCount: number;

    /** True while at least one tool call has not reached a terminal status. */
    hasRunningTool: boolean;

    hasError: boolean;
    hasRejected: boolean;
}

/** True once a tool call can no longer change status on its own. */
export function isTerminalToolStatus(status: ToolCallStatus): boolean {
    switch (status) {
    case ToolCallStatus.Success:
    case ToolCallStatus.Error:
    case ToolCallStatus.AutoApproved:
    case ToolCallStatus.Rejected:
        return true;
    case ToolCallStatus.Pending:
    case ToolCallStatus.Accepted:
        return false;
    default: {
        const exhaustive: never = status;
        return exhaustive;
    }
    }
}

export interface DeriveActivityOptions {

    /**
     * A round the viewer still owes a decision on. It and everything after it
     * stay out of the activity area so the approval card renders in full, next
     * to the text that asked for it. Whether a round needs a decision depends
     * on who is looking, which is why the caller decides it and this function
     * stays pure.
     */
    pendingDecisionRoundId?: string;

    /**
     * Route trailing text-only rounds into the activity area instead of
     * treating them as the answer.
     *
     * Set while a response that has already called a tool is still streaming
     * with the area collapsed. Such text is narration between tool calls far
     * more often than it is the answer, and putting it in the main area only
     * to pull it back out the moment the next tool call lands is the layout
     * jump this exists to prevent. Once the response settles the caller drops
     * the flag and the trailing round becomes the answer.
     */
    foldTrailingText?: boolean;
}

/**
 * Split a post's rounds into the collapsible activity area and the answer,
 * and flatten the activity area into the chronological item list the
 * collapsed row steps through.
 *
 * A post with no tool calls anywhere produces no items and no activity
 * rounds, so it renders exactly as it did before the activity area existed.
 */
export function deriveActivity(rounds: Round[], options: DeriveActivityOptions = {}): PostActivity {
    const {pendingDecisionRoundId, foldTrailingText = false} = options;

    const pendingIdx = pendingDecisionRoundId === undefined ? // eslint-disable-line no-undefined
        -1 :
        rounds.findIndex((round) => round.id === pendingDecisionRoundId);
    const searchEnd = pendingIdx === -1 ? rounds.length : pendingIdx;

    let lastToolRoundIdx = -1;
    for (let i = searchEnd - 1; i >= 0; i--) {
        if (rounds[i].toolCalls.length > 0) {
            lastToolRoundIdx = i;
            break;
        }
    }

    if (lastToolRoundIdx === -1) {
        return {
            activityRounds: [],
            answerRounds: rounds,
            items: [],
            toolCount: 0,
            hasRunningTool: false,
            hasError: false,
            hasRejected: false,
        };
    }

    // A pending-decision round is never folded, so the trailing sweep stops
    // where the ordinary search did.
    const splitIdx = foldTrailingText ? searchEnd : lastToolRoundIdx + 1;
    const activityRounds = rounds.slice(0, splitIdx);
    const answerRounds = rounds.slice(splitIdx);

    const items: ActivityItem[] = [];
    let toolCount = 0;
    let hasRunningTool = false;
    let hasError = false;
    let hasRejected = false;

    for (const round of activityRounds) {
        // The collapsed row is a single line, so the snippet carries no
        // internal line breaks.
        const text = round.text.trim().replace(/\s+/g, ' ');
        if (text !== '') {
            items.push({kind: 'text', id: `${round.id}:text`, roundId: round.id, text});
        }

        for (const toolCall of round.toolCalls) {
            items.push({
                kind: 'tool',
                id: `${round.id}:tool:${toolCall.id}`,
                roundId: round.id,
                toolCall,
            });
            toolCount++;
            if (!isTerminalToolStatus(toolCall.status)) {
                hasRunningTool = true;
            }
            if (toolCall.status === ToolCallStatus.Error) {
                hasError = true;
            }
            if (toolCall.status === ToolCallStatus.Rejected) {
                hasRejected = true;
            }
        }
    }

    return {activityRounds, answerRounds, items, toolCount, hasRunningTool, hasError, hasRejected};
}
