// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect} from 'react';
import {useSelector} from 'react-redux';
import styled from 'styled-components';

import {Spinner} from '@mattermost/compass-ui/components/spinner';

import {getSelectedAgentId, resolvePreferredBot, useBotlist} from '@/bots';

import type {AgentsNavigation} from './use_agents_route';

interface AgentsLandingProps {
    nav: Pick<AgentsNavigation, 'replaceWithAll' | 'replaceWithChat'>;
}

// Product root: hands over to a new chat with the agent the RHS would pick (the user's selected
// agent, else the system default), replacing the root entry so Back never bounces through it.
// Without either it hands over to All agents (never the first agent listed).
const AgentsLanding = ({nav}: AgentsLandingProps) => {
    const {bots} = useBotlist();
    const selectedId = useSelector(getSelectedAgentId);
    const agent = resolvePreferredBot(bots, selectedId);
    const agentId = agent?.id ?? '';
    const loaded = bots !== null;
    const {replaceWithAll, replaceWithChat} = nav;

    useEffect(() => {
        if (agentId) {
            replaceWithChat(agentId);
        } else if (loaded) {
            replaceWithAll();
        }
    }, [agentId, loaded, replaceWithAll, replaceWithChat]);

    return (
        <Loading>
            <Spinner size='20'/>
        </Loading>
    );
};

const Loading = styled.div`
    display: flex;
    flex: 1 1 auto;
    align-items: center;
    justify-content: center;
`;

export default AgentsLanding;
