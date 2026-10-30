// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect, useRef} from 'react';
import {useDispatch, useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

import {getProfilesByIds} from '@/client';

// Ids of the thread's posters whose profiles are not in the store, sorted and
// joined so the selector result is stable between renders.
const missingPosterIds = (state: GlobalState, rootId: string) => {
    if (!rootId) {
        return '';
    }
    const posts = state.entities.posts?.posts ?? {};
    const profiles = state.entities.users?.profiles ?? {};
    const ids = new Set<string>();
    for (const postId of [rootId, ...(state.entities.posts?.postsInThread?.[rootId] ?? [])]) {
        const userId = posts[postId]?.user_id;
        if (userId && !profiles[userId]) {
            ids.add(userId);
        }
    }
    return [...ids].sort().join(',');
};

// Loads the profiles of everyone who posted in the thread into the host store.
// The host reply composer ranks thread posters first in @mention suggestions
// (makeGetProfilesForThread) and its AtMentionProvider throws on a poster
// with a status but no profile, which leaves the mention list empty. The
// ThreadViewer does not always load them (e.g. the agent's own profile).
export const useThreadProfiles = (rootId: string) => {
    const dispatch = useDispatch();
    const missing = useSelector<GlobalState, string>((state) => missingPosterIds(state, rootId));

    // Each id is requested once per composer, so a failed lookup is not retried in a loop.
    const requested = useRef(new Set<string>());
    useEffect(() => {
        const ids = missing.split(',').filter((id) => id && !requested.current.has(id));
        if (ids.length === 0) {
            return;
        }
        ids.forEach((id) => requested.current.add(id));
        getProfilesByIds(ids).then((profiles) => {
            if (profiles.length > 0) {
                dispatch({type: 'RECEIVED_PROFILES', data: profiles});
            }
        }).catch((e) => {
            console.error('Failed to load thread profiles for mention suggestions:', e); // eslint-disable-line no-console
        });
    }, [missing, dispatch]);
};
