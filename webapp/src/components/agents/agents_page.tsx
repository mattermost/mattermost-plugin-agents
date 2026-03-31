// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useState} from 'react';
import styled from 'styled-components';

import manifest from '@/manifest';

import AgentsLicenseGate from './agents_license_gate';
import AgentsList from './agents_list';

const AGENTS_PATH = `/plug/${manifest.id}/agents`;

const AgentsPage = () => {
    const [visible, setVisible] = useState(false);

    useEffect(() => {
        const checkPath = () => {
            setVisible(window.location.pathname.endsWith(AGENTS_PATH));
        };

        checkPath();

        // Listen for popstate (browser back/forward)
        window.addEventListener('popstate', checkPath);

        // Listen for pushstate/replacestate via a short interval
        // since browserHistory.push doesn't fire popstate.
        const interval = setInterval(checkPath, 200);

        return () => {
            window.removeEventListener('popstate', checkPath);
            clearInterval(interval);
        };
    }, []);

    if (!visible) {
        return null;
    }

    return (
        <PageOverlay>
            <PageContainer>
                <AgentsLicenseGate>
                    <AgentsList/>
                </AgentsLicenseGate>
            </PageContainer>
        </PageOverlay>
    );
};

const PageOverlay = styled.div`
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    z-index: 1000;
    background: var(--center-channel-bg, #fff);
    overflow-y: auto;
`;

const PageContainer = styled.div`
    max-width: 1040px;
    margin: 0 auto;
    padding: 40px 32px;
`;

export default AgentsPage;

// Navigation helper — call this to navigate to the agents page.
export function navigateToAgentsPage() {
    if ((window as any).WebappUtils?.browserHistory) {
        (window as any).WebappUtils.browserHistory.push(AGENTS_PATH);
    } else {
        window.location.pathname = AGENTS_PATH;
    }
}

// Navigation helper — call this to navigate back from the agents page.
export function navigateFromAgentsPage() {
    if ((window as any).WebappUtils?.browserHistory) {
        (window as any).WebappUtils.browserHistory.goBack();
    }
}
