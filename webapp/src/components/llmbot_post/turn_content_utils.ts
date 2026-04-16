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
 * Build a ToolCall[] from a turn's content blocks and the subsequent
 * tool_result turn(s) in the conversation.  The result is compatible with
 * the existing ToolApprovalSet / ToolCard interfaces.
 */
export function extractToolCallsFromTurn(
    turn: Turn,
    conversation: ConversationResponse,
): ToolCall[] {
    const toolUseBlocks = turn.content.filter(
        (b: ContentBlock) => b.type === BlockTypeToolUse,
    );

    if (toolUseBlocks.length === 0) {
        return [];
    }

    // Collect tool_result blocks from the next turn(s) after this assistant turn.
    const resultMap = new Map<string, ContentBlock>();
    for (const t of conversation.turns) {
        if (t.sequence <= turn.sequence) {
            continue;
        }
        if (t.role !== 'tool_result') {
            break;
        }
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
        // Annotations block (web search context citations)
        if (block.type === BlockTypeAnnotations && block.web_search_context) {
            // The web_search_context field is surfaced directly as annotations
            // following the same shape the streaming path uses.
            // TODO: map web_search_context.results to Annotation[] when backend provides them.
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
 * Determine whether the tool approval UI should show the 'call' stage
 * (accept/reject tool execution) or the 'result' stage (share/keep-private).
 */
export function deriveApprovalStage(
    turn: Turn,
    conversation: ConversationResponse,
): ToolApprovalStage {
    const toolUseBlocks = turn.content.filter(
        (b: ContentBlock) => b.type === BlockTypeToolUse,
    );

    if (toolUseBlocks.length === 0) {
        return 'call';
    }

    // Collect tool_use IDs from this turn.
    const toolUseIDs = new Set(
        toolUseBlocks.map((b) => b.id).filter(Boolean),
    );

    // Search all subsequent turns for tool_result blocks that reference
    // one of this turn's tool_use IDs.  We cannot simply walk sequentially
    // and break on the first non-tool_result turn because WriteToolTurns
    // may insert intermediate assistant turns between the streaming turn
    // and the corresponding tool_result turn.
    for (const t of conversation.turns) {
        if (t.sequence <= turn.sequence) {
            continue;
        }
        for (const block of t.content) {
            if (
                block.type === BlockTypeToolResult &&
                block.tool_use_id &&
                toolUseIDs.has(block.tool_use_id)
            ) {
                return 'result';
            }
        }
    }

    return 'call';
}

/** Check whether any tool_use block in the turn has auto_approved status. */
export function hasAutoApprovedTools(turn: Turn): boolean {
    return turn.content.some(
        (b: ContentBlock) => b.type === BlockTypeToolUse && b.status === StatusAutoApproved,
    );
}
