// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useId, useMemo, useState} from 'react';
import {useIntl} from 'react-intl';
import AccountMultipleOutlineIcon from '@mattermost/compass-icons/components/account-multiple-outline';
import MessagePlusOutlineIcon from '@mattermost/compass-icons/components/message-plus-outline';
import MessageOutlineIcon from '@mattermost/compass-icons/components/message-outline';
import PencilOutlineIcon from '@mattermost/compass-icons/components/pencil-outline';
import SettingsOutlineIcon from '@mattermost/compass-icons/components/settings-outline';
import SyncIcon from '@mattermost/compass-icons/components/sync';

import {PopoverMenu, PopoverMenuDivider, PopoverMenuGroup, PopoverMenuScroll} from '@mattermost/compass-ui/components/popover-menu';

import type {LLMBot} from '@/bots';
import Dropdown from '@/components/dropdown';
import {handleMenuNavigation} from '@/components/menu/menu_keyboard';
import {AgentMenuItem, IconMenuItem} from '@/components/menu/menu_parts';
import Submenu from '@/components/menu/submenu';
import {useFocusMenuOnOpen} from '@/components/menu/use_focus_menu_on_open';
import {useAgentThreads} from '@/hooks/use_agent_threads';
import {useConversation} from '@/hooks/use_conversation';
import {useConversationIdForThread} from '@/hooks/use_conversation_id_for_thread';
import {useMenuTransition} from '@/hooks/use_menu_transition';
import {isValidId} from '@/utils/ids';

import {chatTitle} from './chat_title';
import {ChatActionsDialogs, useChatActions} from './use_chat_actions';

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

    // Offers a "Switch agent" submenu, which starts a new chat with the agent picked. Passed in a new
    // chat only: an open chat belongs to its agent.
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
    const chatActions = useChatActions();

    // This agent's DM chats, the same stored list the sidebar shows.
    const botList = useMemo(() => [bot], [bot]);
    const {threadsByBot} = useAgentThreads(botList);
    const chats = threadsByBot[bot.id] ?? [];

    // The conversation id comes from a bot post in the store; until one is there (a reply that
    // failed or has not loaded) the open chat's row in this agent's DM list carries it too.
    const openThread = rootId ? chats.find((t) => t.root_post_id === rootId) : null;
    const postConversationId = useConversationIdForThread(rootId);
    const conversationId = postConversationId || (isValidId(openThread?.id) ? openThread.id : '');
    const {conversation} = useConversation(conversationId);
    const currentTitle = openThread?.title ?? conversation?.title ?? '';

    useFocusMenuOnOpen(nameMenuOpen, nameMenuId);

    const hasChat = Boolean(rootId && conversationId);

    // An open chat is named by its title; the agent's name labels the new-chat state.
    const title = rootId ? chatTitle(intl, currentTitle) : '';
    const nameLabel = rootId ? title : bot.displayName;
    const nameMenuLabel = rootId ? intl.formatMessage({defaultMessage: '{title}, {name} options'}, {title, name: bot.displayName}) : intl.formatMessage({defaultMessage: '{name} options'}, {name: bot.displayName});
    const newChatLabel = intl.formatMessage({defaultMessage: 'New chat'});
    const chatsLabel = intl.formatMessage({defaultMessage: 'Recent chats'});
    const switchable = agents && agents.bots.length > 1 ? agents : null;
    const agentsLabel = intl.formatMessage({defaultMessage: 'Switch agent'});

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
                                    <Submenu
                                        label={agentsLabel}
                                        icon={<SyncIcon/>}
                                        menuLabel={agentsLabel}
                                        grow='down'
                                        data-testid='agent-chat-switch-agent'
                                        {...submenuProps('agents')}
                                    >
                                        <PopoverMenuScroll maxHeight={CHATS_LIST_MAX_HEIGHT}>
                                            <PopoverMenuGroup>
                                                {switchable.bots.map((other) => {
                                                    // Picking the current agent switches nothing.
                                                    const active = other.id === bot.id;
                                                    return (
                                                        <AgentMenuItem
                                                            key={other.id}
                                                            bot={other}
                                                            active={active}
                                                            trailingElement={active}
                                                            {...(active && {'aria-current': true})}
                                                            onClick={closing(() => {
                                                                if (!active) {
                                                                    switchable.onSwitch(other);
                                                                }
                                                            })}
                                                        />
                                                    );
                                                })}
                                            </PopoverMenuGroup>
                                        </PopoverMenuScroll>
                                    </Submenu>
                                )}
                                {chats.length > 0 && (
                                    <Submenu
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
                                                        <IconMenuItem
                                                            key={thread.id}
                                                            icon={<MessageOutlineIcon/>}
                                                            label={chatTitle(intl, thread.title)}
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
                                    </Submenu>
                                )}
                            </PopoverMenuGroup>
                            <PopoverMenuDivider/>
                        </>
                    )}
                    {rootId && (
                        <>
                            <PopoverMenuGroup>
                                <IconMenuItem
                                    icon={<MessagePlusOutlineIcon/>}
                                    label={newChatLabel}
                                    onClick={closing(onNewChat)}
                                />
                                <IconMenuItem
                                    icon={<PencilOutlineIcon/>}
                                    label={intl.formatMessage({defaultMessage: 'Rename chat'})}
                                    disabled={!hasChat}
                                    onClick={closing(() => chatActions.startRename({conversationId, title: currentTitle}))}
                                />
                            </PopoverMenuGroup>
                            <PopoverMenuDivider/>
                        </>
                    )}
                    <PopoverMenuGroup>
                        {onOpenSettings && (
                            <IconMenuItem
                                icon={<SettingsOutlineIcon/>}
                                label={intl.formatMessage({defaultMessage: 'Agent settings'})}
                                onClick={closing(onOpenSettings)}
                            />
                        )}
                        <IconMenuItem
                            icon={<AccountMultipleOutlineIcon/>}
                            label={intl.formatMessage({defaultMessage: 'View all agents'})}
                            onClick={closing(onViewAll)}
                        />
                    </PopoverMenuGroup>
                </PopoverMenu>
            </Dropdown>
            <ChatActionsDialogs actions={chatActions}/>
        </>
    );
};

export default AgentChatNameMenu;
