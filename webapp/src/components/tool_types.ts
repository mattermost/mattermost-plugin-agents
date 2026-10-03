// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export enum ToolCallStatus {
    Pending = 0,
    Accepted = 1,
    Rejected = 2,
    Error = 3,
    Success = 4,
    AutoApproved = 5,
    Waiting = 6,
}

// Built-in deferred-result tool that asks a different Mattermost user a
// question. Mirrors mmtools.AskAnotherUserToolName on the server.
export const AskAnotherUserToolName = 'AskAnotherUser';

export type JSONValue =
    | string
    | number
    | boolean
    | null
    | {[key: string]: JSONValue}
    | JSONValue[];

// UserInteractionSelect marks a tool answered by the user picking from a set
// of options. Mirrors llm.UserInteractionSelect on the server.
export const UserInteractionSelect = 'select';

// ToolAnswer is a user's answer to a user-interaction tool call. Mirrors
// mmtools.UserInteractionAnswer on the server.
export interface ToolAnswer {
    selected: string[];
    custom?: string;
}

// Mirrors llm.ToolUIMeta JSON (llm/tools_ui.go).
export interface ToolUIMeta {
    resource_uri: string;
    visibility?: string[];
}

export interface ToolCall {
    id: string;
    name: string;
    description: string;
    server_origin?: string; // omitempty on the server; present only for MCP tools

    // Display name resolved server-side from MCP metadata; absent for
    // built-in tools and MCP tools without a declared title.
    title?: string;

    // Unprefixed MCP tool name (e.g. "create_post" for
    // "mattermost__create_post"). Redacted for non-requesters.
    mcp_bare_name?: string;

    arguments?: JSONValue;
    result?: string;
    status: ToolCallStatus;

    // MCP Apps metadata from the tool's _meta.ui. Present only when the tool
    // declares an app UI and the viewer may see it (requester, or shared).
    ui_meta?: ToolUIMeta;

    // Non-empty for tools answered by the user instead of executed by the
    // server (e.g. AskUserQuestion). See UserInteractionSelect.
    user_interaction?: string;

    // True for a pending call that passed the auto-execution policy. The call
    // may be running live or paused in a persisted round, but never needs an
    // individual approval decision.
    would_auto_execute?: boolean;

    // True when the tool's result arrives out-of-band after a dispatch side
    // effect (deferred-result tools, e.g. AskAnotherUser).
    deferred_result?: boolean;

    // True when the matching tool result has already received its terminal
    // share/keep-private decision (decided_at set server-side). Derived from
    // the conversation API; absent on live websocket payloads.
    decided?: boolean;
}

// ToolApprovalStage mirrors the server-computed approval state for a post.
// 'done' means no user decision remains (auto-run, keep private, all
// rejected, or no tool_use blocks at all) — render no buttons.
export type ToolApprovalStage = 'call' | 'result' | 'done';
