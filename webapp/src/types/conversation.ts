// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Block type discriminator values -- must match Go constants in conversation/content_block.go
export const BlockTypeText = 'text' as const;
export const BlockTypeThinking = 'thinking' as const;
export const BlockTypeToolUse = 'tool_use' as const;
export const BlockTypeToolResult = 'tool_result' as const;
export const BlockTypeFile = 'file' as const;
export const BlockTypeImage = 'image' as const;
export const BlockTypeAnnotations = 'annotations' as const;

export type BlockType =
    | typeof BlockTypeText
    | typeof BlockTypeThinking
    | typeof BlockTypeToolUse
    | typeof BlockTypeToolResult
    | typeof BlockTypeFile
    | typeof BlockTypeImage
    | typeof BlockTypeAnnotations;

// Tool call status constants -- must match Go constants in conversation/content_block.go
export const StatusPending = 'pending' as const;
export const StatusAccepted = 'accepted' as const;
export const StatusRejected = 'rejected' as const;
export const StatusError = 'error' as const;
export const StatusSuccess = 'success' as const;
export const StatusAutoApproved = 'auto_approved' as const;

export type ToolCallStatus =
    | typeof StatusPending
    | typeof StatusAccepted
    | typeof StatusRejected
    | typeof StatusError
    | typeof StatusSuccess
    | typeof StatusAutoApproved;

export interface Citation {
    type: string;
    url?: string;
    title?: string;
    start_index: number;
    end_index: number;
}

export interface WebSearchContext {
    results: unknown;
    executed_queries: unknown;
    count: number;
}

export interface ContentBlock {
    type: string;

    // Text / Thinking fields
    text?: string;
    signature?: string;
    citations?: Citation[];

    // ToolUse fields
    id?: string;
    name?: string;
    server_origin?: string;
    input?: Record<string, unknown> | null;
    status?: ToolCallStatus;
    shared?: boolean;

    // ToolResult fields
    tool_use_id?: string;
    content?: string;

    // Timestamp (ms) at which the share/keep-private decision was recorded.
    // nil → decision still pending; non-nil → decision made, no approval UI.
    decided_at?: number;

    // File / Image fields
    filename?: string;
    mime_type?: string;
    file_id?: string;

    // Annotations fields
    web_search_context?: WebSearchContext;
}

export interface Turn {
    id: string;
    conversation_id?: string;
    post_id: string | null;
    role: 'user' | 'assistant' | 'tool_result';
    content: ContentBlock[];
    tokens_in: number;
    tokens_out: number;
    sequence: number;
    created_at?: number;

    // Set only on post-anchor assistant turns. Server-computed from the
    // conversation state: 'call' → pending Accept/Reject; 'result' → pending
    // Share/Keep private; 'done' → no user decision remains.
    approval_state?: 'call' | 'result' | 'done';
}

export interface ConversationResponse {
    id: string;
    user_id: string;
    bot_id: string;
    channel_id: string | null;
    root_post_id: string | null;
    title: string;
    operation: string;
    turns: Turn[];
}
