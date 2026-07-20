// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

import manifest from '@/manifest';

import AgentsList from './agents_list';
import AutomationsList from './automations_list';

export const AGENTS_ROUTE = `/plug/${manifest.id}/agents`;

type PageTab = 'agents' | 'automations';

// Product mainComponent — rendered by registerProduct when the route matches.
// No URL-matching or overlay needed; Mattermost's product routing handles it.
const AgentsPage = () => {
    const [activeTab, setActiveTab] = useState<PageTab>('agents');

    useEffect(() => {
        // The host webapp owns `app__body` on document.body (see MM-67913 / Boards #188).
        // ChannelController normally sets `channel-view` on #root, but it's not loaded in
        // product views. Without it the global header loses its themed colors.
        const root = document.getElementById('root');
        if (root && !root.classList.contains('channel-view')) {
            root.classList.add('channel-view');
        }
    }, []);

    const handleAgentsTabClick = useCallback(() => {
        setActiveTab('agents');
    }, []);

    const handleAutomationsTabClick = useCallback(() => {
        setActiveTab('automations');
    }, []);

    return (
        <PageWrapper>
            <TabsBar>
                <TabsInner>
                    <PageTabButton
                        $active={activeTab === 'agents'}
                        onClick={handleAgentsTabClick}
                        type='button'
                    >
                        <FormattedMessage defaultMessage='Agents'/>
                    </PageTabButton>
                    <PageTabButton
                        $active={activeTab === 'automations'}
                        onClick={handleAutomationsTabClick}
                        type='button'
                    >
                        <FormattedMessage defaultMessage='Automations'/>
                    </PageTabButton>
                </TabsInner>
            </TabsBar>
            <PageContainer>
                {activeTab === 'agents' ? <AgentsList/> : <AutomationsList/>}
            </PageContainer>
        </PageWrapper>
    );
};

const PageWrapper = styled.div`
    display: flex;
    flex-direction: column;
    width: 100%;
    height: 100%;
    background: var(--center-channel-bg, #fff);
    overflow: hidden;
`;

const TabsBar = styled.div`
    flex-shrink: 0;
    width: 100%;
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    background: var(--center-channel-bg, #fff);
`;

const TabsInner = styled.div`
    display: flex;
    flex-direction: row;
    align-items: stretch;
    width: 100%;
    padding: 0 32px;
`;

const PageTabButton = styled.button<{$active: boolean}>`
    padding: 14px 16px;
    border: none;
    background: none;
    cursor: pointer;
    font-family: 'Open Sans', sans-serif;
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    color: ${(p) => (p.$active ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.64)')};
    border-bottom: 2px solid ${(p) => (p.$active ? 'var(--button-bg)' : 'transparent')};
    margin-bottom: -1px;
    transition: color 0.15s ease, border-color 0.15s ease;

    &:hover {
        color: ${(p) => (p.$active ? 'var(--button-bg)' : 'var(--center-channel-color)')};
    }
`;

const PageContainer = styled.div`
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
    width: 100%;
    max-width: 960px;
    margin: 0 auto;
    padding: 0 32px;
`;

export default AgentsPage;
