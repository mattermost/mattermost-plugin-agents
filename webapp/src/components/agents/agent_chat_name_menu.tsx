// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useId, useMemo, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import AccountMultipleOutlineIcon from '@mattermost/compass-icons/components/account-multiple-outline';
import MessagePlusOutlineIcon from '@mattermost/compass-icons/components/message-plus-outline';
import MessageOutlineIcon from '@mattermost/compass-icons/components/message-outline';
import PencilOutlineIcon from '@mattermost/compass-icons/components/pencil-outline';
import SettingsOutlineIcon from '@mattermost/compass-icons/components/settings-outline';
import SyncIcon from '@mattermost/compass-icons/components/sync';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {MenuItem} from '@mattermost/compass-ui/components/menu-item';
import {PopoverMenu, PopoverMenuDivider, PopoverMenuGroup, PopoverMenuScroll} from '@mattermost/compass-ui/components/popover-menu';
import {UserAvatar} from '@mattermost/compass-ui/components/user-avatar';

import {getProfilePictureUrl, renameConversation} from '@/client';
import type {LLMBot} from '@/bots';
import ComposerSubmenu from '@/components/agent_chat/composer_submenu';
import {focusFirstItem, handleMenuNavigation} from '@/components/agent_chat/composer_menu_keyboard';
import Dropdown from '@/components/dropdown';
import {useAgentThreads} from '@/hooks/use_agent_threads';
import {useConversation} from '@/hooks/use_conversation';
import {useConversationIdForThread} from '@/hooks/use_conversation_id_for_thread';
import {useMenuTransition} from '@/hooks/use_menu_transition';
import {isValidId} from '@/utils/ids';
import {onAgentThreadsRefreshRequested, requestAgentThreadsRefresh, subscribeToAllPostUpdates} from '@/websocket';

import AgentChatRenameDialog from './agent_chat_rename_dialog';

// No compass token for the prototype's chat list max-height (200px).
const CHATS_LIST_MAX_HEIGHT = 200;

export interface NameMenuTrigger {
    ariaLabel: string;

    // The chat's title, or the agent's name in a new chat.
    label: string;
    expanded: boolean;
    onClick: () => void;
}

interface AgentChatNameMenuProps {
    bot: LLMBot;
    rootId?: string;

    // Present only when the current user may edit this agent.
    onOpenSettings?: () => void;
    onSelectChat: (rootId: string) => void;
    onNewChat: () => void;
    onViewAll: () => void;

    // Offers a submenu to pick an agent: "Switch agent" in a new chat, and "New chat" in an open chat
    // (a new chat with the agent picked, the open chat's own included). `onSwitch` starts a new chat
    // with that agent. The RHS passes it always (it has no agent list of its own); the product header
    // only in a new chat, since an open chat belongs to its agent.
    agents?: {bots: LLMBot[]; onSwitch: (bot: LLMBot) => void};

    // The surface draws its own title button; this menu opens from it.
    renderTrigger: (trigger: NameMenuTrigger) => React.JSX.Element;
}

type Submenu = {kind: 'chats' | 'agents'; focus: boolean};

// The agent chat's name menu: recent chats, the chat actions (new, rename), switching agents where
// offered, and the way to the agent pages. Shared by the Agents product header and the RHS title,
// with the rename dialog it opens.
const AgentChatNameMenu = ({bot, rootId, onOpenSettings, onSelectChat, onNewChat, onViewAll, agents, renderTrigger}: AgentChatNameMenuProps) => {
    const intl = useIntl();
    const [nameMenuOpen, setNameMenuOpen] = useState(false);
    const nameMenu = useMenuTransition(nameMenuOpen);
    const [submenu, setSubmenu] = useState<Submenu | null>(null);
    const nameMenuId = `agent-chat-menu-${useId().replace(/:/g, '')}`;
    const setMenuOpen = useCallback((next: boolean) => {
        setNameMenuOpen(next);
        if (!next) {
            setSubmenu(null);
        }
    }, []);
    const [renameOpen, setRenameOpen] = useState(false);
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
    const currentTitle = renamedTitle ?? openThread?.title ?? conversation?.title ?? '';

    // The Dropdown's focus manager moves focus in on mount; a reopen during the exit
    // transition keeps it mounted, so move focus here too.
    useEffect(() => {
        if (!nameMenuOpen) {
            return () => null;
        }
        const frame = requestAnimationFrame(() => {
            const el = document.getElementById(nameMenuId);
            if (el && !el.contains(document.activeElement)) {
                focusFirstItem(el);
            }
        });
        return () => cancelAnimationFrame(frame);
    }, [nameMenuOpen, nameMenuId]);

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

    // An open chat is named by its title; the agent's name labels the new-chat state.
    const chatTitle = rootId ? ((renamedTitle ?? openThread?.title ?? conversation?.title ?? '').trim() || intl.formatMessage({defaultMessage: 'New chat'})) : '';
    const nameLabel = rootId ? chatTitle : bot.displayName;
    const nameMenuLabel = rootId ? intl.formatMessage({defaultMessage: '{title}, {name} options'}, {title: chatTitle, name: bot.displayName}) : intl.formatMessage({defaultMessage: '{name} options'}, {name: bot.displayName});
    const newChatLabel = intl.formatMessage({defaultMessage: 'New chat'});
    const chatsLabel = intl.formatMessage({defaultMessage: 'Recent chats'});
    const switchable = agents && agents.bots.length > 1 ? agents : null;

    // In an open chat the agent picker is the New chat row itself, so the plain item is left out.
    const newChatPicker = Boolean(rootId && switchable);
    const agentsLabel = newChatPicker ? newChatLabel : intl.formatMessage({defaultMessage: 'Switch agent'});

    // Every item closes the whole menu; the submenu rows open their child instead.
    const closing = (action: () => void) => () => {
        setMenuOpen(false);
        action();
    };
    const submenuProps = (kind: Submenu['kind']) => ({
        open: submenu?.kind === kind,
        focusOnOpen: submenu?.kind === kind && submenu.focus,
        onOpen: (focus: boolean) => setSubmenu({kind, focus}),
        onClose: () => setSubmenu(null),
    });

    return (
        <>
            <Dropdown
                isOpen={nameMenu.rendered}
                onOpenChange={setMenuOpen}
                placement='bottom-start'
                target={renderTrigger({
                    ariaLabel: nameMenuLabel,
                    label: nameLabel,
                    expanded: nameMenuOpen,
                    onClick: () => setMenuOpen(!nameMenuOpen),
                })}
            >
                <PopoverMenu
                    id={nameMenuId}
                    open={nameMenuOpen}
                    onExited={nameMenu.onExited}
                    role='menu'
                    aria-label={nameMenuLabel}
                    onKeyDown={handleMenuNavigation}
                >
                    {(chats.length > 0 || switchable) && (
                        <>
                            <PopoverMenuGroup>
                                {switchable && (
                                    <ComposerSubmenu
                                        label={agentsLabel}
                                        icon={newChatPicker ? <MessagePlusOutlineIcon/> : <SyncIcon/>}
                                        menuLabel={agentsLabel}
                                        grow='down'
                                        data-testid='agent-chat-switch-agent'
                                        {...submenuProps('agents')}
                                    >
                                        <PopoverMenuScroll maxHeight={CHATS_LIST_MAX_HEIGHT}>
                                            <PopoverMenuGroup>
                                                {switchable.bots.map((other) => {
                                                    // Picking the current agent switches nothing, but still starts a new chat.
                                                    const active = !newChatPicker && other.id === bot.id;
                                                    return (
                                                        <MenuItem
                                                            key={other.id}
                                                            role='menuitem'
                                                            label={other.displayName}
                                                            leadingElement={true}
                                                            leadingVisual={(
                                                                <UserAvatar
                                                                    src={getProfilePictureUrl(other.id, other.lastIconUpdate)}
                                                                    alt=''
                                                                    aria-hidden='true'
                                                                    name={other.displayName}
                                                                    size='20'
                                                                />
                                                            )}
                                                            active={active}
                                                            trailingElement={active}
                                                            {...(active && {'aria-current': true})}
                                                            onClick={closing(() => {
                                                                if (newChatPicker || !active) {
                                                                    switchable.onSwitch(other);
                                                                }
                                                            })}
                                                        />
                                                    );
                                                })}
                                            </PopoverMenuGroup>
                                        </PopoverMenuScroll>
                                    </ComposerSubmenu>
                                )}
                                {chats.length > 0 && (
                                    <ComposerSubmenu
                                        label={chatsLabel}
                                        icon={<MessageOutlineIcon/>}
                                        menuLabel={chatsLabel}
                                        grow='down'
                                        data-testid='agent-chat-recent-chats'
                                        {...submenuProps('chats')}
                                    >
                                        <PopoverMenuScroll maxHeight={CHATS_LIST_MAX_HEIGHT}>
                                            <PopoverMenuGroup>
                                                {chats.map((thread) => {
                                                    const active = thread.root_post_id === rootId;
                                                    return (
                                                        <ChatMenuItem
                                                            key={thread.id}
                                                            icon={<MessageOutlineIcon/>}
                                                            label={thread.title.trim() || newChatLabel}
                                                            active={active}
                                                            trailingElement={active}
                                                            {...(active && {'aria-current': true})}
                                                            onClick={closing(() => {
                                                                if (!active && thread.root_post_id) {
                                                                    onSelectChat(thread.root_post_id);
                                                                }
                                                            })}
                                                        />
                                                    );
                                                })}
                                            </PopoverMenuGroup>
                                        </PopoverMenuScroll>
                                    </ComposerSubmenu>
                                )}
                            </PopoverMenuGroup>
                            <PopoverMenuDivider/>
                        </>
                    )}
                    {rootId && (
                        <>
                            <PopoverMenuGroup>
                                {!newChatPicker && (
                                    <ChatMenuItem
                                        icon={<MessagePlusOutlineIcon/>}
                                        label={newChatLabel}
                                        onClick={closing(onNewChat)}
                                    />
                                )}
                                <ChatMenuItem
                                    icon={<PencilOutlineIcon/>}
                                    label={intl.formatMessage({defaultMessage: 'Rename chat'})}
                                    disabled={!hasChat}
                                    onClick={closing(() => setRenameOpen(true))}
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
                                onClick={closing(onOpenSettings)}
                            />
                        )}
                        <ChatMenuItem
                            icon={<AccountMultipleOutlineIcon/>}
                            label={intl.formatMessage({defaultMessage: 'View all agents'})}
                            onClick={closing(onViewAll)}
                        />
                    </PopoverMenuGroup>
                </PopoverMenu>
            </Dropdown>
            <AgentChatRenameDialog
                show={renameOpen}
                initialTitle={currentTitle}
                onSubmit={rename}
                onClose={() => setRenameOpen(false)}
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

export default AgentChatNameMenu;
