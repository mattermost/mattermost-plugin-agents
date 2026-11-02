// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useMemo, useSyncExternalStore} from 'react';
import {useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

import type {PostUpdateWebsocketMessage} from '@/components/llmbot_post/llmbot_post';
import type {PluginWebSocketMessage} from '@/types';
import {isCurrentUsersAgentPost, isRequestedByCurrentUser} from '@/agent_posts';

// The current user's agent posts that are generating right now, from the same
// custom_mattermost-ai_postupdate events LLMBotPost follows: a start,
// continue, progress, reasoning or text chunk means generating; end or
// cancel means done. Recorded for the whole session, so a view that mounts
// mid-stream still knows. No post prop marks a generation in progress, so a
// page loaded mid-stream only learns of it from the next chunk.
// Each entry keeps the time of its latest event: an entry whose `end` never
// came (missed while disconnected, or the stream died) is dropped after
// STALE_GENERATING_MS without events, and on a websocket reconnect.
const generating = new Map<string, number>();

// Long enough for a slow tool call or reasoning step between chunks.
export const STALE_GENERATING_MS = 10 * 60 * 1000;

export const MAX_TRACKED_POSTS = 100;

const listeners = new Set<() => void>();
let version = 0;

const notify = () => {
    version++;
    Array.from(listeners).forEach((listener) => listener());
};

const pruneStale = (now: number) => {
    let changed = false;
    for (const [postId, seen] of generating) {
        if (now - seen > STALE_GENERATING_MS) {
            generating.delete(postId);
            changed = true;
        }
    }
    return changed;
};

const LIVE_CONTROLS = new Set(['start', 'continue', 'progress', 'reasoning_summary']);
const DONE_CONTROLS = new Set(['end', 'cancel']);

// Only the current user's agent posts are recorded, so a busy channel cannot crowd theirs out.
export const recordPostUpdate = (state: GlobalState, msg: PluginWebSocketMessage<PostUpdateWebsocketMessage>) => {
    const postId = msg.data?.post_id;
    if (!postId) {
        return;
    }
    const now = Date.now();
    let changed = pruneStale(now);
    const control = msg.data.control ?? '';
    if (DONE_CONTROLS.has(control)) {
        changed = generating.delete(postId) || changed;
    } else if ((LIVE_CONTROLS.has(control) || typeof msg.data.next === 'string') && isCurrentUsersAgentPost(state, postId)) {
        changed = changed || !generating.has(postId);
        generating.delete(postId);
        generating.set(postId, now);
        if (generating.size > MAX_TRACKED_POSTS) {
            generating.delete(generating.keys().next().value as string);
        }
    }
    if (changed) {
        notify();
    }
};

// Forget every recorded generation: on a websocket reconnect the `end` of a stream may have been
// missed; a stream still running is recorded again by its next chunk.
export const clearGeneratingPosts = () => {
    if (generating.size > 0) {
        generating.clear();
        notify();
    }
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

// The sorted, comma-joined root post ids of the requester's own generating posts. One string, so
// an unchanged set does not re-render on every state change. The requester's only: the stop
// endpoint is theirs, as LLMBotPost shows its Stop to the requester.
const rootsKey = (state: GlobalState): string => {
    if (generating.size === 0) {
        return '';
    }
    const posts = (state.entities?.posts?.posts ?? {}) as Record<string, PostLike | undefined>;
    const roots = new Set<string>();
    generating.forEach((_seen, postId) => {
        const post = posts[postId];
        if (post && isRequestedByCurrentUser(state, post)) {
            roots.add(post.root_id || post.id);
        }
    });
    return Array.from(roots).sort().join(',');
};

// The root post ids of the current user's chats with an agent generating right now, across every
// channel. A post leaves the set when its `end` or `cancel` event arrives.
export const useGeneratingThreadRoots = (): ReadonlySet<string> => {
    // Re-renders on every recorded change; the new inline selector then reads the changed map.
    useSyncExternalStore(subscribe, getVersion);
    const key = useSelector<GlobalState, string>((state) => rootsKey(state));
    return useMemo(() => new Set(key ? key.split(',') : []), [key]);
};

// Whether the current user's agent is generating in the thread rooted at rootId. A new chat (no
// root yet) never is.
export const useIsThreadGenerating = (rootId: string): boolean => {
    const roots = useGeneratingThreadRoots();
    return Boolean(rootId) && roots.has(rootId);
};
