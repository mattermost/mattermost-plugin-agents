// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef} from 'react';
import styled from 'styled-components';

import manifest from '@/manifest';

import AgentsList from './agents_list';

export const AGENTS_ROUTE = `/plug/${manifest.id}/agents`;

// Product mainComponent — rendered by registerProduct when the route matches.
// No URL-matching or overlay needed; Mattermost's product routing handles it.
const AgentsPage = () => {
    const pageRef = useRef<HTMLDivElement>(null);

    useEffect(() => {
        // The host webapp owns `app__body` on document.body (see MM-67913 / Boards #188).
        // ChannelController normally sets `channel-view` on #root, but it's not loaded in
        // product views. Without it the global header loses its themed colors.
        const root = document.getElementById('root');
        if (root && !root.classList.contains('channel-view')) {
            root.classList.add('channel-view');
        }
    }, []);

    useEffect(() => {
        const wrapper = pageRef.current;
        if (!wrapper) {
            return undefined;
        }

        // Mattermost's product shell may scroll when our page grows taller than the viewport.
        // Pin overflow on those ancestors so only the agents list viewport scrolls.
        const restored: Array<{el: HTMLElement; overflow: string}> = [];
        let node: HTMLElement | null = wrapper.parentElement;
        while (node) {
            const {overflowY} = window.getComputedStyle(node);
            if (overflowY === 'auto' || overflowY === 'scroll') {
                restored.push({el: node, overflow: node.style.overflow});
                node.style.overflow = 'hidden';
            }
            node = node.parentElement;
        }

        return () => {
            restored.forEach(({el, overflow}) => {
                el.style.overflow = overflow;
            });
        };
    }, []);

    return (
        <PageWrapper ref={pageRef}>
            <PageLayout>
                <AgentsList/>
            </PageLayout>
        </PageWrapper>
    );
};

const PageWrapper = styled.div`
    position: absolute;
    top: 0;
    right: 0;
    bottom: 0;
    left: 0;
    display: flex;
    flex-direction: column;
    width: 100%;
    background: var(--center-channel-bg, #fff);
    overflow: hidden;
`;

const PageLayout = styled.div`
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
    width: 100%;
    overflow: hidden;
`;

export default AgentsPage;
