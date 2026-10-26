// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';
import ArchiveOutlineIcon from '@mattermost/compass-icons/components/archive-outline';
import DotsHorizontalIcon from '@mattermost/compass-icons/components/dots-horizontal';
import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import PlusIcon from '@mattermost/compass-icons/components/plus';
import {WithTooltip} from '@mattermost/shared/components/tooltip';
import {Button} from '@mattermost/compass-ui/components/button';
import {ChannelSidebarItem} from '@mattermost/compass-ui/components/channel-sidebar-item';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {PopoverMenu} from '@mattermost/compass-ui/components/popover-menu';

import {getProfilePictureUrl} from '@/client';
import ConfirmationDialog from '@/components/confirmation_dialog';
import Dropdown from '@/components/dropdown';
import {DropdownMenuItem} from '@/components/dot_menu';
import {useMenuTransition} from '@/hooks/use_menu_transition';
import type {LLMBot} from '@/bots';
import type {AgentThreadsByBot} from '@/hooks/use_agent_threads';
import type {AIThread} from '@/types/ai_thread';

interface AgentThreadsListProps {
    bots: LLMBot[];
    threadsByBot: AgentThreadsByBot;
    loading: boolean;
    error: Error | null;
    activeBotId?: string;
    activeRootId?: string;
    onSelectThread: (botId: string, rootId: string) => void;
    onNewChat: (botId: string) => void;
    onRetry: () => void;

    // Archives one chat. Rejects when it fails (the dialog then shows the error).
    onArchiveThread: (thread: AIThread) => Promise<void>;
}

// Threads shown per agent before "More"; each click reveals this many more.
export const THREADS_PAGE_SIZE = 10;

// Above the plugin's other modals (2000), like every confirmation sheet on this page.
const ARCHIVE_DIALOG_Z_INDEX = 2000;

const AgentThreadsList = ({
    bots,
    threadsByBot,
    loading,
    error,
    activeBotId,
    activeRootId,
    onSelectThread,
    onNewChat,
    onRetry,
    onArchiveThread,
}: AgentThreadsListProps) => {
    const intl = useIntl();
    const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});
    const [limits, setLimits] = useState<Record<string, number>>({});
    const [archiveTarget, setArchiveTarget] = useState<AIThread | null>(null);
    const [archiving, setArchiving] = useState(false);
    const [archiveFailed, setArchiveFailed] = useState(false);

    // Keeps the title visible while the dialog's exit animation runs.
    const lastArchiveTitle = useRef('');
    const pendingFocus = useRef<{botId: string; index: number} | null>(null);
    const mounted = useRef(true);
    useEffect(() => {
        mounted.current = true;
        return () => {
            mounted.current = false;
        };
    }, []);

    // When "More" reveals the last threads its button disappears; move focus to the
    // first revealed row instead of dropping it to the document body.
    useEffect(() => {
        const target = pendingFocus.current;
        if (!target) {
            return;
        }
        pendingFocus.current = null;
        const list = document.getElementById(`agent-threads-${target.botId}`);
        if (!list || list.querySelector('[data-more-threads]')) {
            return;
        }
        list.querySelectorAll<HTMLElement>('[data-thread-item]')[target.index]?.querySelector('button')?.focus();
    });

    const toggle = (botId: string) => {
        setCollapsed((prev) => ({...prev, [botId]: !prev[botId]}));

        // Collapsing starts the agent over at one page.
        setLimits((prev) => Object.fromEntries(Object.entries(prev).filter(([id]) => id !== botId)));
    };

    const closeArchive = () => {
        if (!archiving) {
            setArchiveTarget(null);
        }
    };

    const confirmArchive = async () => {
        if (!archiveTarget || archiving) {
            return;
        }
        setArchiving(true);
        setArchiveFailed(false);
        try {
            await onArchiveThread(archiveTarget);
            if (mounted.current) {
                setArchiveTarget(null);
            }
        } catch {
            if (mounted.current) {
                setArchiveFailed(true);
            }
        } finally {
            if (mounted.current) {
                setArchiving(false);
            }
        }
    };

    if (archiveTarget) {
        lastArchiveTitle.current = archiveTarget.title.trim() || intl.formatMessage({defaultMessage: 'New chat'});
    }

    const sectionTitle = intl.formatMessage({defaultMessage: 'Your agents'});

    // Titles are generated asynchronously, so a blank title falls back to the placeholder.
    // The actions menu does not depend on the thread being loadable, so a "New chat" row
    // whose chat cannot be opened can still be archived.
    const newChatPlaceholder = intl.formatMessage({defaultMessage: 'New chat'});

    return (
        <Section aria-label={sectionTitle}>
            <SectionHeader>
                <SectionLabel>{sectionTitle}</SectionLabel>
            </SectionHeader>
            {error && (
                <StatusMessage role='alert'>
                    <span>{intl.formatMessage({defaultMessage: 'Couldn\'t load your chats.'})}</span>
                    <Button
                        emphasis='link'
                        appearance='inverted'
                        size='x-small'
                        onClick={onRetry}
                    >
                        {intl.formatMessage({defaultMessage: 'Try again'})}
                    </Button>
                </StatusMessage>
            )}
            {loading && !error && (
                <StatusMessage
                    role='status'
                    aria-live='polite'
                >
                    {intl.formatMessage({defaultMessage: 'Loading chats…'})}
                </StatusMessage>
            )}
            {bots.map((bot) => {
                const threads = threadsByBot[bot.id] ?? [];
                const limit = limits[bot.id] ?? THREADS_PAGE_SIZE;
                const isActiveAgent = activeBotId === bot.id;

                // The open chat stays listed even when it is beyond the limit.
                const shown = threads.filter((t, i) => i < limit || (isActiveAgent && Boolean(activeRootId) && t.root_post_id === activeRootId));
                const hiddenCount = threads.length - shown.length;
                const isCollapsed = Boolean(collapsed[bot.id]) && threads.length > 0;
                const listId = `agent-threads-${bot.id}`;

                const openAgent = () => {
                    if (threads.length > 0) {
                        onSelectThread(bot.id, threads[0].root_post_id as string);
                    } else {
                        onNewChat(bot.id);
                    }
                };

                const toggleLabel = isCollapsed ?
                    intl.formatMessage({defaultMessage: 'Expand {agent} chats'}, {agent: bot.displayName}) :
                    intl.formatMessage({defaultMessage: 'Collapse {agent} chats'}, {agent: bot.displayName});
                const newChatLabel = intl.formatMessage({defaultMessage: 'New chat with {agent}'}, {agent: bot.displayName});

                return (
                    <AgentBlock key={bot.id}>
                        <AgentRow>
                            <ChannelSidebarItem
                                name={bot.displayName}
                                leadingVisual='direct-message'
                                avatarSrc={getProfilePictureUrl(bot.id, bot.lastIconUpdate)}
                                avatarAlt=''
                                active={isActiveAgent && (!activeRootId || isCollapsed)}
                                onClick={openAgent}
                            />
                            <ChevronSlot>
                                <IconButton
                                    size='x-small'
                                    // eslint-disable-next-line react/style-prop-object -- compass IconButton `style` is a variant prop, not CSS
                                    style='inverted'
                                    icon={<Icon glyph={<ChevronDownIcon/>}/>}
                                    aria-label={toggleLabel}
                                    aria-expanded={!isCollapsed}
                                    aria-controls={listId}
                                    disabled={threads.length === 0}
                                    onClick={() => toggle(bot.id)}
                                />
                            </ChevronSlot>
                            <NewChatSlot>
                                <WithTooltip title={newChatLabel}>
                                    <TooltipTrigger>
                                        <IconButton
                                            size='x-small'
                                            // eslint-disable-next-line react/style-prop-object -- compass IconButton `style` is a variant prop, not CSS
                                            style='inverted'
                                            icon={<Icon glyph={<PlusIcon/>}/>}
                                            aria-label={newChatLabel}
                                            onClick={() => onNewChat(bot.id)}
                                        />
                                    </TooltipTrigger>
                                </WithTooltip>
                            </NewChatSlot>
                        </AgentRow>
                        {!isCollapsed && threads.length > 0 && (
                            <ThreadList
                                id={listId}
                                aria-label={intl.formatMessage({defaultMessage: '{agent} chats'}, {agent: bot.displayName})}
                            >
                                {shown.map((thread) => (
                                    <ThreadItem
                                        key={thread.id}
                                        thread={thread}
                                        active={isActiveAgent && activeRootId === thread.root_post_id}
                                        placeholder={newChatPlaceholder}
                                        onSelect={() => onSelectThread(bot.id, thread.root_post_id as string)}
                                        onArchive={() => {
                                            setArchiveFailed(false);
                                            setArchiveTarget(thread);
                                        }}
                                    />
                                ))}
                                {hiddenCount > 0 && (
                                    <li>
                                        <MoreRow>
                                            <Button
                                                emphasis='quaternary'
                                                appearance='inverted'
                                                size='x-small'
                                                data-more-threads={true}
                                                aria-label={intl.formatMessage({defaultMessage: 'Show more {agent} chats'}, {agent: bot.displayName})}
                                                onClick={() => {
                                                    pendingFocus.current = {botId: bot.id, index: shown.length};
                                                    setLimits((prev) => ({...prev, [bot.id]: limit + THREADS_PAGE_SIZE}));
                                                }}
                                            >
                                                {intl.formatMessage({defaultMessage: 'More'})}
                                            </Button>
                                        </MoreRow>
                                    </li>
                                )}
                            </ThreadList>
                        )}
                    </AgentBlock>
                );
            })}
            <ConfirmationDialog
                show={archiveTarget !== null}
                titleId='archive-chat-dialog-title'
                title={intl.formatMessage({defaultMessage: 'Archive chat'})}
                message={(
                    <>
                        <p>
                            {intl.formatMessage(
                                {defaultMessage: 'Archive "{title}"? It will be removed from your chats and can no longer be opened from here.'},
                                {title: lastArchiveTitle.current},
                            )}
                        </p>
                        {archiveFailed && (
                            <ArchiveError role='alert'>
                                {intl.formatMessage({defaultMessage: 'Couldn\'t archive this chat. Try again.'})}
                            </ArchiveError>
                        )}
                    </>
                )}
                confirmButtonText={intl.formatMessage({defaultMessage: 'Archive'})}
                cancelButtonText={intl.formatMessage({defaultMessage: 'Cancel'})}
                onConfirm={confirmArchive}
                onCancel={closeArchive}
                isDestructive={true}
                confirmPending={archiving}
                managedAccessibility={true}
                zIndex={ARCHIVE_DIALOG_Z_INDEX}
            />
        </Section>
    );
};

interface ThreadItemProps {
    thread: AIThread;
    active: boolean;
    placeholder: string;
    onSelect: () => void;
    onArchive: () => void;
}

// A compass ChannelSidebarItem only renders its own kebab for channel and DM rows, so the
// actions button is overlaid on the row (as the agent rows do for their chevron and [+]),
// with the same hover/focus reveal as the compass kebab.
const ThreadItem = ({thread, active, placeholder, onSelect, onArchive}: ThreadItemProps) => {
    const intl = useIntl();
    const [menuOpen, setMenuOpen] = useState(false);
    const {rendered, onExited} = useMenuTransition(menuOpen);
    const title = thread.title.trim() || placeholder;
    const actionsLabel = intl.formatMessage({defaultMessage: 'Actions for {title}'}, {title});

    return (
        <ThreadListItem
            data-thread-item={true}
            {...(menuOpen && {'data-menu-open': true})}
        >
            <ChannelSidebarItem
                name={title}
                leadingVisual='threads'
                active={active}
                onClick={onSelect}
            />
            <ThreadActionsSlot>
                <Dropdown
                    isOpen={rendered}
                    onOpenChange={setMenuOpen}
                    placement='bottom-end'
                    tooltip={actionsLabel}
                    target={(
                        <IconButton
                            size='x-small'
                            // eslint-disable-next-line react/style-prop-object -- compass IconButton `style` is a variant prop, not CSS
                            style='inverted'
                            icon={<Icon glyph={<DotsHorizontalIcon/>}/>}
                            aria-label={actionsLabel}
                            aria-haspopup='menu'
                            aria-expanded={menuOpen}
                            active={menuOpen}
                            onClick={() => setMenuOpen(!menuOpen)}
                        />
                    )}
                >
                    <PopoverMenu
                        open={menuOpen}
                        onExited={onExited}
                        onClick={(e: React.MouseEvent) => {
                            e.stopPropagation();
                            setMenuOpen(false);
                        }}
                    >
                        <DropdownMenuItem
                            label={intl.formatMessage({defaultMessage: 'Archive chat'})}
                            icon={<ArchiveOutlineIcon/>}
                            onClick={onArchive}
                        />
                    </PopoverMenu>
                </Dropdown>
            </ThreadActionsSlot>
        </ThreadListItem>
    );
};

const Section = styled.section`
    display: flex;
    flex-direction: column;
    padding-bottom: var(--spacing-s);
`;

const SectionHeader = styled.div`
    display: flex;
    align-items: center;
    padding: var(--spacing-xxxs) var(--spacing-l);
`;

const SectionLabel = styled.h3`
    flex: 1 1 auto;
    min-width: 0;
    margin: 0;
    overflow: hidden;
    font-family: var(--font-family-body);
    font-size: var(--font-size-75);
    font-weight: var(--font-weight-semibold);
    line-height: var(--line-height-75);
    text-overflow: ellipsis;
    text-transform: uppercase;
    white-space: nowrap;
    color: var(--sidebar-text);
    opacity: 0.75;
`;

const StatusMessage = styled.div`
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--spacing-xxxs);
    padding: var(--spacing-xxxs) var(--spacing-l);
    font-size: var(--font-size-75);
    line-height: var(--line-height-75);
    color: var(--sidebar-text);
`;

const AgentBlock = styled.div`
    display: flex;
    flex-direction: column;
`;

const ChevronSlot = styled.span`
    position: absolute;
    top: 0;
    bottom: 0;
    left: var(--spacing-xs);
    z-index: 2;
    display: flex;
    align-items: center;

    svg {
        transition: transform var(--duration-quick) var(--ease-transition);
    }

    @media (prefers-reduced-motion: reduce) {
        svg {
            transition: none;
        }
    }

    button[aria-expanded='false'] svg {
        transform: rotate(-90deg);
    }
`;

const NewChatSlot = styled.span`
    position: absolute;
    top: 0;
    right: 0;
    bottom: 0;
    z-index: 2;
    display: flex;
    align-items: center;
    padding-right: var(--spacing-l);

    /* Visible at rest (dimmed) so it can be found without hovering the row. */
    opacity: 0.64;
    transition: opacity var(--duration-quick) var(--ease-transition);

    &:focus-within {
        opacity: 1;
    }

    @media (prefers-reduced-motion: reduce) {
        transition: none;
    }
`;

const AgentRow = styled.div`
    position: relative;

    /* ChannelSidebarItem's kebab and leading icon cannot be swapped (no trailing or
       custom-icon slot in the installed compass-ui), so hide them and overlay the
       chevron and new-chat buttons instead, as the prototype does. */
    [class*='menu-button'] {
        display: none;
    }

    /* Make room for the chevron before the avatar, in both the active and resting
       states of the item (which pad the control differently). */
    && [class*='__left'] {
        padding-left: var(--spacing-xxxl);
    }

    &:hover ${NewChatSlot} {
        opacity: 1;
    }
`;

const TooltipTrigger = styled.span`
    display: inline-flex;
`;

const ThreadList = styled.ul`
    display: flex;
    flex-direction: column;
    margin: 0;
    padding: 0;
    list-style: none;
`;

// Thread rows are compass ChannelSidebarItems, like the agent rows above them, so height,
// type and hover/active states match. Only the icon column is moved: the same left padding as
// the agent row (which clears its chevron) puts each icon under the agent's avatar, in both the
// resting and active states of the item (which pad the control differently).
const ThreadActionsSlot = styled.span`
    position: absolute;
    top: 0;
    right: var(--spacing-l);
    bottom: 0;
    z-index: 1;
    display: flex;
    align-items: center;
    opacity: 0;
    transition: opacity var(--duration-quick) var(--ease-transition);

    @media (prefers-reduced-motion: reduce) {
        transition: none;
    }
`;

const ThreadListItem = styled.li`
    position: relative;

    && [class*='__left'] {
        padding-left: var(--spacing-xxxl);
    }

    &:hover ${ThreadActionsSlot},
    &:focus-within ${ThreadActionsSlot},
    &[data-menu-open] ${ThreadActionsSlot} {
        opacity: 1;
    }
`;

const MoreRow = styled.div`
    display: flex;
    padding: var(--spacing-xxxxs) var(--spacing-l) var(--spacing-xxxxs) var(--spacing-xxxl);
`;

const ArchiveError = styled.p`
    margin: var(--spacing-xs) 0 0;
    color: var(--error-text);
`;

export default AgentThreadsList;
