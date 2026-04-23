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

export interface ToolCall {
    id: string;
    name: string;
    description: string;
    arguments?: JSONValue;
    result?: string;
    status: ToolCallStatus;
}

// ToolApprovalStage mirrors the server-computed approval state for a post.
// 'done' means no user decision remains (auto-run, keep private, all
// rejected, or no tool_use blocks at all) — render no buttons.
export type ToolApprovalStage = 'call' | 'result' | 'done';
