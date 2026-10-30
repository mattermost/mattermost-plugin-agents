// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {ReactNode, useContext, useMemo} from 'react';
import {ReactReduxContext} from 'react-redux';
import {useIntl} from 'react-intl';
import styled from 'styled-components';
import CodeBracketsIcon from '@mattermost/compass-icons/components/code-brackets';
import CreationOutlineIcon from '@mattermost/compass-icons/components/creation-outline';
import {ChannelSidebarItem} from '@mattermost/compass-ui/components/channel-sidebar-item';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {Scrollbar} from '@mattermost/compass-ui/components/scrollbar';

import {useBotlist} from '@/bots';
import {useUnreadThreadRoots} from '@/hooks/use_agent_thread_read';
import {useAgentThreads} from '@/hooks/use_agent_threads';

import {useGeneratingThreadRoots} from '@/components/agent_chat/use_thread_generating';

import AgentThreadsList from './agent_threads_list';
import FindAgentsButton from './find_agents_button';
import SidebarAddMenu from './sidebar_add_menu';
import {useAgentsRoute, type AgentsView} from './use_agents_route';
import {ChatActionsDialogs, useChatActions} from './use_chat_actions';

interface ProductSidebarProps {
    activeView: AgentsView;
    onSelectAll: () => void;
    onSelectPrompts: () => void;

    // Extension point for the "Your agents" section (rendered below the nav rows).
    children?: ReactNode;
}

interface NavRowProps {
    name: string;
    glyph: ReactNode;
    active: boolean;
    onClick: () => void;
}

// ChannelSidebarItem has no custom icon slot, so (as in the prototype) the glyph is
// overlaid on the item's icon container and the built-in SVG is hidden.
const NavRow = ({name, glyph, active, onClick}: NavRowProps) => (
    <NavRowWrapper>
        <NavIcon aria-hidden={true}>
            <Icon
                size='16'
                glyph={glyph}
            />
        </NavIcon>
        <ChannelSidebarItem
            name={name}
            leadingVisual='insights'
            active={active}
            onClick={onClick}
        />
    </NavRowWrapper>
);

// Data wiring for the "Your agents" section: bots, DM threads (the shared stored list, kept current
// over the websocket), route highlighting, and the chat actions' dialogs.
const YourAgentsSection = () => {
    const route = useAgentsRoute();
    const {bots} = useBotlist();
    const {threadsByBot, loading, error, refresh} = useAgentThreads(bots);
    const generatingRoots = useGeneratingThreadRoots();
    const listedRoots = useMemo(() => Object.values(threadsByBot).flat().map((t) => t.root_post_id).filter((id): id is string => Boolean(id)), [threadsByBot]);
    const unreadRoots = useUnreadThreadRoots(listedRoots, route.view === 'chat' ? route.rootId ?? '' : '');
    const chatActions = useChatActions();

    return (
        <>
            <AgentThreadsList
                bots={bots ?? []}
                threadsByBot={threadsByBot}
                loading={loading && Object.keys(threadsByBot).length === 0}
                error={error}
                activeBotId={route.view === 'chat' ? route.botId : ''}
                activeRootId={route.view === 'chat' ? route.rootId : ''}
                generatingRoots={generatingRoots}
                unreadRoots={unreadRoots}
                onSelectThread={route.toChat}
                onNewChat={route.toChat}
                onRetry={refresh}
                chatActions={chatActions}
            />
            <ChatActionsDialogs actions={chatActions}/>
        </>
    );
};

const ProductSidebar = ({activeView, onSelectAll, onSelectPrompts, children}: ProductSidebarProps) => {
    const intl = useIntl();
    const title = intl.formatMessage({defaultMessage: 'Agents'});

    // The section needs the plugin's redux store; without one there is nothing to list.
    const hasStore = useContext(ReactReduxContext) !== null;

    return (
        <Aside aria-label={title}>
            <Header>
                <ProductName>{title}</ProductName>
                {hasStore && <SidebarAddMenu/>}
            </Header>
            {hasStore && (
                <FindAgentsRow>
                    <FindAgentsButton/>
                </FindAgentsRow>
            )}
            <ScrollArea color='--sidebar-text-rgb'>
                <nav aria-label={title}>
                    <NavGroup>
                        <NavRow
                            name={intl.formatMessage({defaultMessage: 'All agents'})}
                            glyph={<CreationOutlineIcon/>}
                            active={activeView === 'all'}
                            onClick={onSelectAll}
                        />
                        <NavRow
                            name={intl.formatMessage({defaultMessage: 'Custom Prompts'})}
                            glyph={<CodeBracketsIcon/>}
                            active={activeView === 'prompts'}
                            onClick={onSelectPrompts}
                        />
                    </NavGroup>
                </nav>
                {hasStore && <YourAgentsSection/>}
                {children}
            </ScrollArea>
        </Aside>
    );
};

// Prototype width is 264px; no compass token exists, so compose it (48px * 5.5).
const Aside = styled.aside`
    display: flex;
    flex-direction: column;
    flex: 0 0 calc(var(--spacing-xxxxxl) * 5.5);
    width: calc(var(--spacing-xxxxxl) * 5.5);
    min-height: 0;
    background: var(--sidebar-bg);
    color: var(--sidebar-text);
`;

// The button's modal reads agents and chats from the plugin's redux store.
const FindAgentsRow = styled.div`
    flex: 0 0 auto;
    padding: 0 var(--spacing-l) var(--spacing-m);
`;

const Header = styled.div`
    display: flex;
    justify-content: space-between;
    gap: var(--spacing-xs);
    flex: 0 0 auto;
    align-items: center;
    padding: calc(var(--spacing-m) + var(--spacing-xxxxs)) var(--spacing-l);
`;

const ProductName = styled.h2`
    min-width: 0;
    margin: 0;
    padding: var(--spacing-xxxxs) var(--spacing-xs);
    overflow: hidden;
    font-family: var(--font-family-heading);
    font-size: var(--font-size-200);
    font-weight: var(--font-weight-semibold);
    line-height: var(--line-height-200);
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const ScrollArea = styled(Scrollbar)`
    flex: 1 1 auto;
    min-height: 0;
`;

const NavGroup = styled.div`
    display: flex;
    flex-direction: column;
    padding-bottom: var(--spacing-s);
`;

const NavRowWrapper = styled.div`
    position: relative;

    [class*='icon-container'] > svg {
        visibility: hidden;
    }
`;

// Matches the item's icon-container offset: spacing-l + 2 * spacing-xxxxs + spacing-xxxs.
const NavIcon = styled.span`
    position: absolute;
    top: 0;
    bottom: 0;
    left: 0;
    z-index: 1;
    display: flex;
    align-items: center;
    padding-left: calc(var(--spacing-l) + var(--spacing-xxxxs) + var(--spacing-xxxs));
    color: var(--sidebar-text);
    opacity: 0.64;
    pointer-events: none;
`;

export default ProductSidebar;
