// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {GlobalState} from '@mattermost/types/store';

import manifest from '@/manifest';

interface PostLike {
    id: string;
    channel_id: string;
    root_id?: string;
    user_id: string;
    props?: Record<string, unknown>;
}

const postsOf = (state: GlobalState) => (state.entities?.posts?.posts ?? {}) as Record<string, PostLike | undefined>;

// The current user requested this agent post. llm_requester_user_id is set on every agent post
// with a requester; the post it responds to is the fallback.
export function isRequestedByCurrentUser(state: GlobalState, post: PostLike): boolean {
    const currentUserId = state.entities?.users?.currentUserId ?? '';
    if (post.props?.llm_requester_user_id) {
        return post.props.llm_requester_user_id === currentUserId;
    }
    const respondingTo = post.props?.responding_to;
    return typeof respondingTo === 'string' && postsOf(state)[respondingTo]?.user_id === currentUserId;
}

// An agent post the current user's chats can change for: one answering their request, or one in
// their DM with an agent. A post the store does not have counts as unrelated: postupdate events
// reach every member of a channel, most of them for other people's requests.
export function isCurrentUsersAgentPost(state: GlobalState, postId: string): boolean {
    const post = postsOf(state)[postId];
    if (!post) {
        return false;
    }
    if (isRequestedByCurrentUser(state, post)) {
        return true;
    }
    const bots: Array<{dmChannelID?: string}> = (state as any)[`plugins-${manifest.id}`]?.bots ?? [];
    return bots.some((bot) => Boolean(bot.dmChannelID) && bot.dmChannelID === post.channel_id);
}
