// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect} from 'react';
import styled from 'styled-components';

import {Spinner} from '@mattermost/compass-ui/components/spinner';

import {LLMBot, useBotlist} from '@/bots';

import AgentChatPage from './agent_chat_page';
import type {AgentsNavigation} from './use_agents_route';

// The server marks the configured default agent (DefaultBotUserID, matched by ID)
// with isDefault, and only lists agents the user may use. Fail closed: no flag
// means no default, never the first bot.
export const resolveDefaultAgent = (bots: LLMBot[] | null): LLMBot | null => (
    bots?.find((bot) => bot.isDefault) ?? null
);

interface AgentsLandingProps {
    nav: AgentsNavigation;
}

// Product root: a new chat with the default agent, rendered in place (the URL
// stays at the product root so Back never bounces through a redirect). Without
// a usable default it hands over to the All agents view.
const AgentsLanding = ({nav}: AgentsLandingProps) => {
    const {bots} = useBotlist();
    const defaultAgent = resolveDefaultAgent(bots);
    const {replaceWithAll} = nav;
    const noDefault = bots !== null && !defaultAgent;

    useEffect(() => {
        if (noDefault) {
            replaceWithAll();
        }
    }, [noDefault, replaceWithAll]);

    if (!defaultAgent) {
        return (
            <Loading>
                <Spinner size='20'/>
            </Loading>
        );
    }

    return (
        <AgentChatPage
            botId={defaultAgent.id}
            toChat={nav.toChat}
            toAll={nav.toAll}
        />
    );
};

const Loading = styled.div`
    display: flex;
    flex: 1 1 auto;
    align-items: center;
    justify-content: center;
`;

export default AgentsLanding;
