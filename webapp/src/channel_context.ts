// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect} from 'react';
import {useDispatch, useSelector} from 'react-redux';
import {Dispatch, Store, UnknownAction} from 'redux';

import {Channel} from '@mattermost/types/channels';
import {GlobalState} from '@mattermost/types/store';

import {ChannelContextPosts, getChannelContextPosts} from '@/client';
import {licenseAllows} from '@/license';
import manifest from '@/manifest';
import {createRequestGenerations} from '@/request_generation';
import {
    PERMISSION_MANAGE_PRIVATE_CHANNEL_PROPERTIES,
    PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES,
    userHasChannelPermission,
} from '@/utils/permissions';

export const ChannelContextPostsReceived = manifest.id + '_channel_context_posts_received';
export const ChannelContextPostsCleared = manifest.id + '_channel_context_posts_cleared';

// Per-channel agent-context pins. A missing key means not fetched yet; null
// means the fetch failed and is not retried until the next invalidation.
export type ChannelContextPostsState = {[channelId: string]: ChannelContextPosts | null};

type ChannelContextPostsAction = UnknownAction & {channelId?: string; data?: ChannelContextPosts | null};

export function channelContextPostsReducer(state: ChannelContextPostsState = {}, action: ChannelContextPostsAction): ChannelContextPostsState {
    switch (action.type) {
    case ChannelContextPostsReceived:
        return {...state, [action.channelId as string]: action.data ?? null};
    case ChannelContextPostsCleared:
        return {};
    default:
        return state;
    }
}

export function receivedChannelContextPosts(channelId: string, data: ChannelContextPosts | null): ChannelContextPostsAction {
    return {type: ChannelContextPostsReceived, channelId, data};
}

type PluginStateSlice = {channelContextPosts?: ChannelContextPostsState};

export function channelContextPostsFromState(state: GlobalState, channelId: string): ChannelContextPosts | null | undefined {
    const slice = (state as unknown as Record<string, PluginStateSlice | undefined>)['plugins-' + manifest.id];
    return slice?.channelContextPosts?.[channelId];
}

const inFlight = new Map<string, Promise<void>>();
const generations = createRequestGenerations();

// A mutation's response that a refresh superseded before it landed. It is the
// newest state the server confirmed, so it is shown if that refresh fails.
const supersededMutations = new Map<string, ChannelContextPosts>();

// Token of the refresh that last failed while keeping the old pins, so a
// mutation response arriving after that failure is not lost either.
const failedRefreshes = new Map<string, string>();

function startFetch(dispatch: Dispatch, channelId: string, keepOnFailure: boolean): Promise<void> {
    const token = generations.begin(channelId);
    supersededMutations.delete(channelId);
    failedRefreshes.delete(channelId);
    const apply = (data: ChannelContextPosts | null) => {
        if (generations.isCurrent(channelId, token)) {
            supersededMutations.delete(channelId);
            dispatch(receivedChannelContextPosts(channelId, data));
        }
    };
    const request: Promise<void> = getChannelContextPosts(channelId).
        then(apply).
        catch(() => {
            // A failed refresh keeps the pins already shown, or the newer
            // pins from a mutation it superseded; only a failed first load
            // records the failure.
            if (!keepOnFailure) {
                apply(null);
                return;
            }
            if (!generations.isCurrent(channelId, token)) {
                return;
            }
            const mutated = supersededMutations.get(channelId);
            if (mutated) {
                supersededMutations.delete(channelId);
                dispatch(receivedChannelContextPosts(channelId, mutated));
            } else {
                failedRefreshes.set(channelId, token);
            }
        }).
        finally(() => {
            if (inFlight.get(channelId) === request) {
                inFlight.delete(channelId);
            }
        });
    inFlight.set(channelId, request);
    return request;
}

// Fetches a channel's pins into the store. Concurrent calls for the same
// channel share one request: every visible post asks for its channel at once.
// keepOnFailure preserves the stored pins when the fetch fails.
export function fetchChannelContextPosts(dispatch: Dispatch, channelId: string, keepOnFailure = false): Promise<void> {
    return inFlight.get(channelId) ?? startFetch(dispatch, channelId, keepOnFailure);
}

// Runs a pin or unpin and stores the pins the server returns. If newer work
// for the channel (another mutation, a refresh, or a reset) started meanwhile,
// this response is stale and is dropped: that newer work owns the channel's
// state. The exception is a refresh that fails, which has nothing newer to
// show, so the mutation's response is used instead. A pending GET read an
// older snapshot, so starting the mutation supersedes it too. Rejects when
// the mutation fails.
export async function mutateChannelContextPosts(
    dispatch: Dispatch,
    channelId: string,
    mutation: () => Promise<ChannelContextPosts>,
): Promise<void> {
    const token = generations.begin(channelId);
    inFlight.delete(channelId);
    supersededMutations.delete(channelId);
    failedRefreshes.delete(channelId);
    const data = await mutation();
    const failedRefresh = failedRefreshes.get(channelId);
    if (generations.isCurrent(channelId, token) || (failedRefresh && generations.isCurrent(channelId, failedRefresh))) {
        failedRefreshes.delete(channelId);
        dispatch(receivedChannelContextPosts(channelId, data));
    } else if (inFlight.has(channelId)) {
        supersededMutations.set(channelId, data);
    }
}

// Drops every pending fetch so none can repopulate state cleared by a reconnect.
export function resetChannelContextRequests(): void {
    generations.invalidateAll();
    inFlight.clear();
    supersededMutations.clear();
    failedRefreshes.clear();
}

// Agent context applies to public and private channels on a licensed server.
export function channelContextAvailable(state: GlobalState, channel: Channel | undefined): boolean {
    if (!channel || (channel.type !== 'O' && channel.type !== 'P')) {
        return false;
    }
    return licenseAllows(state, 'channel_context');
}

// Mirrors the server's write check: the channel-management permission for the
// channel's type.
export function canManageChannelContext(state: GlobalState, channel: Channel | undefined): boolean {
    return channelContextAvailable(state, channel) && canManageChannelContextPins(state, channel);
}

// Removing pins is never license-gated server-side, so managers can still
// unpin after a downgrade: only the channel type and permission matter.
export function canManageChannelContextPins(state: GlobalState, channel: Channel | undefined): boolean {
    if (!channel || (channel.type !== 'O' && channel.type !== 'P')) {
        return false;
    }
    const permission = channel.type === 'P' ? PERMISSION_MANAGE_PRIVATE_CHANNEL_PROPERTIES : PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES;
    return userHasChannelPermission(state, channel.team_id, channel.id, permission);
}

// Returns the channel's agent-context pins, fetching them the first time any
// component asks. undefined while loading; null when the fetch failed or
// agent context does not apply to the channel. Managing surfaces pass
// forManagement so managers still see pins that a license downgrade left behind.
export function useChannelContextPosts(channelId: string, forManagement = false): ChannelContextPosts | null | undefined {
    const dispatch = useDispatch();
    const available = useSelector((state: GlobalState) => {
        const channel = state.entities.channels.channels[channelId];
        return channelContextAvailable(state, channel) || (forManagement && canManageChannelContextPins(state, channel));
    });
    const data = useSelector((state: GlobalState) => channelContextPostsFromState(state, channelId));
    const needsFetch = available && typeof data === 'undefined';

    useEffect(() => {
        if (needsFetch) {
            fetchChannelContextPosts(dispatch, channelId);
        }
    }, [needsFetch, channelId, dispatch]);

    return available ? data : null;
}

export function isChannelContextPinned(data: ChannelContextPosts | null | undefined, postId: string): boolean {
    return Boolean(data?.posts.some((p) => p.post_id === postId));
}

// Websocket payload for custom_mattermost-ai_channel_context_updated. Only
// channel_id is trusted; the pins are re-fetched.
export type ChannelContextUpdatedEvent = {channel_id?: string};

// Re-fetches a channel's pins after a remote change, but only when this client
// has loaded or is loading that channel. A fetch already in flight may have
// read the pins before the change, so it is superseded by a fresh one.
export function handleChannelContextUpdated(store: Store<GlobalState, UnknownAction>, event: ChannelContextUpdatedEvent): void {
    const channelId = event.channel_id;
    if (!channelId) {
        return;
    }
    const current = channelContextPostsFromState(store.getState(), channelId);
    if (inFlight.has(channelId) || typeof current !== 'undefined') {
        startFetch(store.dispatch, channelId, Boolean(current));
    }
}
