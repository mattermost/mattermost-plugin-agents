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

// Fetches a channel's pins into the store. Concurrent calls for the same
// channel share one request: every visible post asks for its channel at once.
export function fetchChannelContextPosts(dispatch: Dispatch, channelId: string): Promise<void> {
    const pending = inFlight.get(channelId);
    if (pending) {
        return pending;
    }
    const request = getChannelContextPosts(channelId).
        then((data) => {
            dispatch(receivedChannelContextPosts(channelId, data));
        }).
        catch(() => {
            dispatch(receivedChannelContextPosts(channelId, null));
        }).
        finally(() => {
            inFlight.delete(channelId);
        });
    inFlight.set(channelId, request);
    return request;
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
    if (!channel || !channelContextAvailable(state, channel)) {
        return false;
    }
    const permission = channel.type === 'P' ? PERMISSION_MANAGE_PRIVATE_CHANNEL_PROPERTIES : PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES;
    return userHasChannelPermission(state, channel.team_id, channel.id, permission);
}

// Returns the channel's agent-context pins, fetching them the first time any
// component asks. undefined while loading; null when the fetch failed or
// agent context does not apply to the channel.
export function useChannelContextPosts(channelId: string): ChannelContextPosts | null | undefined {
    const dispatch = useDispatch();
    const available = useSelector((state: GlobalState) => channelContextAvailable(state, state.entities.channels.channels[channelId]));
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
// has already loaded that channel.
export function handleChannelContextUpdated(store: Store<GlobalState, UnknownAction>, event: ChannelContextUpdatedEvent): void {
    if (!event.channel_id || typeof channelContextPostsFromState(store.getState(), event.channel_id) === 'undefined') {
        return;
    }
    fetchChannelContextPosts(store.dispatch, event.channel_id);
}
