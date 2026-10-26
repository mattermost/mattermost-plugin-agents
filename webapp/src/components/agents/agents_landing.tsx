// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';

import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';
import {Spinner} from '@mattermost/compass-ui/components/spinner';

import {LLMBot, useBotlist} from '@/bots';

import AgentChatPage from './agent_chat_page';
import AgentsList from './agents_list';
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
// a usable default it shows All agents with an explanation.
const AgentsLanding = ({nav}: AgentsLandingProps) => {
    const intl = useIntl();
    const {bots} = useBotlist();

    if (!bots) {
        return (
            <Loading>
                <Spinner size='20'/>
            </Loading>
        );
    }

    const defaultAgent = resolveDefaultAgent(bots);
    if (defaultAgent) {
        return (
            <AgentChatPage
                botId={defaultAgent.id}
                toChat={nav.toChat}
                toAll={nav.toAll}
            />
        );
    }

    return (
        <>
            <NoticeWrapper>
                <SectionNotice
                    type='info'
                    title={intl.formatMessage({defaultMessage: 'No default agent is available'})}
                    description={intl.formatMessage({defaultMessage: 'Choose an agent below to start a chat. A system admin can set the default agent in the plugin settings.'})}
                />
            </NoticeWrapper>
            <AgentsList/>
        </>
    );
};

const Loading = styled.div`
    display: flex;
    flex: 1 1 auto;
    align-items: center;
    justify-content: center;
`;

// Matches AgentsList's content column so the notice lines up with the list.
// No compass token covers content widths (same local value as agents_list.tsx).
const CONTENT_MAX_WIDTH = '960px';

const NoticeWrapper = styled.div`
    flex-shrink: 0;
    width: 100%;
    max-width: ${CONTENT_MAX_WIDTH};
    margin: 0 auto;
    padding: var(--spacing-xl) var(--spacing-xxxl) 0;
`;

export default AgentsLanding;
