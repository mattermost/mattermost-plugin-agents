// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

import type {LLMBot} from '@/bots';
import manifest from '@/manifest';
import {isValidId} from '@/utils/ids';

/**
 * useConversationIdForThread resolves the plugin's conversation_id from a
 * Mattermost root post id by scanning posts in the thread for the
 * conversation_id prop the plugin writes onto every bot post. Post props are
 * free-form JSON that any user can set on their own posts, so only posts by
 * one of the plugin's agent bots count, and a malformed id is skipped.
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
        const bots: LLMBot[] | null | undefined = (state as any)['plugins-' + manifest.id]?.bots;
        const botConversationId = (postId: string): string => {
            const post = posts[postId];
            const convId: unknown = post?.props?.conversation_id;
            if (!isValidId(convId) || !bots?.some((bot) => bot.id === post.user_id)) {
                return '';
            }
            return convId;
        };

        const rootConvId = botConversationId(rootPostId);
        if (rootConvId) {
            return rootConvId;
        }

        const replyIds = state.entities.posts.postsInThread[rootPostId];
        if (!replyIds) {
            return '';
        }
        for (const id of replyIds) {
            const convId = botConversationId(id);
            if (convId) {
                return convId;
            }
        }
        return '';
    });
}
