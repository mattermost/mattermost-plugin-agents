// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useMemo, useRef} from 'react';
import {useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

import {updateRead} from '@/client';

// Read state comes from the host: the chat views mark a thread read as any thread view does, and
// the unread dot is the host's collapsed reply threads state for that thread.

const missingThreadMembershipMessage = 'User thread membership doesn\'t exist';

const isMissingThreadMembershipError = (error: unknown) => {
    const message = error instanceof Error ? error.message : String(error);
    return message.includes(missingThreadMembershipMessage);
};

// The product URL is not team-scoped, so currentTeamId may be empty on a cold
// load. Fall back to the user's first team; '' means skip updateRead.
export const resolveReadTeamId = (state: GlobalState): string => {
    const {currentTeamId, myMembers} = state.entities.teams;
    if (currentTeamId) {
        return currentTeamId;
    }
    return Object.keys(myMembers ?? {})[0] ?? '';
};

// Whether the host tracks thread read state for the current user (collapsed reply threads), as
// the host's isCollapsedThreadsEnabled decides it. Without it the host keeps no thread unread
// state, so there is nothing to show.
export const isCollapsedThreadsOn = (state: GlobalState): boolean => {
    const setting = state.entities?.general?.config?.CollapsedThreads;
    if (!setting || setting === 'disabled') {
        return false;
    }
    if (setting === 'always_on') {
        return true;
    }
    const preference = (state.entities?.preferences?.myPreferences as Record<string, {value?: string} | undefined> | undefined)?.['display_settings--collapsed_reply_threads']?.value;
    return (preference ?? (setting === 'default_on' ? 'on' : 'off')) === 'on';
};

// The listed roots (`rootIds`) the host has as unread, `exclude` (the open chat) left out. A thread
// the host has not loaded, and every thread with collapsed reply threads off, counts as read.
export const useUnreadThreadRoots = (rootIds: readonly string[], exclude = ''): ReadonlySet<string> => {
    const key = useSelector<GlobalState, string>((state) => {
        if (rootIds.length === 0 || !isCollapsedThreadsOn(state)) {
            return '';
        }
        const threads = state.entities?.threads?.threads ?? {};
        return rootIds.filter((id) => id !== exclude && (threads[id]?.unread_replies ?? 0) > 0).sort().join(',');
    });
    return useMemo(() => new Set(key ? key.split(',') : []), [key]);
};

// Marks an open chat read: once it has a reply (the user's thread membership only exists then, so
// marking a reply-less thread 404s), again whenever another reply arrives, and once more when it
// is left (the server can lag behind the open). `knownReplyCount` adds a count the caller fetched
// itself, for a root the store does not have.
export const useMarkAgentThreadRead = (rootId: string, enabled: boolean, knownReplyCount = 0) => {
    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);
    const teamId = useSelector<GlobalState, string>(resolveReadTeamId);

    // The host keeps the root's reply_count (and the loaded replies) current as replies arrive.
    const storeReplyCount = useSelector<GlobalState, number>((state) => (rootId ? Math.max(
        state.entities.posts.posts[rootId]?.reply_count ?? 0,
        state.entities.posts.postsInThread?.[rootId]?.length ?? 0,
    ) : 0));
    const replyCount = Math.max(storeReplyCount, knownReplyCount);

    // Never before the latest reply, even with a client clock behind the server's.
    const lastReplyAt = useSelector<GlobalState, number>((state) => (rootId ? Math.max(
        state.entities.posts.posts[rootId]?.last_reply_at ?? 0,
        state.entities.threads?.threads?.[rootId]?.last_reply_at ?? 0,
    ) : 0));
    const lastReplyAtRef = useRef(lastReplyAt);
    lastReplyAtRef.current = lastReplyAt;

    const markRead = useCallback((id: string) => {
        if (!teamId || !currentUserId) {
            return;
        }
        const timestamp = Math.max(Date.now(), lastReplyAtRef.current + 1);
        updateRead(currentUserId, teamId, id, timestamp).catch((error: unknown) => {
            if (isMissingThreadMembershipError(error)) {
                console.warn('Skipping AI thread read marker because thread membership is missing.', error); // eslint-disable-line no-console
                return;
            }
            console.error('Failed to update AI thread read marker:', error); // eslint-disable-line no-console
        });
    }, [currentUserId, teamId]);

    const canMarkRead = enabled && Boolean(rootId) && replyCount > 0;
    useEffect(() => {
        if (canMarkRead) {
            markRead(rootId);
        }
    }, [canMarkRead, replyCount, markRead, rootId]);

    // Set after the commit, so the cleanup below still sees the chat being left.
    const markOnLeave = useRef<(() => void) | null>(null);
    useEffect(() => {
        markOnLeave.current = canMarkRead ? () => markRead(rootId) : null;
    });
    useEffect(() => () => markOnLeave.current?.(), [rootId]);
};
