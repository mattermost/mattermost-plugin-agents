// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useMemo, useSyncExternalStore} from 'react';
import {useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

import type {PostUpdateWebsocketMessage} from '@/components/llmbot_post/llmbot_post';
import type {PluginWebSocketMessage} from '@/types';
import {subscribeToAllPostUpdates} from '@/websocket';

// Agent posts that are generating right now, from the same
// custom_mattermost-ai_postupdate events LLMBotPost follows: a start,
// continue, progress, reasoning or text chunk means generating; end or
// cancel means done. Recorded for the whole session (subscribed when this
// module loads), so a composer that mounts mid-stream, such as the thread
// view a new chat turns into, still knows. No post prop marks a generation in
// progress, so a page loaded mid-stream only learns of it from the next event.
const generating = new Set<string>();

// Stopped from the composer: later chunks already in flight must not mark it
// generating again (LLMBotPost's stoppedRef); a new start (regenerate) does.
const stopped = new Set<string>();

const listeners = new Set<() => void>();
let version = 0;

const notify = () => {
    version++;
    Array.from(listeners).forEach((listener) => listener());
};

const LIVE_CONTROLS = new Set(['start', 'continue', 'progress', 'reasoning_summary']);
const RESTART_CONTROLS = new Set(['start', 'continue']);
const DONE_CONTROLS = new Set(['end', 'cancel']);

export const recordPostUpdate = (msg: PluginWebSocketMessage<PostUpdateWebsocketMessage>) => {
    const postId = msg.data?.post_id;
    if (!postId) {
        return;
    }
    const control = msg.data.control ?? '';
    if (DONE_CONTROLS.has(control)) {
        stopped.delete(postId);
        if (generating.delete(postId)) {
            notify();
        }
        return;
    }
    if (RESTART_CONTROLS.has(control)) {
        stopped.delete(postId);
    }
    const live = LIVE_CONTROLS.has(control) || typeof msg.data.next === 'string';
    if (live && !stopped.has(postId) && !generating.has(postId)) {
        generating.add(postId);
        notify();
    }
};

subscribeToAllPostUpdates(recordPostUpdate);

// The composer stopped this post's generation (the server confirmed).
export const markGenerationStopped = (postId: string) => {
    stopped.add(postId);
    if (generating.delete(postId)) {
        notify();
    }
};

// Tests only: forget every recorded generation.
export const resetGeneratingPosts = () => {
    generating.clear();
    stopped.clear();
    notify();
};

const subscribe = (listener: () => void) => {
    listeners.add(listener);
    return () => {
        listeners.delete(listener);
    };
};

const getVersion = () => version;

interface PostLike {
    id: string;
    channel_id: string;
    root_id?: string;
    user_id: string;
    props?: Record<string, unknown>;
}

// The requester's own generations only: the stop endpoint is the requester's
// (as LLMBotPost shows its Stop to the requester). llm_requester_user_id is
// set on every agent post with a requester; the post it responds to is the
// fallback.
const isRequester = (state: GlobalState, post: PostLike, currentUserId: string) => {
    if (post.props?.llm_requester_user_id) {
        return post.props.llm_requester_user_id === currentUserId;
    }
    const respondingTo = post.props?.responding_to;
    const posts = state.entities.posts.posts as Record<string, PostLike | undefined>;
    return typeof respondingTo === 'string' && posts[respondingTo]?.user_id === currentUserId;
};

const findGenerating = (state: GlobalState, channelId: string, rootId: string): string | null => {
    if (!channelId || !rootId) {
        return null;
    }
    const posts = (state.entities.posts?.posts ?? {}) as Record<string, PostLike | undefined>;
    const currentUserId = state.entities.users.currentUserId;
    for (const postId of generating) {
        const post = posts[postId];
        if (post && post.channel_id === channelId && (post.root_id === rootId || post.id === rootId) && isRequester(state, post, currentUserId)) {
            return postId;
        }
    }
    return null;
};

// The id of the current user's agent post generating in this thread, or null.
// Works for any channel (the agent's DM or a channel thread it was mentioned
// in); a new chat (no root yet) has none.
export const useThreadGenerating = (channelId: string, rootId: string): string | null => {
    // Re-render on every recorded change; the selector below reads the set.
    const recorded = useSyncExternalStore(subscribe, getVersion);
    return useSelector<GlobalState, string | null>((state) => (recorded >= 0 ? findGenerating(state, channelId, rootId) : null));
};

// The root post ids of the current user's chats with an agent generating right now, across every
// channel (a chat they are not looking at included). A post leaves the set when its `end` or
// `cancel` event arrives. Only posts already in the store count: the host receives a reply's
// post when the agent starts it, in any channel the user belongs to.
export const useGeneratingThreadRoots = (): ReadonlySet<string> => {
    const recorded = useSyncExternalStore(subscribe, getVersion);

    // One string, so an unchanged set does not re-render on every state change.
    const key = useSelector<GlobalState, string>((state) => {
        if (recorded < 0 || generating.size === 0) {
            return '';
        }
        const posts = (state.entities?.posts?.posts ?? {}) as Record<string, PostLike | undefined>;
        const currentUserId = state.entities?.users?.currentUserId ?? '';
        const roots = new Set<string>();
        generating.forEach((postId) => {
            const post = posts[postId];
            if (post && isRequester(state, post, currentUserId)) {
                roots.add(post.root_id || post.id);
            }
        });
        return Array.from(roots).sort().join(',');
    });

    return useMemo(() => new Set(key ? key.split(',') : []), [key]);
};
