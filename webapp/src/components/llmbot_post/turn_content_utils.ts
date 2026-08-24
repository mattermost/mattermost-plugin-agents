// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {
    BlockTypeThinking,
    BlockTypeToolUse,
    BlockTypeToolResult,
    BlockTypeAnnotations,
    BlockTypeText,
    BlockTypeServerToolUse,
    StatusPending,
    StatusAccepted,
    StatusRejected,
    StatusError,
    StatusSuccess,
    StatusAutoApproved,
    type ConversationResponse,
    type ContentBlock,
    type ServerToolUse,
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

// Index every tool_result block in the conversation by tool_use_id so a
// tool_use can be paired regardless of which turn its result lives in.
function buildToolResultMap(conversation: ConversationResponse): Map<string, ContentBlock> {
    const resultMap = new Map<string, ContentBlock>();
    for (const t of conversation.turns) {
        for (const block of t.content) {
            if (block.type === BlockTypeToolResult && block.tool_use_id) {
                resultMap.set(block.tool_use_id, block);
            }
        }
    }
    return resultMap;
}

function toolUseBlockToToolCall(block: ContentBlock, resultMap: Map<string, ContentBlock>): ToolCall {
    const resultBlock = block.id ? resultMap.get(block.id) : undefined; // eslint-disable-line no-undefined
    return {
        id: block.id ?? '',
        name: block.name ?? '',
        description: '',
        arguments: (block.input as ToolCall['arguments']) ?? undefined, // eslint-disable-line no-undefined
        result: resultBlock?.content ?? undefined, // eslint-disable-line no-undefined
        status: statusStringToEnum(block.status),
        user_interaction: block.user_interaction ?? undefined, // eslint-disable-line no-undefined
        would_auto_execute: block.would_auto_execute ?? undefined, // eslint-disable-line no-undefined
        decided: resultBlock?.decided_at != null,
    };
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

    const resultMap = buildToolResultMap(conversation);
    return toolUseBlocks.map((block) => toolUseBlockToToolCall(block, resultMap));
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

// One assistant turn in a response. The post renders these as a vertical
// sequence: text → tools → text → tools → final text.
export interface Round {
    id: string;
    text: string;
    toolCalls: ToolCall[];
    reasoning: {summary: string; signature: string};
    annotations: Annotation[];

    // Provider-executed (server) tool activity for the round: web searches,
    // page fetches, sandbox code runs. Rendered before the text since the
    // activity precedes the final answer.
    serverTools: ServerToolUse[];
}

/** Extract ServerToolUse[] from server_tool_use content blocks. */
export function extractServerToolsFromTurn(turn: Turn): ServerToolUse[] {
    const serverTools: ServerToolUse[] = [];
    for (const block of turn.content) {
        if (block.type === BlockTypeServerToolUse && block.server_tool) {
            serverTools.push(block.server_tool);
        }
    }
    return serverTools;
}

/**
 * Assemble the rounds to render for a post from its persisted rounds, the
 * rounds completed live during the current stream, and the in-progress round.
 *
 * Legacy posts (e.g. meeting summaries) have no conversation entity, so their
 * content never lands in `persistedRounds` — it lives only in the live round.
 * For those posts the live round must stay rendered after streaming ends;
 * otherwise the summary vanishes the moment `generating` flips to false.
 * Conversation posts also keep the live current round visible while refetching
 * or waiting for persisted rounds, preventing content from disappearing.
 */
export function computeRenderedRounds(params: {
    regenerating: boolean;
    hasConversation: boolean;
    persistedRounds: Round[];
    liveRounds: Round[];
    generating: boolean;
    pendingRefetch?: boolean;
    currentRound: Round | null;
}): Round[] {
    const {regenerating, hasConversation, persistedRounds, liveRounds, generating, pendingRefetch = false, currentRound} = params;

    if (regenerating) {
        // Suppress persistedRounds (still the pre-regen turn) but keep
        // liveRounds so multi-round regens don't visually empty between rounds.
        const out: Round[] = [...liveRounds];
        if (currentRound) {
            out.push(currentRound);
        }
        return out;
    }

    const out: Round[] = [...persistedRounds, ...liveRounds];
    if ((generating || pendingRefetch || !hasConversation || persistedRounds.length === 0) && currentRound) {
        out.push(currentRound);
    }
    return out;
}

/**
 * A round under construction, plus where its text starts within the turn's
 * full concatenated text. The offset is needed to rebase citation indices,
 * which the server reports against the whole message.
 */
interface RoundDraft {
    reasoning: {summary: string; signature: string};
    serverTools: ServerToolUse[];
    text: string;
    textStart: number;
}

function emptyDraft(textStart: number): RoundDraft {
    return {reasoning: {summary: '', signature: ''}, serverTools: [], text: '', textStart};
}

function draftIsEmpty(draft: RoundDraft): boolean {
    return draft.text === '' && draft.serverTools.length === 0 && draft.reasoning.summary === '';
}

/**
 * Split one assistant turn's content blocks into the rounds to render.
 *
 * A round renders as `reasoning → activity → text → tool calls`, so a block
 * whose slot is already filled has to start a new round or it would appear
 * above content that arrived before it. Concretely: provider-executed activity
 * arriving after the round already has text begins a new round, which is what
 * keeps narration written between two sandbox runs below the first and above
 * the second.
 *
 * Client tool calls need no equivalent split — the toolrunner already persists
 * each of their rounds as its own turn — so every tool_use block in a turn
 * belongs to that turn's final round, which is where the turn ended.
 */
function splitTurnIntoRounds(
    turn: Turn,
    resultMap: Map<string, ContentBlock>,
): Round[] {
    const drafts: RoundDraft[] = [];
    let draft = emptyDraft(0);
    let textConsumed = 0;

    const startNewDraft = () => {
        drafts.push(draft);
        draft = emptyDraft(textConsumed);
    };

    for (const block of turn.content) {
        switch (block.type) {
        case BlockTypeThinking: {
            const text = block.text ?? '';
            if (text === '') {
                break;
            }
            if (draft.text !== '' || draft.serverTools.length > 0) {
                startNewDraft();
            }
            draft.reasoning.summary = draft.reasoning.summary === '' ? text : `${draft.reasoning.summary}\n${text}`;
            draft.reasoning.signature = block.signature ?? draft.reasoning.signature;
            break;
        }
        case BlockTypeServerToolUse: {
            if (!block.server_tool) {
                break;
            }

            // Consecutive invocations stay in one round so a burst of searches
            // renders as a single activity group.
            if (draft.text !== '') {
                startNewDraft();
            }
            draft.serverTools.push(block.server_tool);
            break;
        }
        case BlockTypeText: {
            const text = block.text ?? '';
            draft.text += text;
            textConsumed += text.length;
            break;
        }
        default:
            break;
        }
    }
    drafts.push(draft);

    const toolCalls = turn.content.
        filter((b) => b.type === BlockTypeToolUse).
        map((block) => toolUseBlockToToolCall(block, resultMap));

    const kept = drafts.filter((d) => !draftIsEmpty(d));
    if (kept.length === 0) {
        // A round can be tool calls alone (the model called a tool without
        // narrating), and dropping it would lose the approval UI with it.
        if (toolCalls.length === 0) {
            return [];
        }
        kept.push(emptyDraft(0));
    }

    const annotationsByDraft = distributeAnnotations(kept, extractAnnotationsFromTurn(turn));

    return kept.map((d, i) => ({

        // The first round keeps the turn id so single-round turns are
        // unchanged; later ones are suffixed to stay unique as React keys.
        id: i === 0 ? turn.id : `${turn.id}-${i}`,
        text: d.text,
        toolCalls: i === kept.length - 1 ? toolCalls : [],
        reasoning: d.reasoning,
        annotations: annotationsByDraft[i],
        serverTools: d.serverTools,
    }));
}

/**
 * Assign each annotation to the round holding the text it points into, with its
 * indices rebased onto that round's text. Citation offsets are reported against
 * the turn's whole message, but each round renders only its own slice, and
 * citation_processor slices by index — so unrebased offsets would drop markers
 * in the wrong place.
 */
function distributeAnnotations(drafts: RoundDraft[], annotations: Annotation[]): Annotation[][] {
    const out: Annotation[][] = drafts.map(() => []);
    if (annotations.length === 0) {
        return out;
    }

    const textBearing = drafts.
        map((draft, index) => ({draft, index})).
        filter(({draft}) => draft.text !== '');
    if (textBearing.length === 0) {
        return out;
    }

    // Single text run: the offsets already line up, so leave them untouched.
    if (textBearing.length === 1) {
        out[textBearing[0].index] = annotations;
        return out;
    }

    for (const annotation of annotations) {
        const target = textBearing.find(({draft}) =>
            annotation.end_index >= draft.textStart &&
            annotation.end_index <= draft.textStart + draft.text.length,
        ) ?? textBearing[textBearing.length - 1];

        out[target.index].push({
            ...annotation,
            start_index: Math.max(annotation.start_index - target.draft.textStart, 0),
            end_index: Math.max(annotation.end_index - target.draft.textStart, 0),
        });
    }
    return out;
}

export function buildRoundsFromTurns(
    conversation: ConversationResponse,
    postId: string,
): Round[] {
    const turns = collectResponseTurns(conversation, postId);
    if (turns.length === 0) {
        return [];
    }

    const resultMap = buildToolResultMap(conversation);
    const rounds: Round[] = [];
    for (const turn of turns) {
        if (turn.role !== 'assistant') {
            continue;
        }
        rounds.push(...splitTurnIntoRounds(turn, resultMap));
    }
    return rounds;
}
