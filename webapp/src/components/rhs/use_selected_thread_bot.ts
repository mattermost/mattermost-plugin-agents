// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSelector} from 'react-redux';

import type {LLMBot} from '@/bots';
import manifest from '@/manifest';

export interface SelectedThread {

    // The open thread's root post id; '' when none is open.
    rootId: string;

    // The thread's channel, once the store has its root post.
    channelId: string;

    // The agent of the thread: the DM partner, or the root's author for chats an agent started
    // (e.g. channel analysis). Null until the root post and the bots are loaded, or when no listed
    // agent matches.
    bot: LLMBot | null;
}

// The thread open in the RHS and its agent.
export function useSelectedThreadBot(bots: LLMBot[] | null): SelectedThread {
    const rootId: string = useSelector((state: any) => state['plugins-' + manifest.id]?.selectedPostId) || '';
    const root = useSelector((state: any) => (rootId && state.entities?.posts?.posts?.[rootId]) || null) as {channel_id: string; user_id: string} | null;
    const channelId = root?.channel_id ?? '';
    const bot = (root && bots?.find((b) => (channelId && b.dmChannelID === channelId) || b.id === root.user_id)) || null;
    return {rootId, channelId, bot};
}
