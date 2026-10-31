// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef} from 'react';
import styled from 'styled-components';

import CustomPromptsManagement from '@/components/custom_prompts/custom_prompts_management';
import CustomPromptsPage from '@/components/custom_prompts/custom_prompts_page';

import AgentChatPage from './agent_chat_page';
import AgentsLanding from './agents_landing';
import AgentsList from './agents_list';
import ProductSidebar from './product_sidebar';
import AgentsSettingsPage from './settings/agents_settings_page';
import {AGENTS_ROUTE, AgentsNavigation, AgentsRoute, useAgentsRoute} from './use_agents_route';

export {AGENTS_ROUTE};

// The center pane for the current route.
const Content = ({route}: {route: AgentsRoute & AgentsNavigation}) => {
    switch (route.view) {
    case 'landing':
        return <AgentsLanding nav={route}/>;
    case 'prompts':
        return <CustomPromptsPage/>;
    case 'settings':
        return (
            <AgentsSettingsPage
                section={route.section}
                onSelectSection={route.toSettings}
            />
        );
    case 'chat':
        return (
            <AgentChatPage
                botId={route.botId ?? ''}
                rootId={route.rootId}
                toChat={route.toChat}
                toAll={route.toAll}
            />
        );
    default:
        return (
            <AgentsList
                onStartChat={route.toChat}
                openCreate={route.create === true}
                onCloseCreate={route.closeNewAgent}
            />
        );
    }
};

// Product mainComponent — rendered by registerProduct when the route matches.
const AgentsPage = () => {
    const pageRef = useRef<HTMLDivElement>(null);
    const route = useAgentsRoute();
    const {toAll, toPrompts, toSettings} = route;

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
                onSelectSettings={() => toSettings()}
            />
            <ContentOutlet>
                <Content route={route}/>
            </ContentOutlet>

            {/* Product routes get no root components, so the product mounts the Custom Prompts modal for
                the composer's "Manage prompts" (only one mounted copy ever renders). */}
            <CustomPromptsManagement/>
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

    /* Same look as the Channels view: the sidebar-colored page behind a rounded center card. */
    background: var(--sidebar-bg);
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
    border: var(--border-light);
    border-radius: var(--radius-l);
    background: var(--center-channel-bg);
    box-shadow: var(--elevation-1);
    overflow-x: hidden;
    overflow-y: auto;
`;

export default AgentsPage;
