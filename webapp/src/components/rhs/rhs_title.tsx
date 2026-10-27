// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useState} from 'react';
import {useDispatch, useSelector} from 'react-redux';
import {FormattedMessage, useIntl} from 'react-intl';
import styled from 'styled-components';
import ArrowLeftIcon from '@mattermost/compass-icons/components/arrow-left';
import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import PlusIcon from '@mattermost/compass-icons/components/plus';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {UserAvatar} from '@mattermost/compass-ui/components/user-avatar';

//@ts-ignore it exists
import aiIcon from '../../../../assets/bot_icon.png';

import {useBotlist, type LLMBot} from '@/bots';
import {getProfilePictureUrl, getUserMCPTools} from '@/client';
import AgentChatNameMenu from '@/components/agents/agent_chat_name_menu';
import {useAgentsRoute} from '@/components/agents/use_agents_route';
import {useComposerToolPreferences} from '@/components/agent_chat/use_composer_tool_preferences';
import manifest from '@/manifest';
import type {RHSTab} from '@/redux';

import {useConversationIdForThread} from '@/hooks/use_conversation_id_for_thread';

import ContextUsageIndicator from './context_usage_indicator';
import ToolProviderPopover, {type UserMCPServerInfo} from './tool_provider_popover';

// No compass token for the translucent hover fill (the agent chat header's name button uses the same).
const HOVER_PERCENT = '8%';

const AgentAvatar = ({bot}: {bot: LLMBot}) => (
    <UserAvatar
        src={getProfilePictureUrl(bot.id, bot.lastIconUpdate)}
        alt=''
        aria-hidden='true'
        name={bot.displayName}
        size='24'
    />
);

// The RHS header title: Back to the chat history, the chat (or the agent, in a new chat) with the
// same menu as the Agents product's chat header (recent chats, new, rename, archive, view all
// agents) plus a Switch agent submenu, and at the end the tools button on a new chat, or the
// context usage ring and New chat everywhere else.
// The host wraps the title in its own heading landmark (a span#rhsPanelTitle), so none is added here.
const RHSTitle = () => {
    const intl = useIntl();
    const {bots, activeBot, setActiveBot} = useBotlist();
    const dispatch = useDispatch();

    const pluginState = useSelector((state: any) => state['plugins-' + manifest.id]);
    const threadOpen = Boolean(pluginState?.selectedPostId);
    const listTab: RHSTab = pluginState?.rhsTab ?? 'new';
    const onNewChat = !threadOpen && listTab === 'new';
    const onHistory = !threadOpen && listTab === 'threads';
    const activeConversationId = useConversationIdForThread(pluginState?.selectedPostId);
    const {toAll} = useAgentsRoute();

    // The agent of the open chat: the DM partner, or the root's author for chats an agent started
    // (as the RHS body finds it). It can differ from the active agent that new chats use.
    const selectedRoot = useSelector((state: any) => (
        (pluginState?.selectedPostId && state.entities?.posts?.posts?.[pluginState.selectedPostId]) || null
    ));

    // The tools button's data: the same per-user preferences and server list the RHS body used
    // to load; a toggle here is published to the composer's tools menu too.
    const toolPrefs = useComposerToolPreferences();
    const [preloadedServers, setPreloadedServers] = useState<UserMCPServerInfo[]>([]);
    useEffect(() => {
        getUserMCPTools().then((response) => setPreloadedServers(response.servers)).catch(() => null);
    }, []);

    // Switching agents behaves like starting a new chat with the new agent (leave the open
    // thread and show the new-chat tab); choosing the current agent changes nothing.
    const switchAgent = useCallback((bot: LLMBot) => {
        setActiveBot(bot);
        dispatch({type: 'SELECT_AI_POST', postId: ''});
        dispatch({type: 'AI_RHS_NEW_CHAT'});
    }, [setActiveBot, dispatch]);
    const startNewChat = useCallback(() => {
        dispatch({type: 'SELECT_AI_POST', postId: ''});
        dispatch({type: 'AI_RHS_SET_TAB', tab: 'new'});
    }, [dispatch]);

    const newChatLabel = intl.formatMessage({defaultMessage: 'New chat'});
    const backLabel = intl.formatMessage({defaultMessage: 'Back to chat history'});
    const back = onHistory ? null : (
        <IconButton
            size='small'
            padding='compact'
            icon={<Icon glyph={<ArrowLeftIcon/>}/>}
            aria-label={backLabel}
            title={backLabel}
            data-testid='chat-history'
            onClick={() => {
                dispatch({type: 'SELECT_AI_POST', postId: ''});
                dispatch({type: 'AI_RHS_SET_TAB', tab: 'threads'});
            }}
        />
    );

    // The end of the title: the tools button on a new chat (SA agents have no per-user connections
    // or preferences to manage), New chat (with the context ring) on the history and an open chat.
    let actions: React.ReactNode = null;
    if (onNewChat) {
        if (activeBot && activeBot.useServiceAccountAuth !== true) {
            actions = (
                <ToolProviderPopover
                    disabledServers={toolPrefs.disabledServers}
                    onDisabledServersChange={toolPrefs.setDisabledServers}
                    preloadedServers={preloadedServers}
                    enabledMCPTools={activeBot.enabledMCPTools}
                    autoEnableNewMCPTools={activeBot.autoEnableNewMCPTools}
                />
            );
        }
    } else {
        actions = (
            <>
                {activeConversationId && <ContextUsageIndicator conversationId={activeConversationId}/>}
                <NewChatButton
                    size='small'
                    padding='compact'
                    rounded={true}
                    icon={<Icon glyph={<PlusIcon/>}/>}
                    aria-label={newChatLabel}
                    title={newChatLabel}
                    data-testid='new-chat'
                    onClick={startNewChat}
                />
            </>
        );
    }
    const end = actions && <Actions>{actions}</Actions>;

    // The chat history lists every agent's chats, so it has the generic title (no agent menu),
    // as do loading, no agents, and an unresolved active agent.
    if (onHistory || !bots || bots.length === 0 || !activeBot) {
        return (
            <Container
                data-testid='rhs-title'
                $hasBack={Boolean(back)}
            >
                {back}
                <FallbackIcon
                    src={aiIcon}
                    alt=''
                />
                <Name><FormattedMessage defaultMessage='Agents'/></Name>
                {end}
            </Container>
        );
    }

    const menuBot = (threadOpen && bots.find((b) => (selectedRoot?.channel_id && b.dmChannelID === selectedRoot.channel_id) || b.id === selectedRoot?.user_id)) || activeBot;
    return (
        <Container
            data-testid='rhs-title'
            $hasBack={Boolean(back)}
        >
            {back}
            <AgentAvatar bot={menuBot}/>
            <AgentChatNameMenu
                bot={menuBot}
                rootId={pluginState?.selectedPostId || ''}
                agents={{bots, onSwitch: switchAgent}}
                onSelectChat={(rootId) => dispatch({type: 'SELECT_AI_POST', postId: rootId})}
                onNewChat={startNewChat}
                onArchived={startNewChat}
                onViewAll={toAll}
                renderTrigger={({ariaLabel, label, expanded, onClick}) => (
                    <TitleButton
                        type='button'
                        aria-label={ariaLabel}
                        title={label}
                        aria-haspopup='menu'
                        aria-expanded={expanded}
                        data-testid='bot-selector-rhs'
                        onClick={onClick}
                    >
                        <Name>{label}</Name>
                        <Icon
                            glyph={<ChevronDownIcon/>}
                            size='16'
                        />
                    </TitleButton>
                )}
            />
            {end}
        </Container>
    );
};

// The Back button sits flush with the header's start; without it the title is inset.
// min-width: 0 all the way down lets a long name ellipsize instead of pushing the
// host's expand, popout and close buttons out of the header.
// The host pads its title area by 1.6rem on both sides (sass/layout/_sidebar-right.scss
// `.sidebar--right__title`); the end of the title (the New chat or tools button) takes that padding
// back, less 4px (--spacing-xxxs), so it sits close to the host's own header buttons. Not a compass
// value: it mirrors host CSS.
const HOST_TITLE_INLINE_PADDING = '1.6rem';

const Container = styled.span<{$hasBack: boolean}>`
    display: flex;
    flex: 1 1 auto;
    min-width: 0;
    align-items: center;
    gap: var(--spacing-xs);
    margin-left: ${({$hasBack}) => ($hasBack ? '0' : 'var(--spacing-xs)')};
    margin-right: calc(-1 * ${HOST_TITLE_INLINE_PADDING} + var(--spacing-xxxs));
`;

const Actions = styled.span`
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    gap: var(--spacing-xxs);
    margin-inline-start: auto;
`;

// No compass token for the resting and hover washes of the text color behind New chat, which set it
// apart from the bare back button next to the title.
const NEW_CHAT_FILL = 'rgba(var(--center-channel-color-rgb), 0.08)';
const NEW_CHAT_FILL_HOVER = 'rgba(var(--center-channel-color-rgb), 0.12)';

const NewChatButton = styled(IconButton)`
    && {
        background-color: ${NEW_CHAT_FILL};
    }

    &&:hover:not(:disabled) {
        background-color: ${NEW_CHAT_FILL_HOVER};
    }
`;

const FallbackIcon = styled.img`
    width: var(--icon-size-24);
    height: var(--icon-size-24);
    flex: 0 0 auto;
    border-radius: var(--radius-pill);
`;

const Name = styled.span`
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const TitleButton = styled.button`
    display: inline-flex;
    min-width: 0;
    align-items: center;
    margin: 0;
    padding: 0 var(--spacing-xxxs) 0 var(--spacing-xxs);
    border: none;
    border-radius: var(--radius-s);
    background: transparent;
    color: inherit;
    font: inherit;
    cursor: pointer;
    text-align: left;

    &:hover,
    &[aria-expanded='true'] {
        background: color-mix(in srgb, var(--center-channel-color) ${HOVER_PERCENT}, transparent);
    }

    &:focus-visible {
        outline: var(--spacing-xxxxs) solid var(--focus-ring-color);
        outline-offset: var(--spacing-xxxxs);
    }

    svg {
        flex-shrink: 0;
    }
`;

export default RHSTitle;
