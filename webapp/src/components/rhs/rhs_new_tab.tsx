// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

import {ErrorMessage} from '@mattermost/compass-ui/components/error-message';
import {Spinner} from '@mattermost/compass-ui/components/spinner';

import type {LLMBot} from '@/bots';
import {useRefreshAgentThreads} from '@/hooks/use_agent_threads';

import AgentNewChat from '../agent_chat/agent_new_chat';
import {useAgentDMChannel} from '../agents/use_agent_dm_channel';

type Props = {
    selectPost: (postId: string) => void
    setCurrentTab: (tab: string) => void
    activeBot: LLMBot | null
}

// The RHS new-chat tab: the same new-chat state as the Agents product view
// (avatar, greeting, pill composer), sized to fill the RHS.
const RHSNewTab = ({selectPost, setCurrentTab, activeBot}: Props) => {
    // Creates the bot DM when missing, stores its id on the bot in Redux, and
    // loads the channel, membership and roles the host ThreadViewer needs.
    const dm = useAgentDMChannel(activeBot);
    const refreshThreads = useRefreshAgentThreads();

    const onCreated = useCallback((rootId: string) => {
        selectPost(rootId);
        setCurrentTab('thread');

        // The chat lists pick up the new chat; its title follows over the websocket.
        refreshThreads();
    }, [selectPost, setCurrentTab, refreshThreads]);

    let content;
    if (dm.status === 'error') {
        content = (
            <Status>
                <ErrorMessage message={<FormattedMessage defaultMessage='Failed to create chat channel. Please try again later.'/>}/>
            </Status>
        );
    } else if (dm.status === 'loading' || !activeBot) {
        content = (
            <Status>
                <Spinner size='16'/>
                <FormattedMessage defaultMessage='Setting up chat channel...'/>
            </Status>
        );
    } else {
        content = (
            <AgentNewChat
                key={activeBot.id}
                layout='panel'
                bot={activeBot}
                channelId={dm.channelId}
                onCreated={onCreated}
            />
        );
    }

    return (
        <Frame data-testid='rhs-new-tab'>
            {content}
        </Frame>
    );
};

const Frame = styled.div`
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    min-height: 0;
    overflow-y: auto;
`;

const Status = styled.div`
    display: flex;
    flex: 1 1 auto;
    align-items: center;
    justify-content: center;
    gap: var(--spacing-xs);
    padding: var(--spacing-xl);
`;

export default React.memo(RHSNewTab);
