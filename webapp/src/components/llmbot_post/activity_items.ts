// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {ServerToolStatusError, ServerToolStatusInProgress, ServerToolUse} from '@/types/conversation';

import {ToolCall, ToolCallStatus} from '../tool_types';

import {Round} from './turn_content_utils';

/** A client tool call (MCP or built-in). */
export interface ActivityToolItem {
    kind: 'tool';
    id: string;
    status: ToolCallStatus;
    toolCall: ToolCall;
}

/** A provider-executed tool such as web search or sandboxed code. */
export interface ActivityServerToolItem {
    kind: 'server_tool';
    id: string;
    status: ToolCallStatus;
    serverTool: ServerToolUse;
}

export type ActivityItem = ActivityToolItem | ActivityServerToolItem;

export interface PostActivity {

    /** Rounds folded into the activity area, ending with the last round that used a tool. */
    activityRounds: Round[];

    /** Everything after that; its text renders as the post message. */
    answerRounds: Round[];

    /** Every tool invocation in the activity area, in order. */
    items: ActivityItem[];

    /** A client tool call can wait on approval after the stream ends; provider tools cannot. */
    hasRunningTool: boolean;

    hasError: boolean;
    hasRejected: boolean;
}

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

export function serverToolStatus(tool: ServerToolUse): ToolCallStatus {
    switch (tool.status) {
    case ServerToolStatusInProgress:
        return ToolCallStatus.Pending;
    case ServerToolStatusError:
        return ToolCallStatus.Error;
    default:
        return ToolCallStatus.Success;
    }
}

function usesTools(round: Round): boolean {
    return round.toolCalls.length > 0 || round.serverTools.length > 0;
}

// Split parts are cached per round so settled rounds keep their identity and
// the memoized RoundView does not re-render them on every streamed chunk.
const splitCache = new WeakMap<Round, {activity: Round; answer: Round}>();

function splitAnswerText(round: Round): {activity: Round; answer: Round} {
    let parts = splitCache.get(round);
    if (!parts) {
        parts = {
            activity: {...round, text: '', annotations: []},
            answer: {...round, reasoning: {summary: '', signature: ''}, serverTools: []},
        };
        splitCache.set(round, parts);
    }
    return parts;
}

export interface DeriveActivityOptions {

    /**
     * A round the viewer still owes a decision on. It and everything after it
     * stay out of the activity area so the approval card renders in full.
     */
    pendingDecisionRoundId?: string;
}

/**
 * Split a post's rounds into the collapsible activity area and the answer.
 * Text is only folded once a later tool invocation shows it was narration, so
 * the answer streams into the post body as it always did. A post that never
 * used a tool produces no activity.
 */
export function deriveActivity(rounds: Round[], options: DeriveActivityOptions = {}): PostActivity {
    const pendingIdx = options.pendingDecisionRoundId === undefined ? // eslint-disable-line no-undefined
        -1 :
        rounds.findIndex((round) => round.id === options.pendingDecisionRoundId);
    const searchEnd = pendingIdx === -1 ? rounds.length : pendingIdx;

    let lastToolRoundIdx = -1;
    for (let i = searchEnd - 1; i >= 0; i--) {
        if (usesTools(rounds[i])) {
            lastToolRoundIdx = i;
            break;
        }
    }

    if (lastToolRoundIdx === -1) {
        return {activityRounds: [], answerRounds: rounds, items: [], hasRunningTool: false, hasError: false, hasRejected: false};
    }

    // A round renders provider tools before its text and client tool calls
    // after it, so only text that follows provider tools can be the answer.
    const lastToolRound = rounds[lastToolRoundIdx];
    const activityRounds = rounds.slice(0, lastToolRoundIdx);
    const answerRounds = rounds.slice(lastToolRoundIdx + 1);
    if (lastToolRound.toolCalls.length === 0 && lastToolRound.text !== '') {
        const {activity, answer} = splitAnswerText(lastToolRound);
        activityRounds.push(activity);
        answerRounds.unshift(answer);
    } else {
        activityRounds.push(lastToolRound);
    }

    // Keyed by invocation id, which survives the refetch that replaces live rounds.
    const items: ActivityItem[] = [];
    for (const round of activityRounds) {
        round.serverTools.forEach((serverTool, idx) => {
            items.push({kind: 'server_tool', id: `server:${serverTool.id || `${round.id}:${idx}`}`, status: serverToolStatus(serverTool), serverTool});
        });
        for (const toolCall of round.toolCalls) {
            items.push({kind: 'tool', id: `tool:${toolCall.id}`, status: toolCall.status, toolCall});
        }
    }

    return {
        activityRounds,
        answerRounds,
        items,
        hasRunningTool: items.some((item) => item.kind === 'tool' && !isTerminalToolStatus(item.status)),
        hasError: items.some((item) => item.status === ToolCallStatus.Error),
        hasRejected: items.some((item) => item.status === ToolCallStatus.Rejected),
    };
}
