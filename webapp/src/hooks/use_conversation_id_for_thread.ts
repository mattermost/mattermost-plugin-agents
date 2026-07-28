// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

/**
 * useConversationIdForThread resolves the plugin's conversation_id from a
 * Mattermost root post id by scanning posts in the thread for the
 * conversation_id prop the plugin writes onto every bot post.
 *
 * Returns an empty string ('') when no bot post is in Redux yet (e.g. a fresh
 * thread the user hasn't opened, or one where the assistant has not replied).
 */
export function useConversationIdForThread(rootPostId: string | null | undefined): string {
    return useSelector<GlobalState, string>((state) => {
        if (!rootPostId) {
            return '';
        }
        const posts = state.entities.posts.posts;

        const root = posts[rootPostId];
        const rootConvId = root?.props?.conversation_id;
        if (typeof rootConvId === 'string' && rootConvId) {
            return rootConvId;
        }

        const replyIds = state.entities.posts.postsInThread[rootPostId];
        if (!replyIds) {
            return '';
        }
        for (const id of replyIds) {
            const convId = posts[id]?.props?.conversation_id;
            if (typeof convId === 'string' && convId) {
                return convId;
            }
        }
        return '';
    });
}
