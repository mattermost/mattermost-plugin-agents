// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {ReactNode, useEffect, useRef} from 'react';
import styled from 'styled-components';

import AgentsList from './agents_list';
import ProductSidebar from './product_sidebar';
import {AGENTS_ROUTE, AgentsRoute, AgentsView, useAgentsRoute} from './use_agents_route';

export {AGENTS_ROUTE};

// Seam for the center pane: each view maps to its content. Later steps replace the
// null entries with CustomPromptsPage and AgentChatPage (one line each).
export type ContentRenderers = Record<AgentsView, (route: AgentsRoute) => ReactNode>;

export const defaultContentRenderers: ContentRenderers = {
    all: () => <AgentsList/>,
    prompts: () => null,
    chat: () => null,
};

interface AgentsPageProps {
    renderers?: ContentRenderers;
}

// Product mainComponent — rendered by registerProduct when the route matches.
const AgentsPage = ({renderers = defaultContentRenderers}: AgentsPageProps) => {
    const pageRef = useRef<HTMLDivElement>(null);
    const route = useAgentsRoute();
    const {toAll, toPrompts} = route;

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
        const restored: Array<{el: HTMLElement; overflowY: string}> = [];

        if (wrapper) {
            // Mattermost's product shell may scroll when our page grows taller than the viewport.
            // Pin overflow on those ancestors so only the content outlet scrolls.
            let node: HTMLElement | null = wrapper.parentElement;
            while (node) {
                const {overflowY} = window.getComputedStyle(node);
                if (overflowY === 'auto' || overflowY === 'scroll') {
                    restored.push({el: node, overflowY: node.style.overflowY});
                    node.style.overflowY = 'hidden';
                }
                node = node.parentElement;
            }
        }

        return () => {
            restored.forEach(({el, overflowY}) => {
                el.style.overflowY = overflowY;
            });
        };
    }, []);

    return (
        <PageWrapper ref={pageRef}>
            <ProductSidebar
                activeView={route.view}
                onSelectAll={toAll}
                onSelectPrompts={toPrompts}
            />
            <ContentOutlet>
                {renderers[route.view](route)}
            </ContentOutlet>
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
    flex-direction: row;
    width: 100%;
    background: var(--center-channel-bg);
    overflow: hidden;
`;

// The outlet owns the definite height (min-height: 0) and its own scroll region.
// Chat content (host ThreadViewer) must fill it and must not scroll itself.
const ContentOutlet = styled.main`
    display: flex;
    flex: 1 1 0;
    flex-direction: column;
    min-width: 0;
    min-height: 0;
    overflow-x: hidden;
    overflow-y: auto;
`;

export default AgentsPage;
