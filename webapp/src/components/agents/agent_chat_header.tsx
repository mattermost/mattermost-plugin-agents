// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';
import ArchiveOutlineIcon from '@mattermost/compass-icons/components/archive-outline';
import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import MessagePlusOutlineIcon from '@mattermost/compass-icons/components/message-plus-outline';
import PencilOutlineIcon from '@mattermost/compass-icons/components/pencil-outline';
import SettingsOutlineIcon from '@mattermost/compass-icons/components/settings-outline';

import {Button} from '@mattermost/compass-ui/components/button';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {MenuItem} from '@mattermost/compass-ui/components/menu-item';
import {PopoverMenu, PopoverMenuDivider, PopoverMenuGroup} from '@mattermost/compass-ui/components/popover-menu';

import {archiveConversation, getProfilePictureUrl, renameConversation} from '@/client';
import type {LLMBot} from '@/bots';
import ConfirmationDialog from '@/components/confirmation_dialog';
import Dropdown from '@/components/dropdown';
import {useConversation} from '@/hooks/use_conversation';
import {useConversationIdForThread} from '@/hooks/use_conversation_id_for_thread';
import {useMenuTransition} from '@/hooks/use_menu_transition';
import {requestAgentThreadsRefresh} from '@/websocket';

import AgentChatRenameDialog from './agent_chat_rename_dialog';
import ChannelHeader from './channel_header';

// Same layer as the sidebar's archive confirmation.
const DIALOG_Z_INDEX = 2000;

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
    const [menuOpen, setMenuOpen] = useState(false);
    const {rendered, onExited} = useMenuTransition(menuOpen);
    const [renameOpen, setRenameOpen] = useState(false);
    const [archiveOpen, setArchiveOpen] = useState(false);
    const [archiving, setArchiving] = useState(false);
    const [archiveFailed, setArchiveFailed] = useState(false);
    const [renamedTitle, setRenamedTitle] = useState<string | null>(null);

    // The conversation id is only known once a bot post (carrying it) is in the store.
    const conversationId = useConversationIdForThread(rootId);
    const {conversation} = useConversation(conversationId);
    const currentTitle = renamedTitle ?? conversation?.title ?? '';

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

    const menuLabel = intl.formatMessage({defaultMessage: '{name} options'}, {name: bot.displayName});

    return (
        <>
            <ChannelHeader
                ariaLabel={bot.displayName}
                name={bot.displayName}
                {...(hasChat && currentTitle.trim() ? {description: currentTitle} : {})}
                avatarSrc={getProfilePictureUrl(bot.id, bot.lastIconUpdate)}
                nameSlot={(
                    <Dropdown
                        isOpen={rendered}
                        onOpenChange={setMenuOpen}
                        placement='bottom-start'
                        target={(
                            <NameButton
                                emphasis='quaternary'
                                size='small'
                                trailingIcon={<Icon glyph={<ChevronDownIcon/>}/>}
                                aria-haspopup='menu'
                                aria-expanded={menuOpen}
                                data-testid='agent-chat-menu-button'
                                onClick={() => setMenuOpen(!menuOpen)}
                            >
                                {bot.displayName}
                            </NameButton>
                        )}
                    >
                        <PopoverMenu
                            open={menuOpen}
                            onExited={onExited}
                            role='menu'
                            aria-label={menuLabel}
                            onClick={(e: React.MouseEvent) => {
                                e.stopPropagation();
                                setMenuOpen(false);
                            }}
                        >
                            <PopoverMenuGroup>
                                <ChatMenuItem
                                    icon={<MessagePlusOutlineIcon/>}
                                    label={intl.formatMessage({defaultMessage: 'New chat'})}
                                    onClick={onNewChat}
                                />
                                {onOpenSettings && (
                                    <ChatMenuItem
                                        icon={<SettingsOutlineIcon/>}
                                        label={intl.formatMessage({defaultMessage: 'Agent settings'})}
                                        onClick={onOpenSettings}
                                    />
                                )}
                            </PopoverMenuGroup>
                            {hasChat && (
                                <>
                                    <PopoverMenuDivider/>
                                    <PopoverMenuGroup>
                                        <ChatMenuItem
                                            icon={<PencilOutlineIcon/>}
                                            label={intl.formatMessage({defaultMessage: 'Rename chat'})}
                                            onClick={() => setRenameOpen(true)}
                                        />
                                        <ChatMenuItem
                                            icon={<ArchiveOutlineIcon/>}
                                            label={intl.formatMessage({defaultMessage: 'Archive chat'})}
                                            destructive={true}
                                            onClick={() => {
                                                setArchiveFailed(false);
                                                setArchiveOpen(true);
                                            }}
                                        />
                                    </PopoverMenuGroup>
                                </>
                            )}
                        </PopoverMenu>
                    </Dropdown>
                )}
            />
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

// Same as ChannelHeader's own name button (which cannot carry the menu wiring):
// a compass quaternary Button in the heading's type.
const NameButton = styled(Button)`
    && {
        min-width: 0;
        padding-inline: var(--spacing-xxs);
        font: inherit;
        color: inherit;
    }
`;

const ErrorText = styled.p`
    margin: 0;
    color: var(--error-text);
`;

export default AgentChatHeader;
