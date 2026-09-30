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

    // When true, selecting the prompt posts the rendered template instead of
    // inserting it into the draft for the user to review and send.
    run_immediately: boolean;
    created_at: number;
    updated_at: number;
    deleted_at: number;
}

/** The writable subset of a prompt, as accepted by create and update. */
export type CustomPromptInput = Pick<CustomPrompt, 'name' | 'description' | 'template' | 'is_shared' | 'run_immediately'>;
