// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useState} from 'react';
import {useDispatch, useSelector} from 'react-redux';
import {FormattedMessage, useIntl} from 'react-intl';
import styled from 'styled-components';
import ArrowLeftIcon from '@mattermost/compass-icons/components/arrow-left';
import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import PencilOutlineIcon from '@mattermost/compass-icons/components/pencil-outline';

import {Button} from '@mattermost/compass-ui/components/button';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {UserAvatar} from '@mattermost/compass-ui/components/user-avatar';

//@ts-ignore it exists
import aiIcon from '../../../../assets/bot_icon.png';

import {useBotlist, type LLMBot} from '@/bots';
import {getProfilePictureUrl, getUserMCPTools} from '@/client';
import {BotDropdownMenu, BotMenuContent} from '@/components/bot_selector';
import {useComposerToolPreferences} from '@/components/agent_chat/use_composer_tool_preferences';
import Dropdown from '@/components/dropdown';
import {useMenuTransition} from '@/hooks/use_menu_transition';
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

// The RHS header title: Back to the chat history, the active agent (which opens the agent list
// when there is more than one), and at the end the tools button on a new chat, or the context
// usage ring and New chat everywhere else.
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

    // The tools button's data: the same per-user preferences and server list the RHS body used
    // to load; a toggle here is published to the composer's tools menu too.
    const toolPrefs = useComposerToolPreferences();
    const [preloadedServers, setPreloadedServers] = useState<UserMCPServerInfo[]>([]);
    useEffect(() => {
        getUserMCPTools().then((response) => setPreloadedServers(response.servers)).catch(() => null);
    }, []);

    // Switching agents behaves like starting a new chat with the new agent (leave the open
    // thread and show the new-chat tab); choosing the current agent changes nothing.
    const switchAgent = useCallback((bot: LLMBot | null) => {
        if (!bot || bot.id === activeBot?.id) {
            return;
        }
        setActiveBot(bot);
        dispatch({type: 'SELECT_AI_POST', postId: ''});
        dispatch({type: 'AI_RHS_NEW_CHAT'});
    }, [activeBot?.id, setActiveBot, dispatch]);
    const [open, setOpen] = useState(false);
    const menu = useMenuTransition(open);

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
                <Button
                    emphasis='tertiary'
                    size='x-small'
                    leadingIcon={<Icon glyph={<PencilOutlineIcon/>}/>}
                    data-testid='new-chat'
                    className='new-button'
                    onClick={() => {
                        dispatch({type: 'SELECT_AI_POST', postId: ''});
                        dispatch({type: 'AI_RHS_SET_TAB', tab: 'new'});
                    }}
                >
                    <FormattedMessage defaultMessage='New chat'/>
                </Button>
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

    if (bots.length === 1) {
        return (
            <Container
                data-testid='rhs-title'
                $hasBack={Boolean(back)}
            >
                {back}
                <AgentAvatar bot={activeBot}/>
                <Name title={activeBot.displayName}>{activeBot.displayName}</Name>
                {end}
            </Container>
        );
    }

    const label = intl.formatMessage({defaultMessage: '{name}, choose agent'}, {name: activeBot.displayName});
    return (
        <Container
            data-testid='rhs-title'
            $hasBack={Boolean(back)}
        >
            {back}
            <AgentAvatar bot={activeBot}/>
            <Dropdown
                isOpen={menu.rendered}
                onOpenChange={setOpen}
                placement='bottom-start'
                target={(
                    <TitleButton
                        type='button'
                        aria-label={label}
                        title={activeBot.displayName}
                        aria-haspopup='menu'
                        aria-expanded={open}
                        data-testid='bot-selector-rhs'
                        onClick={() => setOpen(!open)}
                    >
                        <Name>{activeBot.displayName}</Name>
                        <Icon
                            glyph={<ChevronDownIcon/>}
                            size='16'
                        />
                    </TitleButton>
                )}
            >
                <BotDropdownMenu
                    open={open}
                    onExited={menu.onExited}
                    role='menu'
                    aria-label={label}
                    onClick={(e: React.MouseEvent) => {
                        e.stopPropagation();
                        setOpen(false);
                    }}
                >
                    <BotMenuContent
                        bots={bots}
                        activeBot={activeBot}
                        setActiveBot={switchAgent}
                        itemRole='menuitem'
                    />
                </BotDropdownMenu>
            </Dropdown>
            {end}
        </Container>
    );
};

// The Back button sits flush with the header's start; without it the title is inset.
// min-width: 0 all the way down lets a long name ellipsize instead of pushing the
// host's expand, popout and close buttons out of the header.
const Container = styled.span<{$hasBack: boolean}>`
    display: flex;
    flex: 1 1 auto;
    min-width: 0;
    align-items: center;
    gap: var(--spacing-xs);
    margin-left: ${({$hasBack}) => ($hasBack ? '0' : 'var(--spacing-xs)')};
`;

const Actions = styled.span`
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    gap: var(--spacing-xxs);
    margin-inline-start: auto;
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
