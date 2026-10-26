// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {PostUpdateWebsocketMessage} from './components/llmbot_post/llmbot_post';
import {PluginWebSocketMessage} from './types';
import PostEventListener, {onAgentThreadsRefreshRequested, requestAgentThreadsRefresh, subscribeToAllPostUpdates} from './websocket';

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
        events.handlePostUpdateWebsockets(message({
            post_id: 'post-id',
            control: 'start',
        }));

        events.registerPostUpdateListener('post-id', 'listener-id', listener);

        expect(listener).toHaveBeenCalledTimes(2);
        expect(listener.mock.calls[0][0].data.control).toBe('start');
        expect(listener.mock.calls[1][0].data.progress_phase).toBe('preparing_request');
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
        events.handlePostUpdateWebsockets(message({
            post_id: 'post-id',
            control: 'progress',
            progress_phase: 'connecting_provider',
            progress_seq: 4,
        }));
        events.handlePostUpdateWebsockets(message({
            post_id: 'post-id',
            control: 'start',
        }));
        events.registerPostUpdateListener('post-id', 'listener-id', listener);

        expect(listener).not.toHaveBeenCalled();
    });
});

describe('subscribeToAllPostUpdates', () => {
    const msg = (postID: string, control?: string) => ({data: {post_id: postID, control}} as PluginWebSocketMessage<PostUpdateWebsocketMessage>);

    test('notifies subscribers for every post, after per-post listeners, until unsubscribed', () => {
        const events = new PostEventListener();
        const order: string[] = [];
        events.registerPostUpdateListener('p1', 'l1', () => order.push('post'));
        const all = jest.fn(() => order.push('all'));
        const unsubscribe = subscribeToAllPostUpdates(all);

        events.handlePostUpdateWebsockets(msg('p1', 'end'));
        events.handlePostUpdateWebsockets(msg('p2', 'end'));
        expect(all).toHaveBeenCalledTimes(2);
        expect(order).toEqual(['post', 'all', 'all']);

        unsubscribe();
        events.handlePostUpdateWebsockets(msg('p1', 'end'));
        expect(all).toHaveBeenCalledTimes(2);
    });

    test('subscribers see events handled by a different instance', () => {
        const all = jest.fn();
        const unsubscribe = subscribeToAllPostUpdates(all);
        new PostEventListener().handlePostUpdateWebsockets(msg('p9', 'end'));
        unsubscribe();
        expect(all).toHaveBeenCalledTimes(1);
    });

    test('per-post delivery is unchanged', () => {
        const events = new PostEventListener();
        const listener = jest.fn();
        events.registerPostUpdateListener('p1', 'l1', listener);
        events.handlePostUpdateWebsockets(msg('p1', 'end'));
        events.handlePostUpdateWebsockets(msg('p2', 'end'));
        expect(listener).toHaveBeenCalledTimes(1);
    });
});

describe('agent threads refresh requests', () => {
    test('notifies subscribers until unsubscribed', () => {
        const cb = jest.fn();
        const unsubscribe = onAgentThreadsRefreshRequested(cb);
        requestAgentThreadsRefresh();
        unsubscribe();
        requestAgentThreadsRefresh();
        expect(cb).toHaveBeenCalledTimes(1);
    });
});
