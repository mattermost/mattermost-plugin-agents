// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {PostUpdateWebsocketMessage} from './components/llmbot_post/llmbot_post';
import {PluginWebSocketMessage} from './types';
import PostEventListener from './websocket';

function message(data: PostUpdateWebsocketMessage): PluginWebSocketMessage<PostUpdateWebsocketMessage> {
    return {data} as PluginWebSocketMessage<PostUpdateWebsocketMessage>;
}

describe('PostEventListener progress buffering', () => {
    test('replays only the latest monotonic phase registered before the post renders', () => {
        const events = new PostEventListener();
        const listener = jest.fn();

        events.handlePostUpdateWebsockets(message({
            post_id: 'post-id',
            control: 'progress',
            progress_phase: 'checking_mcp',
            progress_seq: 1,
        }));
        events.handlePostUpdateWebsockets(message({
            post_id: 'post-id',
            control: 'progress',
            progress_phase: 'preparing_request',
            progress_seq: 3,
        }));
        events.handlePostUpdateWebsockets(message({
            post_id: 'post-id',
            control: 'progress',
            progress_phase: 'loading_conversation',
            progress_seq: 2,
        }));

        events.registerPostUpdateListener('post-id', 'listener-id', listener);

        expect(listener).toHaveBeenCalledTimes(1);
        expect(listener.mock.calls[0][0].data.progress_phase).toBe('preparing_request');
    });

    test('does not replay progress after substantive stream content arrives', () => {
        const events = new PostEventListener();
        const listener = jest.fn();

        events.handlePostUpdateWebsockets(message({
            post_id: 'post-id',
            control: 'progress',
            progress_phase: 'connecting_provider',
            progress_seq: 4,
        }));
        events.handlePostUpdateWebsockets(message({
            post_id: 'post-id',
            next: 'Hello',
        }));
        events.registerPostUpdateListener('post-id', 'listener-id', listener);

        expect(listener).not.toHaveBeenCalled();
    });
});
