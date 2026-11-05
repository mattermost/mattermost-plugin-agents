// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {BaseWebSocketMessage} from '@mattermost/client';

// The client's WebSocketMessage union only covers core server events, so plugin
// events are described with the open-ended base shape instead.
export type PluginWebSocketMessage<T> = BaseWebSocketMessage<string, T>;

export interface CustomPrompt {
    id: string;
    creator_id: string;
    name: string;
    description: string;
    template: string;
    is_shared: boolean;
    created_at: number;
    updated_at: number;
    deleted_at: number;
}
