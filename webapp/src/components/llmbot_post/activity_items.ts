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

/** A reasoning block of a post that also used tools. */
export interface ActivityReasoningItem {
    kind: 'reasoning';
    id: string;
    status: ToolCallStatus;
}

export type ActivityItem = ActivityToolItem | ActivityServerToolItem | ActivityReasoningItem;

export interface PostActivity {

    /** Rounds folded into the activity area, ending with the last round that used a tool or reasoned. */
    activityRounds: Round[];

    /** Everything after that; its text renders as the post message. */
    answerRounds: Round[];

    /** Every tool invocation and reasoning block in the activity area, in order. */
    items: ActivityItem[];

    /** Tool invocations only, for the "Used N tools" summary. */
    toolCount: number;

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

function hasActivity(round: Round): boolean {
    return usesTools(round) || round.reasoning.summary !== '';
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

    /** The round whose reasoning is still streaming. */
    reasoningLoadingRoundId?: string;

    /**
     * The response is still streaming. Reasoning then joins the activity even
     * before any tool runs, so the status line carries it; a post that ends
     * without tools hands it back to its own row.
     */
    live?: boolean;
}

function splitRounds(rounds: Round[], searchEnd: number, live: boolean): {activityRounds: Round[]; answerRounds: Round[]} {
    const considered = rounds.slice(0, searchEnd);
    if (!considered.some(usesTools)) {
        if (!live || !considered.some(hasActivity)) {
            return {activityRounds: [], answerRounds: rounds};
        }

        // No tool has shown the text to be narration yet, so only reasoning moves.
        const activityRounds: Round[] = [];
        const answerRounds: Round[] = [];
        rounds.forEach((round, idx) => {
            if (idx >= searchEnd || round.reasoning.summary === '') {
                answerRounds.push(round);
            } else if (round.text === '') {
                activityRounds.push(round);
            } else {
                const {activity, answer} = splitAnswerText(round);
                activityRounds.push(activity);
                answerRounds.push(answer);
            }
        });
        return {activityRounds, answerRounds};
    }

    let lastActivityRoundIdx = searchEnd - 1;
    while (!hasActivity(rounds[lastActivityRoundIdx])) {
        lastActivityRoundIdx--;
    }

    // A round renders reasoning and provider tools before its text and client
    // tool calls after it, so only text without client calls can be the answer.
    const lastActivityRound = rounds[lastActivityRoundIdx];
    const activityRounds = rounds.slice(0, lastActivityRoundIdx);
    const answerRounds = rounds.slice(lastActivityRoundIdx + 1);
    if (lastActivityRound.toolCalls.length === 0 && lastActivityRound.text !== '') {
        const {activity, answer} = splitAnswerText(lastActivityRound);
        activityRounds.push(activity);
        answerRounds.unshift(answer);
    } else {
        activityRounds.push(lastActivityRound);
    }
    return {activityRounds, answerRounds};
}

/**
 * Split a post's rounds into the collapsible activity area and the answer.
 * Text is only folded once a later tool invocation or reasoning block shows
 * it was narration, so the answer streams into the post body as it always
 * did. A finished post that never used a tool produces no activity, so its
 * reasoning keeps its own row.
 */
export function deriveActivity(rounds: Round[], options: DeriveActivityOptions = {}): PostActivity {
    const pendingIdx = options.pendingDecisionRoundId === undefined ? // eslint-disable-line no-undefined
        -1 :
        rounds.findIndex((round) => round.id === options.pendingDecisionRoundId);
    const searchEnd = pendingIdx === -1 ? rounds.length : pendingIdx;

    const {activityRounds, answerRounds} = splitRounds(rounds, searchEnd, options.live ?? false);

    // Tools are keyed by invocation id, which survives the refetch that replaces live rounds.
    const items: ActivityItem[] = [];
    for (const round of activityRounds) {
        if (round.reasoning.summary !== '') {
            const loading = round.id === options.reasoningLoadingRoundId;
            items.push({kind: 'reasoning', id: `reasoning:${round.id}`, status: loading ? ToolCallStatus.Pending : ToolCallStatus.Success});
        }
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
        toolCount: items.filter((item) => item.kind !== 'reasoning').length,
        hasRunningTool: items.some((item) => item.kind === 'tool' && !isTerminalToolStatus(item.status)),
        hasError: items.some((item) => item.status === ToolCallStatus.Error),
        hasRejected: items.some((item) => item.status === ToolCallStatus.Rejected),
    };
}
