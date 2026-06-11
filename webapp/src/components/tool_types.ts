// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export enum ToolCallStatus {
    Pending = 0,
    Accepted = 1,
    Rejected = 2,
    Error = 3,
    Success = 4,
    AutoApproved = 5,
}

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

export interface ToolCall {
    id: string;
    name: string;
    description: string;
    server_origin?: string; // omitempty on the server; present only for MCP tools
    arguments?: JSONValue;
    result?: string;
    status: ToolCallStatus;

    // Non-empty for tools answered by the user instead of executed by the
    // server (e.g. AskUserQuestion). See UserInteractionSelect.
    user_interaction?: string;
}

// ToolApprovalStage mirrors the server-computed approval state for a post.
// 'done' means no user decision remains (auto-run, keep private, all
// rejected, or no tool_use blocks at all) — render no buttons.
export type ToolApprovalStage = 'call' | 'result' | 'done';
