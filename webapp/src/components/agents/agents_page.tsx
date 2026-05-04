// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect} from 'react';
import styled from 'styled-components';

import manifest from '@/manifest';

import AgentsLicenseGate from './agents_license_gate';
import AgentsList from './agents_list';

export const AGENTS_ROUTE = `/plug/${manifest.id}/agents`;

// Product mainComponent — rendered by registerProduct when the route matches.
// No URL-matching or overlay needed; Mattermost's product routing handles it.
const AgentsPage = () => {
    useEffect(() => {
        // ChannelController normally sets these classes, but it's not loaded in
        // product views. Without them the global header loses its themed colors.
        // Playbooks and Boards do the same thing in their top-level components.
        document.body.classList.add('app__body');

        const root = document.getElementById('root');
        if (root && !root.classList.contains('channel-view')) {
            root.classList.add('channel-view');
        }

        return () => {
            document.body.classList.remove('app__body');
        };
    }, []);

    return (
        <PageWrapper>
            <PageContainer>
                <AgentsLicenseGate>
                    <AgentsList/>
                </AgentsLicenseGate>
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
