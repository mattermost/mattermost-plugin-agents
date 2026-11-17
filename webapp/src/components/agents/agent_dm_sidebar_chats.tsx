// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useMemo} from 'react';
import {useSelector} from 'react-redux';
import styled from 'styled-components';

import type {Channel} from '@mattermost/types/channels';
import {GlobalState} from '@mattermost/types/store';

import {dmOtherUserId} from '@/agent_dm_view';
import {useBotlist} from '@/bots';

import {useDMPostId} from './agent_dm_route';
import {AgentThreadsSection} from './product_sidebar';
import {getHistory} from './use_agents_route';

export interface AgentDMSidebarChatsProps {
    channel: Channel;
    currentTeamName: string;
    isCurrentChannel: boolean;
}

// The agent's recent chats nested under its DM item in the host's channel sidebar
// (registerSidebarChannelChildrenComponent), with the Agents product's "Show more". A chat opens by
// navigating to the DM URL with its root post, which the agent DM view follows.
const AgentDMSidebarChats = ({channel, currentTeamName, isCurrentChannel}: AgentDMSidebarChatsProps) => {
    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);
    const {bots} = useBotlist();
    const botId = dmOtherUserId(channel.name, currentUserId);
    const bot = useMemo(() => bots?.find((b) => b.id === botId) ?? null, [bots, botId]);
    const listBots = useMemo(() => (bot ? [bot] : null), [bot]);

    const urlPostId = useDMPostId();
    const openRootId = useSelector<GlobalState, string>((state) => {
        if (!isCurrentChannel || !urlPostId) {
            return '';
        }
        const post = state.entities.posts.posts[urlPostId];
        return post ? post.root_id || post.id : urlPostId;
    });

    const toChat = useCallback((_botId: string, rootId?: string) => {
        if (!bot) {
            return;
        }
        const dm = `/${currentTeamName}/messages/@${encodeURIComponent(bot.username)}`;
        getHistory()?.push(rootId ? `${dm}/${encodeURIComponent(rootId)}` : dm);
    }, [bot, currentTeamName]);

    if (!listBots) {
        return null;
    }

    return (
        <Nested>
            <AgentThreadsSection
                bots={listBots}
                activeBotId={isCurrentChannel ? botId : undefined} // eslint-disable-line no-undefined
                activeRootId={openRootId}
                onSelectThread={toChat}
                onNewChat={toChat}
                hideAgentRows={true}
            />
        </Nested>
    );
};

const Nested = styled.div`
    padding-left: var(--spacing-xl);
`;

export default AgentDMSidebarChats;
