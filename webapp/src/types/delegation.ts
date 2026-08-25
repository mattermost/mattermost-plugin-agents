// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Delegation phases mirror the server delegation package constants. The
// webapp additionally derives 'awaiting_approval' from a pending tool call.
export type DelegationPhase =
    | 'awaiting_approval'
    | 'starting'
    | 'running'
    | 'waiting_on_you'
    | 'completed'
    | 'failed'
    | 'timed_out';

// Payload of custom_mattermost-ai_delegation_update websocket events.
export type DelegationUpdate = {
    delegation_id: string;
    parent_tool_call_id: string;
    phase: DelegationPhase;
    activity?: 'using_tools' | 'writing';
    tools?: string;
    task_post_id?: string;
    permalink?: string;
    target_agent_id?: string;
    target_agent_username?: string;
    target_agent_displayname?: string;
}

// Response of GET /delegations/{parenttoolcallid}.
export type DelegationStatus = {
    delegation_id: string;
    parent_tool_call_id: string;
    phase: DelegationPhase;
    task_post_id: string;
    permalink: string;
    target_agent_id: string;
    target_agent_username: string;
    target_agent_displayname: string;
    created_at: number;
    answer_preview?: string;
}
