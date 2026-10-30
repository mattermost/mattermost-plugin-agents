// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useMemo, useSyncExternalStore} from 'react';
import {useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

import type {PostUpdateWebsocketMessage} from '@/components/llmbot_post/llmbot_post';
import type {PluginWebSocketMessage} from '@/types';
import {subscribeToAllPostUpdates} from '@/websocket';
import {isCurrentUsersAgentPost, isRequestedByCurrentUser} from '@/agent_posts';

// The current user's agent posts that are generating right now, from the same
// custom_mattermost-ai_postupdate events LLMBotPost follows: a start,
// continue, progress, reasoning or text chunk means generating; end or
// cancel means done. Recorded for the whole session (subscribed when this
// module loads), so a composer that mounts mid-stream, such as the thread
// view a new chat turns into, still knows. No post prop marks a generation in
// progress, so a page loaded mid-stream only learns of it from the next chunk.
// Each entry keeps the time of its latest event: an entry whose `end` never
// came (missed while disconnected, or the stream died) is dropped after
// STALE_GENERATING_MS without events, and on a websocket reconnect.
const generating = new Map<string, number>();

// Stopped from the composer: later chunks already in flight must not mark it
// generating again (LLMBotPost's stoppedRef); a new start (regenerate) does.
const stopped = new Map<string, number>();

// Long enough for a slow tool call or reasoning step between chunks.
export const STALE_GENERATING_MS = 10 * 60 * 1000;

// Bounds both maps, oldest first.
export const MAX_TRACKED_POSTS = 100;

const listeners = new Set<() => void>();
let version = 0;

const notify = () => {
    version++;
    Array.from(listeners).forEach((listener) => listener());
};

// Where the store comes from (index.tsx sets it). With it, only the current user's agent posts are
// recorded; without it (tests that drive the module directly) every post is, and the readers below
// still filter.
let getState: (() => GlobalState) | null = null;
export const setGeneratingStateSource = (source: (() => GlobalState) | null) => {
    getState = source;
};

const isRelevant = (postId: string) => !getState || isCurrentUsersAgentPost(getState(), postId);

const track = (map: Map<string, number>, postId: string, now: number) => {
    map.delete(postId);
    map.set(postId, now);
    while (map.size > MAX_TRACKED_POSTS) {
        const oldest = map.keys().next().value as string;
        map.delete(oldest);
    }
};

// Drops entries no event has touched for STALE_GENERATING_MS. Returns whether any went.
const pruneStale = (now: number) => {
    let changed = false;
    for (const [postId, seen] of generating) {
        if (now - seen > STALE_GENERATING_MS) {
            generating.delete(postId);
            changed = true;
        }
    }
    for (const [postId, seen] of stopped) {
        if (now - seen > STALE_GENERATING_MS) {
            stopped.delete(postId);
        }
    }
    return changed;
};

const LIVE_CONTROLS = new Set(['start', 'continue', 'progress', 'reasoning_summary']);
const RESTART_CONTROLS = new Set(['start', 'continue']);
const DONE_CONTROLS = new Set(['end', 'cancel']);

export const recordPostUpdate = (msg: PluginWebSocketMessage<PostUpdateWebsocketMessage>) => {
    const postId = msg.data?.post_id;
    if (!postId) {
        return;
    }
    const now = Date.now();
    let changed = pruneStale(now);
    const control = msg.data.control ?? '';
    if (DONE_CONTROLS.has(control)) {
        stopped.delete(postId);
        changed = generating.delete(postId) || changed;
    } else {
        if (RESTART_CONTROLS.has(control)) {
            stopped.delete(postId);
        }
        const live = LIVE_CONTROLS.has(control) || typeof msg.data.next === 'string';
        if (live && !stopped.has(postId) && isRelevant(postId)) {
            changed = changed || !generating.has(postId);
            track(generating, postId, now);
        }
    }
    if (changed) {
        notify();
    }
};

subscribeToAllPostUpdates(recordPostUpdate);

// The composer stopped this post's generation (the server confirmed).
export const markGenerationStopped = (postId: string) => {
    track(stopped, postId, Date.now());
    if (generating.delete(postId)) {
        notify();
    }
};

// Forget every recorded generation: on a websocket reconnect (the `end` of a
// stream may have been missed; a stream still running is recorded again by its
// next chunk), and in tests.
export const clearGeneratingPosts = () => {
    const had = generating.size > 0;
    generating.clear();
    stopped.clear();
    if (had) {
        notify();
    }
};

// Tests only.
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
// (as LLMBotPost shows its Stop to the requester).
const isRequester = (state: GlobalState, post: PostLike) => isRequestedByCurrentUser(state, post);

const findGenerating = (state: GlobalState, channelId: string, rootId: string): string | null => {
    if (!channelId || !rootId) {
        return null;
    }
    const posts = (state.entities.posts?.posts ?? {}) as Record<string, PostLike | undefined>;
    for (const postId of generating.keys()) {
        const post = posts[postId];
        if (post && post.channel_id === channelId && (post.root_id === rootId || post.id === rootId) && isRequester(state, post)) {
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

// The sorted, comma-joined root post ids of the current user's chats among the recorded posts `ids`
// (`exclude` left out). One string, so an unchanged set does not re-render on every state change.
// Only posts already in the store count: the host receives a reply's post when the agent starts it,
// in any channel the user belongs to.
const rootsKey = (state: GlobalState, ids: ReadonlyMap<string, number>, exclude = ''): string => {
    if (ids.size === 0) {
        return '';
    }
    const posts = (state.entities?.posts?.posts ?? {}) as Record<string, PostLike | undefined>;
    const roots = new Set<string>();
    ids.forEach((_seen, postId) => {
        const post = posts[postId];
        if (post && isRequester(state, post)) {
            const root = post.root_id || post.id;
            if (root !== exclude) {
                roots.add(root);
            }
        }
    });
    return Array.from(roots).sort().join(',');
};

const useRoots = (ids: ReadonlyMap<string, number>, exclude = ''): ReadonlySet<string> => {
    // Re-render on every recorded change; the selector reads the set.
    const recorded = useSyncExternalStore(subscribe, getVersion);
    const key = useSelector<GlobalState, string>((state) => (recorded < 0 ? '' : rootsKey(state, ids, exclude)));
    return useMemo(() => new Set(key ? key.split(',') : []), [key]);
};

// The root post ids of the current user's chats with an agent generating right now, across every
// channel (a chat they are not looking at included). A post leaves the set when its `end` or
// `cancel` event arrives.
export const useGeneratingThreadRoots = (): ReadonlySet<string> => useRoots(generating);
