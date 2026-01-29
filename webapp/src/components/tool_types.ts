// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export enum ToolCallStatus {
    Pending = 0,
    Accepted = 1,
    Rejected = 2,
    Error = 3,
    Success = 4
}

export interface ToolCall {
    id: string;
    name: string;
    description: string;
    arguments?: any;
    result?: string;
    status: ToolCallStatus;
}

export type ToolApprovalStage = 'call' | 'result';
