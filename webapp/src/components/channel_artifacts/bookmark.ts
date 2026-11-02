// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {ChannelBookmark} from '@mattermost/types/channel_bookmarks';
import type {Channel} from '@mattermost/types/channels';
import type {GlobalState} from '@mattermost/types/store';
import type {Dispatch} from 'redux';

import {Client4} from '@/client';
import {userHasChannelPermission} from '@/utils/permissions';

export const LIBRARY_ARTIFACT_QUERY = 'mm_ai_library_artifact';

const ADD_BOOKMARK_PUBLIC = 'add_bookmark_public_channel';
const ADD_BOOKMARK_PRIVATE = 'add_bookmark_private_channel';
const DELETE_BOOKMARK_PUBLIC = 'delete_bookmark_public_channel';
const DELETE_BOOKMARK_PRIVATE = 'delete_bookmark_private_channel';

// Mattermost redux action types (keyMirror values). Creating via Client4 with
// Connection-Id suppresses the websocket echo to this tab, so we must apply
// the same store updates the webapp's createBookmark action would.
const RECEIVED_BOOKMARK = 'RECEIVED_BOOKMARK';
const BOOKMARK_DELETED = 'BOOKMARK_DELETED';

/** True when the server license enables channel bookmarks. */
export function areChannelBookmarksLicensed(state: GlobalState): boolean {
    return state.entities.general?.license?.IsLicensed === 'true';
}

function isGuest(state: GlobalState): boolean {
    const roles = state.entities.users.profiles[state.entities.users.currentUserId]?.roles || '';
    return roles.split(/\s+/).includes('system_guest');
}

/**
 * Whether the current user may add a link bookmark in this channel
 * (licensed + add_bookmark_* / non-guest DM·GM membership).
 */
export function canAddChannelBookmarks(state: GlobalState, channelId: string): boolean {
    if (!areChannelBookmarksLicensed(state) || !channelId) {
        return false;
    }
    const channel = state.entities.channels?.channels?.[channelId] as Channel | undefined;
    if (!channel || channel.delete_at !== 0) {
        return false;
    }
    if (channel.type === 'D' || channel.type === 'G') {
        if (isGuest(state)) {
            return false;
        }
        return state.entities.channels.myMembers?.[channelId]?.channel_id === channelId;
    }
    if (channel.type === 'O') {
        return userHasChannelPermission(state, channel.team_id, channelId, ADD_BOOKMARK_PUBLIC);
    }
    if (channel.type === 'P') {
        return userHasChannelPermission(state, channel.team_id, channelId, ADD_BOOKMARK_PRIVATE);
    }
    return false;
}

export function canDeleteChannelBookmarks(state: GlobalState, channelId: string): boolean {
    if (!areChannelBookmarksLicensed(state) || !channelId) {
        return false;
    }
    const channel = state.entities.channels?.channels?.[channelId] as Channel | undefined;
    if (!channel || channel.delete_at !== 0) {
        return false;
    }
    if (channel.type === 'D' || channel.type === 'G') {
        if (isGuest(state)) {
            return false;
        }
        return state.entities.channels.myMembers?.[channelId]?.channel_id === channelId;
    }
    if (channel.type === 'O') {
        return userHasChannelPermission(state, channel.team_id, channelId, DELETE_BOOKMARK_PUBLIC);
    }
    if (channel.type === 'P') {
        return userHasChannelPermission(state, channel.team_id, channelId, DELETE_BOOKMARK_PRIVATE);
    }
    return false;
}

/** Absolute channel URL that opens a library artifact via the root deep-link listener. */
export function buildLibraryArtifactBookmarkUrl(
    siteURL: string,
    teamName: string,
    channelName: string,
    artifactId: string,
): string {
    const base = siteURL.replace(/\/+$/, '');
    const url = new URL(`${base}/${encodeURIComponent(teamName)}/channels/${encodeURIComponent(channelName)}`);
    url.searchParams.set(LIBRARY_ARTIFACT_QUERY, artifactId);
    return url.toString();
}

export function artifactIdFromBookmarkUrl(linkUrl?: string): string | null {
    if (!linkUrl) {
        return null;
    }
    try {
        const absolute = linkUrl.includes('://') ? linkUrl : `https://placeholder.local${linkUrl.startsWith('/') ? '' : '/'}${linkUrl}`;
        const id = new URL(absolute).searchParams.get(LIBRARY_ARTIFACT_QUERY);
        return id || null;
    } catch {
        return null;
    }
}

export function findLibraryArtifactBookmark(
    bookmarks: ChannelBookmark[] | Record<string, ChannelBookmark> | null | undefined,
    artifactId: string,
): ChannelBookmark | null {
    if (!bookmarks || !artifactId) {
        return null;
    }
    const list = Array.isArray(bookmarks) ? bookmarks : Object.values(bookmarks);
    return list.find((b) => b.type === 'link' && artifactIdFromBookmarkUrl(b.link_url) === artifactId) || null;
}

function connectionIdFromState(state: GlobalState): string {
    return (state as GlobalState & {websocket?: {connectionId?: string}}).websocket?.connectionId || '';
}

function resolveChannelPath(state: GlobalState, channelId: string): {teamName: string; channelName: string} | null {
    const channel = state.entities.channels?.channels?.[channelId] as Channel | undefined;
    if (!channel?.name) {
        return null;
    }
    const team = state.entities.teams?.teams?.[channel.team_id];
    if (!team?.name) {
        return null;
    }
    return {teamName: team.name, channelName: channel.name};
}

function dispatchReceivedBookmark(dispatch: Dispatch | undefined, bookmark: ChannelBookmark) {
    if (!dispatch || !bookmark?.id || !bookmark.channel_id) {
        return;
    }
    dispatch({type: RECEIVED_BOOKMARK, data: bookmark});
}

function dispatchDeletedBookmark(dispatch: Dispatch | undefined, bookmark: ChannelBookmark) {
    if (!dispatch || !bookmark?.id || !bookmark.channel_id) {
        return;
    }
    dispatch({type: BOOKMARK_DELETED, data: bookmark});
}

/** Creates a link bookmark for a library artifact. No-ops if one already exists. */
export async function pinLibraryArtifactBookmark(
    state: GlobalState,
    channelId: string,
    artifactId: string,
    title: string,
    dispatch?: Dispatch,
): Promise<ChannelBookmark | null> {
    if (!canAddChannelBookmarks(state, channelId)) {
        return null;
    }
    const path = resolveChannelPath(state, channelId);
    if (!path) {
        return null;
    }

    const fromState = state.entities?.channelBookmarks?.byChannelId?.[channelId];
    const existing = fromState ? Object.values(fromState) : await Client4.getChannelBookmarks(channelId).catch(() => []);
    const found = findLibraryArtifactBookmark(existing, artifactId);
    if (found) {
        return found;
    }

    const siteURL = Client4.getUrl() || (window.location.origin + (window.basename || ''));
    const linkUrl = buildLibraryArtifactBookmarkUrl(siteURL, path.teamName, path.channelName, artifactId);
    const displayName = (title.trim() || 'Channel artifact').slice(0, 64);

    const created = await Client4.createChannelBookmark(
        channelId,
        {type: 'link', display_name: displayName, link_url: linkUrl},
        connectionIdFromState(state),
    );
    dispatchReceivedBookmark(dispatch, created);
    return created;
}

export async function unpinLibraryArtifactBookmark(
    state: GlobalState,
    channelId: string,
    artifactId: string,
    dispatch?: Dispatch,
): Promise<boolean> {
    if (!canDeleteChannelBookmarks(state, channelId)) {
        return false;
    }
    const fromState = state.entities?.channelBookmarks?.byChannelId?.[channelId];
    const existing = fromState ? Object.values(fromState) : await Client4.getChannelBookmarks(channelId);
    const found = findLibraryArtifactBookmark(existing, artifactId);
    if (!found) {
        return false;
    }
    await Client4.deleteChannelBookmark(channelId, found.id, connectionIdFromState(state));
    dispatchDeletedBookmark(dispatch, found);
    return true;
}

/** Updates the channel bookmark display name when a library artifact is renamed. */
export async function syncLibraryArtifactBookmarkTitle(
    state: GlobalState,
    channelId: string,
    artifactId: string,
    title: string,
    dispatch?: Dispatch,
): Promise<boolean> {
    const displayName = (title.trim() || 'Channel artifact').slice(0, 64);
    if (!channelId || !artifactId || !displayName) {
        return false;
    }
    try {
        const fromState = state.entities?.channelBookmarks?.byChannelId?.[channelId];
        const existing = fromState ? Object.values(fromState) : await Client4.getChannelBookmarks(channelId);
        const found = findLibraryArtifactBookmark(existing, artifactId);
        if (!found || found.display_name === displayName) {
            return false;
        }
        const result = await Client4.updateChannelBookmark(
            channelId,
            found.id,
            {display_name: displayName},
            connectionIdFromState(state),
        );
        if (result?.updated) {
            dispatchReceivedBookmark(dispatch, result.updated);
        } else {
            // Fallback if the response shape is unexpected: patch locally.
            dispatchReceivedBookmark(dispatch, {...found, display_name: displayName});
        }
        if (result?.deleted) {
            dispatchDeletedBookmark(dispatch, result.deleted);
        }
        return true;
    } catch {
        return false;
    }
}

export function parseLibraryArtifactIdFromSearch(search: string): string | null {
    if (!search) {
        return null;
    }
    const params = new URLSearchParams(search.startsWith('?') ? search.slice(1) : search);
    return params.get(LIBRARY_ARTIFACT_QUERY);
}

export function stripLibraryArtifactQuery(pathname: string, search: string, hash = ''): string {
    const params = new URLSearchParams(search.startsWith('?') ? search.slice(1) : search);
    params.delete(LIBRARY_ARTIFACT_QUERY);
    const next = params.toString();
    return pathname + (next ? `?${next}` : '') + hash;
}
