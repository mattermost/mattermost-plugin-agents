// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useMemo, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';
import AccountMultipleOutlineIcon from '@mattermost/compass-icons/components/account-multiple-outline';
import ArchiveOutlineIcon from '@mattermost/compass-icons/components/archive-outline';
import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import MessagePlusOutlineIcon from '@mattermost/compass-icons/components/message-plus-outline';
import MessageTextOutlineIcon from '@mattermost/compass-icons/components/message-text-outline';
import PlusIcon from '@mattermost/compass-icons/components/plus';
import PencilOutlineIcon from '@mattermost/compass-icons/components/pencil-outline';
import SettingsOutlineIcon from '@mattermost/compass-icons/components/settings-outline';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {MenuGroupHeading} from '@mattermost/compass-ui/components/menu-group-heading';
import {MenuItem} from '@mattermost/compass-ui/components/menu-item';
import {PopoverMenu, PopoverMenuDivider, PopoverMenuGroup, PopoverMenuScroll} from '@mattermost/compass-ui/components/popover-menu';
import {UserAvatar} from '@mattermost/compass-ui/components/user-avatar';

import {archiveConversation, getProfilePictureUrl, getUserMCPTools, getUserToolPreferences, renameConversation} from '@/client';
import type {LLMBot} from '@/bots';
import ConfirmationDialog from '@/components/confirmation_dialog';
import Dropdown from '@/components/dropdown';
import ToolProviderPopover, {type UserMCPServerInfo} from '@/components/rhs/tool_provider_popover';
import {useAgentThreads} from '@/hooks/use_agent_threads';
import {useConversation} from '@/hooks/use_conversation';
import {useConversationIdForThread} from '@/hooks/use_conversation_id_for_thread';
import {useMenuTransition} from '@/hooks/use_menu_transition';
import {isValidId} from '@/utils/ids';
import {onAgentThreadsRefreshRequested, requestAgentThreadsRefresh, subscribeToAllPostUpdates} from '@/websocket';

import AgentChatRenameDialog from './agent_chat_rename_dialog';
import {useAgentsRoute} from './use_agents_route';

// Same layer as the sidebar's archive confirmation.
const DIALOG_Z_INDEX = 2000;

// No compass token for these two: the prototype's chat list max-height (200px) and its
// translucent hover fill (rgba(center-channel-color-rgb, 0.08)).
const CHATS_LIST_MAX_HEIGHT = 200;
const HOVER_PERCENT = '8%';

// Caps the name button so a long chat title never pushes the tools button and the new-chat button
// off the bar. No compass token for a width of this size.
const TITLE_MAX_WIDTH = 'min(100%, 40rem)';

interface AgentChatHeaderProps {
    bot: LLMBot;
    rootId?: string;

    // Present only when the current user may edit this agent.
    onOpenSettings?: () => void;
    onNewChat: () => void;

    // Called after the open chat was archived.
    onArchived: () => void;
}

// The agent name opens the chat menu, as the prototype's header title button does.
const AgentChatHeader = ({bot, rootId, onOpenSettings, onNewChat, onArchived}: AgentChatHeaderProps) => {
    const intl = useIntl();
    const {toChat, toAll} = useAgentsRoute();
    const [nameMenuOpen, setNameMenuOpen] = useState(false);
    const nameMenu = useMenuTransition(nameMenuOpen);
    const [renameOpen, setRenameOpen] = useState(false);
    const [archiveOpen, setArchiveOpen] = useState(false);
    const [archiving, setArchiving] = useState(false);
    const [archiveFailed, setArchiveFailed] = useState(false);
    const [renamedTitle, setRenamedTitle] = useState<string | null>(null);

    // This agent's DM chats, the same data the sidebar lists.
    const botList = useMemo(() => [bot], [bot]);
    const {threadsByBot, refresh, refreshWithTitleRetry} = useAgentThreads(botList);
    const chats = threadsByBot[bot.id] ?? [];
    useEffect(() => subscribeToAllPostUpdates((msg) => {
        if (msg.data.control === 'end') {
            refresh();
        }
    }), [refresh]);
    useEffect(() => onAgentThreadsRefreshRequested(refreshWithTitleRetry), [refreshWithTitleRetry]);

    // The conversation id comes from a bot post in the store; until one is there (a reply that
    // failed or has not loaded) the open chat's row in this agent's DM list carries it too.
    const openThread = rootId ? chats.find((t) => t.root_post_id === rootId) : null;
    const postConversationId = useConversationIdForThread(rootId);
    const conversationId = postConversationId || (isValidId(openThread?.id) ? openThread.id : '');
    const {conversation} = useConversation(conversationId);
    const currentTitle = renamedTitle ?? conversation?.title ?? '';

    // Tool providers: fed exactly as the RHS feeds its popover.
    const [disabledServers, setDisabledServers] = useState<string[]>([]);
    const [preloadedServers, setPreloadedServers] = useState<UserMCPServerInfo[]>([]);
    useEffect(() => {
        getUserToolPreferences().then((prefs) => setDisabledServers(prefs.disabled_servers || [])).catch(() => null);
        getUserMCPTools().then((response) => setPreloadedServers(response.servers)).catch(() => null);
    }, []);

    const mounted = useRef(true);
    useEffect(() => {
        mounted.current = true;
        return () => {
            mounted.current = false;
        };
    }, []);

    useEffect(() => {
        setRenamedTitle(null);
    }, [conversationId]);

    const hasChat = Boolean(rootId && conversationId);

    const rename = async (title: string) => {
        await renameConversation(conversationId, title);
        if (!mounted.current) {
            return;
        }
        setRenamedTitle(title);
        setRenameOpen(false);
        requestAgentThreadsRefresh();
    };

    const confirmArchive = async () => {
        if (archiving || !conversationId) {
            return;
        }
        setArchiving(true);
        setArchiveFailed(false);
        try {
            await archiveConversation(conversationId);
            if (!mounted.current) {
                return;
            }
            setArchiveOpen(false);
            requestAgentThreadsRefresh();
            onArchived();
        } catch (e) {
            console.error('Failed to archive agent chat:', e); // eslint-disable-line no-console
            if (mounted.current) {
                setArchiveFailed(true);
            }
        } finally {
            if (mounted.current) {
                setArchiving(false);
            }
        }
    };

    // An open chat is named by its title; the agent's name labels the new-chat state.
    const chatTitle = rootId ? ((renamedTitle ?? conversation?.title ?? openThread?.title ?? '').trim() || intl.formatMessage({defaultMessage: 'New chat'})) : '';
    const nameLabel = rootId ? chatTitle : bot.displayName;
    const nameMenuLabel = rootId ? intl.formatMessage({defaultMessage: '{title}, {name} options'}, {title: chatTitle, name: bot.displayName}) : intl.formatMessage({defaultMessage: '{name} options'}, {name: bot.displayName});
    const newChatLabel = intl.formatMessage({defaultMessage: 'New chat'});
    const chatsLabel = intl.formatMessage({defaultMessage: 'Recent chats'});
    const closeOnItemClick = (close: () => void) => (e: React.MouseEvent) => {
        e.stopPropagation();
        close();
    };

    // Like the RHS: the tools button is for the new-chat state, and not for service
    // account agents (no per-user connections or preferences to manage).
    const showTools = !rootId && bot.useServiceAccountAuth !== true;

    return (
        <>
            <Root aria-label={bot.displayName}>
                <Title>
                    <UserAvatar
                        src={getProfilePictureUrl(bot.id, bot.lastIconUpdate)}
                        alt=''
                        aria-hidden='true'
                        name={bot.displayName}
                        size='24'
                    />
                    <Dropdown
                        isOpen={nameMenu.rendered}
                        onOpenChange={setNameMenuOpen}
                        placement='bottom-start'
                        target={(
                            <TitleButton
                                type='button'
                                aria-label={nameMenuLabel}
                                title={nameLabel}
                                aria-haspopup='menu'
                                aria-expanded={nameMenuOpen}
                                data-testid='agent-chat-menu-button'
                                onClick={() => setNameMenuOpen(!nameMenuOpen)}
                            >
                                <Name>{nameLabel}</Name>
                                <Icon
                                    glyph={<ChevronDownIcon/>}
                                    size='16'
                                />
                            </TitleButton>
                        )}
                    >
                        <PopoverMenu
                            open={nameMenuOpen}
                            onExited={nameMenu.onExited}
                            role='menu'
                            aria-label={nameMenuLabel}
                            onClick={closeOnItemClick(() => setNameMenuOpen(false))}
                        >
                            {chats.length > 0 && (
                                <>
                                    <PopoverMenuScroll maxHeight={CHATS_LIST_MAX_HEIGHT}>
                                        <PopoverMenuGroup aria-label={chatsLabel}>
                                            <MenuGroupHeading
                                                aria-hidden={true}
                                                label={chatsLabel}
                                            />
                                            {chats.map((thread) => {
                                                const active = thread.root_post_id === rootId;
                                                return (
                                                    <ChatMenuItem
                                                        key={thread.id}
                                                        icon={<MessageTextOutlineIcon/>}
                                                        label={thread.title.trim() || newChatLabel}
                                                        active={active}
                                                        trailingElement={active}
                                                        {...(active && {'aria-current': true})}
                                                        onClick={() => {
                                                            if (!active && thread.root_post_id) {
                                                                toChat(bot.id, thread.root_post_id);
                                                            }
                                                        }}
                                                    />
                                                );
                                            })}
                                        </PopoverMenuGroup>
                                    </PopoverMenuScroll>
                                    <PopoverMenuDivider/>
                                </>
                            )}
                            {rootId && (
                                <>
                                    <PopoverMenuGroup>
                                        <ChatMenuItem
                                            icon={<MessagePlusOutlineIcon/>}
                                            label={intl.formatMessage({defaultMessage: 'New chat'})}
                                            onClick={onNewChat}
                                        />
                                        <ChatMenuItem
                                            icon={<PencilOutlineIcon/>}
                                            label={intl.formatMessage({defaultMessage: 'Rename chat'})}
                                            disabled={!hasChat}
                                            onClick={() => setRenameOpen(true)}
                                        />
                                        <ChatMenuItem
                                            icon={<ArchiveOutlineIcon/>}
                                            label={intl.formatMessage({defaultMessage: 'Archive chat'})}
                                            destructive={true}
                                            disabled={!hasChat}
                                            onClick={() => {
                                                setArchiveFailed(false);
                                                setArchiveOpen(true);
                                            }}
                                        />
                                    </PopoverMenuGroup>
                                    <PopoverMenuDivider/>
                                </>
                            )}
                            <PopoverMenuGroup>
                                {onOpenSettings && (
                                    <ChatMenuItem
                                        icon={<SettingsOutlineIcon/>}
                                        label={intl.formatMessage({defaultMessage: 'Agent settings'})}
                                        onClick={onOpenSettings}
                                    />
                                )}
                                <ChatMenuItem
                                    icon={<AccountMultipleOutlineIcon/>}
                                    label={intl.formatMessage({defaultMessage: 'View all agents'})}
                                    onClick={toAll}
                                />
                            </PopoverMenuGroup>
                        </PopoverMenu>
                    </Dropdown>
                </Title>
                <Actions>
                    {showTools && (
                        <ToolProviderPopover
                            disabledServers={disabledServers}
                            onDisabledServersChange={setDisabledServers}
                            preloadedServers={preloadedServers}
                            enabledMCPTools={bot.enabledMCPTools}
                            autoEnableNewMCPTools={bot.autoEnableNewMCPTools}
                        />
                    )}
                    <IconButton
                        size='small'
                        padding='compact'
                        icon={<Icon glyph={<PlusIcon/>}/>}
                        aria-label={newChatLabel}
                        title={rootId ? newChatLabel : intl.formatMessage({defaultMessage: 'You\'re already in a new chat'})}
                        disabled={!rootId}
                        data-testid='agent-chat-new-chat-button'
                        onClick={onNewChat}
                    />
                </Actions>
            </Root>
            <AgentChatRenameDialog
                show={renameOpen}
                initialTitle={currentTitle}
                onSubmit={rename}
                onClose={() => setRenameOpen(false)}
            />
            <ConfirmationDialog
                show={archiveOpen}
                titleId='agent-chat-archive-dialog-title'
                title={intl.formatMessage({defaultMessage: 'Archive chat'})}
                message={(
                    <>
                        <p>
                            {intl.formatMessage({defaultMessage: 'Archive this chat? It will be removed from your chats and can no longer be opened from here.'})}
                        </p>
                        {archiveFailed && (
                            <ErrorText role='alert'>
                                {intl.formatMessage({defaultMessage: 'Couldn\'t archive this chat. Try again.'})}
                            </ErrorText>
                        )}
                    </>
                )}
                confirmButtonText={intl.formatMessage({defaultMessage: 'Archive'})}
                onConfirm={confirmArchive}
                onCancel={() => {
                    if (!archiving) {
                        setArchiveOpen(false);
                    }
                }}
                isDestructive={true}
                confirmPending={archiving}
                managedAccessibility={true}
                zIndex={DIALOG_Z_INDEX}
            />
        </>
    );
};

// The prototype's MenuItem usage: menu semantics and a 16px leading icon.
const ChatMenuItem = ({icon, ...props}: Omit<React.ComponentProps<typeof MenuItem>, 'leadingVisual'> & {icon: React.ReactNode}) => (
    <MenuItem
        role='menuitem'
        leadingElement={true}
        leadingVisual={(
            <Icon
                glyph={icon}
                size='16'
            />
        )}
        {...props}
    />
);

// The prototype's agent-chat header: 56px bar (spacing-m padding around a 32px row).
const Root = styled.header`
    /* Above the new-chat body, whose lift (translateY) can overlap this bar and block its clicks. */
    position: relative;
    z-index: 1;
    display: flex;
    box-sizing: border-box;
    flex: 0 0 auto;
    align-items: center;
    justify-content: space-between;
    gap: var(--spacing-l);
    min-height: calc(var(--spacing-xxxl) + 2 * var(--spacing-m));
    padding: var(--spacing-m);
    background: var(--center-channel-bg);
    border-bottom: var(--border-default);
`;

const Title = styled.div`
    display: flex;
    flex: 1 1 auto;
    min-width: 0;
    align-items: center;
    gap: var(--spacing-xs);
    padding-inline: var(--spacing-xs);
`;

const TitleButton = styled.button`
    display: inline-flex;
    max-width: ${TITLE_MAX_WIDTH};
    align-items: center;
    min-width: 0;
    margin: 0;
    padding: 0 var(--spacing-xxxs) 0 var(--spacing-xxs);
    border: none;
    border-radius: var(--radius-s);
    background: transparent;
    color: var(--center-channel-color);
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
`;

const Name = styled.h1`
    min-width: 0;
    margin: 0;
    overflow: hidden;
    font-family: var(--font-family-heading);
    font-size: var(--font-size-200);
    font-weight: var(--font-weight-semibold);
    line-height: var(--line-height-200);
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--center-channel-color);
`;

const Actions = styled.div`
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    gap: var(--spacing-xxs);
`;

const ErrorText = styled.p`
    margin: 0;
    color: var(--error-text);
`;

export default AgentChatHeader;
