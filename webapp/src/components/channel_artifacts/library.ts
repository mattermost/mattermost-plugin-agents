// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useRef} from 'react';
import {useDispatch, useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

import {ChannelArtifactEntry, listChannelArtifacts} from '@/client';
import {getChannelArtifactsEnabled, getChannelLibrary} from '@/selectors';

type LibraryDispatch = (action: Record<string, unknown>) => unknown;

const inflight = new Map<string, Promise<void>>();

/**
 * Loads a channel's library into the shared cache. Concurrent callers for the
 * same channel share one request, so a thread full of artifact cards, the
 * panel, and the count badge cost one list call.
 */
export function loadChannelLibrary(dispatch: LibraryDispatch, channelId: string): Promise<void> {
    const pending = inflight.get(channelId);
    if (pending) {
        return pending;
    }
    dispatch({type: 'CHANNEL_ARTIFACTS_LOADING', channelId});
    const request = listChannelArtifacts(channelId).then(
        (list) => {
            inflight.delete(channelId);
            dispatch({type: 'CHANNEL_ARTIFACTS_LOADED', channelId, list});
        },
        () => {
            inflight.delete(channelId);
            dispatch({type: 'CHANNEL_ARTIFACTS_FAILED', channelId});
        },
    );
    inflight.set(channelId, request);
    return request;
}

export type ChannelLibrary = {
    status: 'loading' | 'ready' | 'error';
    entries: ChannelArtifactEntry[];
    canAdd: boolean;
    canEdit: boolean;
    canDelete: boolean;
    maxPerChannel: number;
    reload: () => Promise<void>;
};

/**
 * The cached library of a channel. The first consumer to ask loads it; a failed
 * load is retried by the next consumer that mounts or calls reload. Websocket
 * events and local changes keep it current afterwards.
 */
export function useChannelLibrary(channelId: string): ChannelLibrary {
    const dispatch = useDispatch();
    const enabled = useSelector(getChannelArtifactsEnabled);
    const cached = useSelector((state: GlobalState) => getChannelLibrary(state as never, channelId));
    const status = cached?.status;

    // The channel this consumer has already asked the server for. A failed load
    // changes the status again, so without this a server that keeps failing
    // would be asked in a loop; each consumer retries a failure once.
    const requestedFor = useRef('');

    useEffect(() => {
        if (!enabled || !channelId) {
            requestedFor.current = '';
            return;
        }
        if (status === 'loading' || status === 'ready') {
            return;
        }
        if (status === 'error' && requestedFor.current === channelId) {
            return;
        }
        requestedFor.current = channelId;
        loadChannelLibrary(dispatch as LibraryDispatch, channelId);
    }, [enabled, channelId, status, dispatch]);

    const reload = useCallback(() => {
        if (!channelId) {
            return Promise.resolve();
        }
        return loadChannelLibrary(dispatch as LibraryDispatch, channelId);
    }, [dispatch, channelId]);

    return {
        status: cached?.status ?? 'loading',
        entries: cached?.entries ?? NO_ENTRIES,
        canAdd: Boolean(cached?.canAdd),
        canEdit: Boolean(cached?.canEdit),
        canDelete: Boolean(cached?.canDelete),
        maxPerChannel: cached?.maxPerChannel ?? 0,
        reload,
    };
}

const NO_ENTRIES: ChannelArtifactEntry[] = [];

/** Applies a library websocket event to the cache. */
export function applyLibraryEvent(dispatch: LibraryDispatch, event: string, data: Record<string, unknown> | undefined) {
    if (!data) {
        return;
    }
    const channelId = typeof data.channel_id === 'string' ? data.channel_id : '';
    if (event === 'deleted') {
        if (channelId && typeof data.id === 'string') {
            dispatch({type: 'CHANNEL_ARTIFACT_REMOVE', channelId, id: data.id});
        }
        return;
    }
    const entry = data.artifact as ChannelArtifactEntry | undefined;
    if (entry && typeof entry.id === 'string' && typeof entry.channel_id === 'string') {
        dispatch({type: 'CHANNEL_ARTIFACT_UPSERT', entry});
    }
}
