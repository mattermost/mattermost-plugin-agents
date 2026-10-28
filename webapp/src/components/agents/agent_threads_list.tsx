// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';
import DotsHorizontalIcon from '@mattermost/compass-icons/components/dots-horizontal';
import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import ChevronRightIcon from '@mattermost/compass-icons/components/chevron-right';
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
import {WaveDots} from '@/components/llmbot_post/thinking_dots';
import {prefersReducedMotion} from '@/components/llmbot_post/motion';
import {useMenuTransition} from '@/hooks/use_menu_transition';
import type {LLMBot} from '@/bots';
import type {AgentThreadsByBot} from '@/hooks/use_agent_threads';
import type {AIThread} from '@/types/ai_thread';

import AgentChatRenameDialog from './agent_chat_rename_dialog';
import ChatActionsMenuItems from './chat_actions_menu_items';
import MessageOutlineIcon from './message_outline_icon';

interface AgentThreadsListProps {
    bots: LLMBot[];
    threadsByBot: AgentThreadsByBot;
    loading: boolean;
    error: Error | null;
    activeBotId?: string;
    activeRootId?: string;

    // Root post ids of the chats whose agent is answering right now; a chat that is not open shows it.
    generatingRoots?: ReadonlySet<string>;

    // Root post ids of the chats whose agent finished answering while they were not open.
    unreadRoots?: ReadonlySet<string>;
    onSelectThread: (botId: string, rootId: string) => void;
    onNewChat: (botId: string) => void;
    onRetry: () => void;

    // Archives one chat. Rejects when it fails (the dialog then shows the error).
    onArchiveThread: (thread: AIThread) => Promise<void>;

    // Renames one chat. Rejects when it fails (the dialog then shows the error).
    onRenameThread: (thread: AIThread, title: string) => Promise<void>;
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
    generatingRoots,
    unreadRoots,
    onSelectThread,
    onNewChat,
    onRetry,
    onArchiveThread,
    onRenameThread,
}: AgentThreadsListProps) => {
    const intl = useIntl();
    const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});
    const [limits, setLimits] = useState<Record<string, number>>({});
    const [archiveTarget, setArchiveTarget] = useState<AIThread | null>(null);
    const [renameTarget, setRenameTarget] = useState<AIThread | null>(null);
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

                const toggleLabel = isCollapsed ? intl.formatMessage({defaultMessage: 'Expand {agent} chats'}, {agent: bot.displayName}) : intl.formatMessage({defaultMessage: 'Collapse {agent} chats'}, {agent: bot.displayName});
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
                            {threads.length > 0 && (
                                <ChevronSlot>
                                    <ChevronToggle
                                        type='button'
                                        aria-label={toggleLabel}
                                        aria-expanded={!isCollapsed}
                                        aria-controls={listId}
                                        onClick={() => toggle(bot.id)}
                                    >
                                        <Icon
                                            glyph={isCollapsed ? <ChevronRightIcon/> : <ChevronDownIcon/>}
                                            size='12'
                                        />
                                    </ChevronToggle>
                                </ChevronSlot>
                            )}
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
                        {threads.length > 0 && (
                            <ThreadCollapse $collapsed={isCollapsed}>
                                <ThreadCollapseInner>
                                    <ThreadList
                                        id={listId}
                                        aria-label={intl.formatMessage({defaultMessage: '{agent} chats'}, {agent: bot.displayName})}
                                    >
                                        {shown.map((thread) => (
                                            <ThreadItem
                                                key={thread.id}
                                                thread={thread}
                                                active={isActiveAgent && activeRootId === thread.root_post_id}
                                                generating={Boolean(thread.root_post_id && generatingRoots?.has(thread.root_post_id))}
                                                unread={Boolean(thread.root_post_id && unreadRoots?.has(thread.root_post_id))}
                                                placeholder={newChatPlaceholder}
                                                onSelect={() => onSelectThread(bot.id, thread.root_post_id as string)}
                                                onRename={() => setRenameTarget(thread)}
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
                                </ThreadCollapseInner>
                            </ThreadCollapse>
                        )}
                    </AgentBlock>
                );
            })}
            <AgentChatRenameDialog
                show={renameTarget !== null}
                initialTitle={renameTarget?.title ?? ''}
                onSubmit={async (title) => {
                    if (renameTarget) {
                        await onRenameThread(renameTarget, title);
                    }
                    if (mounted.current) {
                        setRenameTarget(null);
                    }
                }}
                onClose={() => setRenameTarget(null)}
            />
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

    // The agent is answering in this chat.
    generating: boolean;

    // The agent finished answering while the chat was not open.
    unread: boolean;
    placeholder: string;
    onSelect: () => void;
    onRename: () => void;
    onArchive: () => void;
}

// A compass ChannelSidebarItem only renders its own kebab for channel and DM rows, so the
// actions button is overlaid on the row (as the agent rows do for their chevron and [+]),
// with the same hover/focus reveal as the compass kebab.
const ThreadItem = ({thread, active, generating, unread, placeholder, onSelect, onRename, onArchive}: ThreadItemProps) => {
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
            <ThreadIcon
                aria-hidden={true}
                $active={active}
                $busy={generating}
            >
                {generating && (
                    <span data-testid='thread-generating'>
                        <WaveDots animate={!prefersReducedMotion()}/>
                    </span>
                )}
                {!generating && unread && (
                    <span data-testid='thread-unread'>
                        <UnreadDot/>
                    </span>
                )}
                {!generating && !unread && (
                    <Icon
                        size='12'
                        glyph={<MessageOutlineIcon/>}
                    />
                )}
            </ThreadIcon>
            {generating && (
                <VisuallyHidden>{intl.formatMessage({defaultMessage: 'The agent is responding'})}</VisuallyHidden>
            )}
            <ChannelSidebarItem
                name={title}
                leadingVisual='threads'
                status={unread && !generating ? 'unread' : 'read'}
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
                        <ChatActionsMenuItems
                            onRename={onRename}
                            onArchive={onArchive}
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

// The channel sidebar category header from compass-design (compass-proto ChannelsSidebar
// `channels-sidebar__category`): 32px tall, 12px semibold uppercase body font (Open Sans) with
// 0.02em tracking, sidebar text at 75%. compass-ui does not ship it yet, so it is composed here.
const SectionHeader = styled.div`
    display: flex;
    align-items: center;
    height: var(--spacing-xxxl);
    padding: 0 var(--spacing-l);
`;

// && keeps the type over any host heading rule (the host styles h3 for its own pages).
const SectionLabel = styled.h3`
    && {
        flex: 1 1 auto;
        min-width: 0;
        margin: 0;
        overflow: hidden;
        font-family: var(--font-family-body);
        font-size: var(--font-size-75);
        font-weight: var(--font-weight-semibold);
        letter-spacing: 0.02em;
        line-height: var(--line-height-75);
        text-overflow: ellipsis;
        text-transform: uppercase;
        white-space: nowrap;
        color: var(--sidebar-text);
        opacity: 0.75;
    }
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

// Sits in the gutter left of the avatar (the avatar keeps the same leading position as the
// nav items above) and only shows on row hover or keyboard focus.
const ChevronSlot = styled.span`
    position: absolute;
    top: 0;
    bottom: 0;
    left: var(--spacing-xxs);
    z-index: 2;
    display: flex;
    align-items: center;
    opacity: 0;
    transition: opacity var(--duration-quick) var(--ease-transition);

    &:focus-within {
        opacity: 1;
    }

    @media (prefers-reduced-motion: reduce) {
        transition: none;
    }
`;

// Just the chevron glyph (right when collapsed, down when expanded), with no button chrome.
// It is a bare native button, declared next to its handler for the a11y lint rules,
// because compass has no glyph-only toggle without the IconButton surface.
const ChevronToggle = styled.button`
    display: inline-flex;
    align-items: center;
    justify-content: center;
    margin: 0;
    padding: 0;
    border: 0;
    background: transparent;
    color: var(--sidebar-text);
    cursor: pointer;

    &:focus-visible {
        border-radius: var(--radius-s);
        outline: var(--spacing-xxxxs) solid var(--focus-ring-color);
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

    &:hover ${NewChatSlot},
    &:hover ${ChevronSlot},
    &:focus-within ${ChevronSlot} {
        opacity: 1;
    }
`;

const TooltipTrigger = styled.span`
    display: inline-flex;
`;

// Height animates by transitioning the grid row between 0fr and 1fr. The list stays mounted
// (so aria-controls resolves and the open chat is not remounted) and is taken out of the
// tab order and the accessibility tree with visibility once the collapse finishes.
const ThreadCollapse = styled.div<{$collapsed: boolean}>`
    display: grid;
    grid-template-rows: ${(p) => (p.$collapsed ? '0fr' : '1fr')};
    visibility: ${(p) => (p.$collapsed ? 'hidden' : 'visible')};
    transition:
        grid-template-rows var(--duration-moderate) var(--ease-transition),
        visibility 0s linear ${(p) => (p.$collapsed ? 'var(--duration-moderate)' : '0s')};

    @media (prefers-reduced-motion: reduce) {
        transition: none;
    }
`;

const ThreadCollapseInner = styled.div`
    min-height: 0;
    overflow: hidden;
`;

const ThreadList = styled.ul`
    display: flex;
    flex-direction: column;
    margin: 0;
    padding: 0;
    list-style: none;
`;

// Thread rows are compass ChannelSidebarItems, like the agent rows above them, so height,
// type and hover/active states match. The item has no trailing slot for the actions button, so it
// is overlaid on the row and revealed like the compass kebab.
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

const VisuallyHidden = styled.span`
    position: absolute;
    width: 1px;
    height: 1px;
    margin: -1px;
    padding: 0;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
`;

// Over the item's own 16px icon slot (its padding plus the container's 6px offset), centered in it: a 12px
// icon (drawn 14px) is 2px in. The icon column is as wide as the agent row's avatar column, so the
// chat's label sits at the same left edge as the agent's name.
const ThreadIcon = styled.span<{$active: boolean; $busy: boolean}>`
    position: absolute;
    top: 0;
    bottom: 0;
    left: 0;
    z-index: 1;
    display: flex;
    align-items: center;
    padding-left: calc(var(--spacing-l) + var(--spacing-xxxxs) + var(--spacing-xxxs) + var(--spacing-xxxxs));
    color: var(--sidebar-text);
    opacity: ${(p) => (p.$active || p.$busy ? 1 : 0.64)};
    pointer-events: none;

    /* The wave and the dot are narrower than the 16px slot, so center them in it. */
    > span {
        display: inline-flex;
        justify-content: center;
        width: var(--spacing-l);
    }
`;

// The agent finished while the chat was not open: the host's unread/mention color, in the icon slot.
const UnreadDot = styled.span`
    && {
        width: var(--spacing-xs);
        height: var(--spacing-xs);
        border-radius: var(--radius-pill);
        background: var(--mention-bg);
    }
`;

const ThreadListItem = styled.li`
    position: relative;

    /* The item's built-in icon cannot be swapped (no custom-icon slot), so it is hidden and the
       smaller chat icon is overlaid in its place (as the nav rows above do). */
    [class*='icon-container'] > svg {
        visibility: hidden;
    }

    &:hover ${ThreadActionsSlot},
    &:focus-within ${ThreadActionsSlot},
    &[data-menu-open] ${ThreadActionsSlot} {
        opacity: 1;
    }
`;

// The button's text lines up with the chat labels above it: they start 46px in (18px item padding, the
// 24px icon column and 4px of gaps), and the x-small button has 8px of padding of its own.
const MoreRow = styled.div`
    display: flex;
    padding: var(--spacing-xxxxs) var(--spacing-l) var(--spacing-xxxxs) calc(var(--spacing-xxxxl) - var(--spacing-xxxxs));
`;

const ArchiveError = styled.p`
    margin: var(--spacing-xs) 0 0;
    color: var(--error-text);
`;

export default AgentThreadsList;
