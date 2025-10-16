// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {WebSocketMessage} from '@mattermost/client';

export interface PostUpdateWebsocketMessage {
    post_id: string
    next?: string
    control?: string
    tool_call?: string
    reasoning?: string
}

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
    arguments: any;
    result?: string;
    status: ToolCallStatus;
}

export interface LLMBotPostProps {
    post: any;
    websocketRegister?: (postID: string, listenerID: string, handler: (msg: WebSocketMessage<any>) => void) => void;
    websocketUnregister?: (postID: string, listenerID: string) => void;
}

export type PermalinkData = {
    channel_display_name: string
    channel_id: string
    post_id: string
    team_name: string
    post: {
        message: string
        user_id: string
    }
}

