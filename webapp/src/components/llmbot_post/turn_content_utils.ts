// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {
    BlockTypeThinking,
    BlockTypeToolUse,
    BlockTypeToolResult,
    BlockTypeAnnotations,
    BlockTypeText,
    StatusPending,
    StatusAccepted,
    StatusRejected,
    StatusError,
    StatusSuccess,
    StatusAutoApproved,
    type ConversationResponse,
    type ContentBlock,
    type Turn,
    type ToolCallStatus as ConvToolCallStatus,
} from '@/types/conversation';

import {ToolApprovalStage, ToolCall, ToolCallStatus} from '../tool_types';
import {Annotation} from '../citations/types';

/** Map a string-based tool call status from the conversation API to the numeric enum used by ToolCard / ToolApprovalSet. */
export function statusStringToEnum(status: ConvToolCallStatus | undefined): ToolCallStatus {
    switch (status) {
    case StatusPending:
        return ToolCallStatus.Pending;
    case StatusAccepted:
        return ToolCallStatus.Accepted;
    case StatusRejected:
        return ToolCallStatus.Rejected;
    case StatusError:
        return ToolCallStatus.Error;
    case StatusSuccess:
        return ToolCallStatus.Success;
    case StatusAutoApproved:
        return ToolCallStatus.AutoApproved;
    default:
        return ToolCallStatus.Pending;
    }
}

/**
 * Collect all turns that belong to the same assistant response as the post
 * identified by `postId`. The anchor is the turn whose post_id matches; the
 * streaming layer creates this turn at finalize with the highest sequence in
 * the response, so tool-round turns that WriteToolTurns persisted during the
 * stream sit BEFORE it. We walk backwards from the anchor, stopping at the
 * user turn that introduced this response, and include the anchor itself.
 */
function collectResponseTurns(
    conversation: ConversationResponse,
    postId: string,
): Turn[] {
    const sorted = [...conversation.turns].sort((a, b) => a.sequence - b.sequence);
    const anchorIdx = sorted.findIndex((t) => t.post_id === postId);
    if (anchorIdx === -1) {
        return [];
    }

    const out: Turn[] = [];
    for (let i = anchorIdx - 1; i >= 0; i--) {
        const t = sorted[i];
        if (t.role === 'user') {
            break;
        }

        // Stop when we cross into another post's response — its anchor turn
        // has a post_id of its own. Without this, an approval-continuation
        // post would also sweep in the preceding post's tool_use blocks.
        if (t.post_id && t.post_id !== postId) {
            break;
        }
        out.unshift(t);
    }
    out.push(sorted[anchorIdx]);
    return out;
}

/**
 * Build a ToolCall[] from every tool_use block across the turns that belong
 * to a given post's response, pairing each with its matching tool_result by
 * id. The result is compatible with the existing ToolApprovalSet / ToolCard
 * interfaces.
 */
export function extractToolCallsForPost(
    conversation: ConversationResponse,
    postId: string,
): ToolCall[] {
    const turns = collectResponseTurns(conversation, postId);
    if (turns.length === 0) {
        return [];
    }

    const toolUseBlocks: ContentBlock[] = [];
    for (const t of turns) {
        for (const block of t.content) {
            if (block.type === BlockTypeToolUse) {
                toolUseBlocks.push(block);
            }
        }
    }

    if (toolUseBlocks.length === 0) {
        return [];
    }

    // Results may land AFTER the anchor when the user just approved
    // previously pending tool calls, so search every turn by tool_use_id
    // rather than only the collected response range.
    const resultMap = new Map<string, ContentBlock>();
    for (const t of conversation.turns) {
        for (const block of t.content) {
            if (block.type === BlockTypeToolResult && block.tool_use_id) {
                resultMap.set(block.tool_use_id, block);
            }
        }
    }

    return toolUseBlocks.map((block: ContentBlock): ToolCall => {
        const resultBlock = block.id ? resultMap.get(block.id) : undefined; // eslint-disable-line no-undefined
        return {
            id: block.id ?? '',
            name: block.name ?? '',
            description: '',
            arguments: (block.input as ToolCall['arguments']) ?? undefined, // eslint-disable-line no-undefined
            result: resultBlock?.content ?? undefined, // eslint-disable-line no-undefined
            status: statusStringToEnum(block.status),
        };
    });
}

/** Extract reasoning summary text and signature from thinking content blocks. */
export function extractReasoningFromTurn(turn: Turn): {summary: string; signature: string} {
    const thinkingBlocks = turn.content.filter(
        (b: ContentBlock) => b.type === BlockTypeThinking,
    );
    if (thinkingBlocks.length === 0) {
        return {summary: '', signature: ''};
    }

    // Concatenate all thinking blocks (typically there is only one).
    const summary = thinkingBlocks.map((b) => b.text ?? '').join('\n');
    const signature = thinkingBlocks[thinkingBlocks.length - 1]?.signature ?? '';
    return {summary, signature};
}

/** Extract Annotation[] from annotation blocks and citations on text blocks. */
export function extractAnnotationsFromTurn(turn: Turn): Annotation[] {
    const annotations: Annotation[] = [];
    let runningIndex = 0;

    for (const block of turn.content) {
        // Annotations block (web search context citations). The streamer
        // persists the live annotations array verbatim into web_search_context.results,
        // so we surface those without re-deriving indices.
        if (block.type === BlockTypeAnnotations && block.web_search_context) {
            const results = block.web_search_context.results;
            if (Array.isArray(results)) {
                for (const r of results as Partial<Annotation>[]) {
                    if (r && r.type === 'url_citation') {
                        annotations.push({
                            type: 'url_citation',
                            start_index: r.start_index ?? 0,
                            end_index: r.end_index ?? 0,
                            url: r.url,
                            title: r.title,
                            cited_text: r.cited_text,
                            index: r.index ?? runningIndex,
                        });
                        runningIndex++;
                    }
                }
            }
        }

        // Text block with inline citations
        if (block.type === BlockTypeText && block.citations) {
            for (let i = 0; i < block.citations.length; i++) {
                const c = block.citations[i];
                annotations.push({
                    type: 'url_citation',
                    start_index: c.start_index,
                    end_index: c.end_index,
                    url: c.url,
                    title: c.title,
                    index: runningIndex,
                });
                runningIndex++;
            }
        }
    }

    return annotations;
}

/**
 * Returns the server-computed approval stage for the post's anchor turn.
 * Defaults to 'done' (no buttons) when the anchor or the field is missing —
 * safer than defaulting to a stage that would render approval controls.
 */
export function deriveApprovalStageForPost(
    conversation: ConversationResponse,
    postId: string,
): ToolApprovalStage {
    const anchor = conversation.turns.find(
        (t) => t.post_id === postId && t.role === 'assistant',
    );
    return anchor?.approval_state ?? 'done';
}

/** True if any tool_use block across the post's response has auto_approved status. */
export function hasAutoApprovedToolsForPost(
    conversation: ConversationResponse,
    postId: string,
): boolean {
    const turns = collectResponseTurns(conversation, postId);
    return turns.some((t) =>
        t.content.some(
            (b: ContentBlock) => b.type === BlockTypeToolUse && b.status === StatusAutoApproved,
        ),
    );
}
