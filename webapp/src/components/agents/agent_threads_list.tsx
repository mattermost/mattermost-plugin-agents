// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import styled, {css, keyframes} from 'styled-components';
import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import ChevronRightIcon from '@mattermost/compass-icons/components/chevron-right';
import MessageOutlineIcon from '@mattermost/compass-icons/components/message-outline';
import PlusIcon from '@mattermost/compass-icons/components/plus';
import {WithTooltip} from '@mattermost/shared/components/tooltip';
import {Button} from '@mattermost/compass-ui/components/button';
import {ChannelSidebarItem} from '@mattermost/compass-ui/components/channel-sidebar-item';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';

import {getProfilePictureUrl} from '@/client';
import {WaveDots} from '@/components/llmbot_post/thinking_dots';
import {prefersReducedMotion} from '@/components/llmbot_post/motion';
import type {LLMBot} from '@/bots';
import type {AgentThreadsByBot} from '@/hooks/use_agent_threads';
import type {AIThread} from '@/types/ai_thread';

import ChatRowActionsMenu from './chat_row_actions_menu';
import {chatTitle} from './chat_title';
import type {ChatActions} from './use_chat_actions';

interface AgentThreadsListProps {
    bots: LLMBot[];
    threadsByBot: AgentThreadsByBot;
    loading: boolean;
    error: boolean;
    activeBotId?: string;
    activeRootId?: string;

    // Root post ids of the chats whose agent is answering right now; a chat that is not open shows it.
    generatingRoots?: ReadonlySet<string>;

    // Root post ids of the chats with replies the user has not read (the host's thread read state).
    unreadRoots?: ReadonlySet<string>;
    onSelectThread: (botId: string, rootId: string) => void;
    onNewChat: (botId: string) => void;
    onRetry: () => void;

    // The chat rows' actions; the owner renders their dialogs.
    chatActions: ChatActions;

    // The section's heading; defaults to "Your agents".
    title?: string;
}

// Threads shown per agent before "Show more", which reveals the rest at once ("Show less" folds back).
export const THREADS_PAGE_SIZE = 7;

// The compass quick duration, in ms, for sequencing work after a CSS animation that uses it.
const quickDurationMs = () => {
    const ms = parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--duration-quick'));
    return Number.isFinite(ms) ? ms : 150;
};

// Rows past the first page only exist once "Show more" revealed them, so they enter; on "Show less" the
// ones that are about to go (not the open chat, which stays listed) fold away first.
const rowMotion = (index: number, rootId: string | null | undefined, limit: number, folding: boolean, activeRootId: string | undefined) => {
    if (limit <= THREADS_PAGE_SIZE || index < THREADS_PAGE_SIZE) {
        return {};
    }
    if (folding && !(activeRootId && rootId === activeRootId)) {
        return {motion: 'leave' as const};
    }
    return {motion: 'enter' as const};
};

// The dimmed strength of the sidebar's quiet affordances (icons, the row chevron).
const QUIET_OPACITY = 0.64;

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
    chatActions,
    title,
}: AgentThreadsListProps) => {
    const intl = useIntl();
    const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});
    const [limits, setLimits] = useState<Record<string, number>>({});

    // Agents whose revealed rows are folding away after "Show less"; the limit resets when that ends.
    const [folding, setFolding] = useState<Record<string, boolean>>({});

    // The pending end of each agent's fold. Any later change to that agent's list cancels it, so a
    // "Show more" right after "Show less" is not undone when the fold would have ended.
    const foldTimers = useRef<Record<string, number>>({});
    const cancelFold = (botId: string) => {
        const timer = foldTimers.current[botId];
        if (timer !== undefined) { // eslint-disable-line no-undefined
            window.clearTimeout(timer);
            delete foldTimers.current[botId];
        }
        setFolding((prev) => (prev[botId] ? Object.fromEntries(Object.entries(prev).filter(([id]) => id !== botId)) : prev));
    };
    useEffect(() => {
        const timers = foldTimers.current;
        return () => Object.values(timers).forEach((timer) => window.clearTimeout(timer));
    }, []);

    const resetLimit = (botId: string) => {
        setLimits((prev) => Object.fromEntries(Object.entries(prev).filter(([id]) => id !== botId)));
    };

    // "Show less" folds the extra rows away over the quick duration, then drops them.
    const foldBack = (botId: string) => {
        cancelFold(botId);
        const quick = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ? 0 : quickDurationMs();
        if (quick === 0) {
            resetLimit(botId);
            return;
        }
        setFolding((prev) => ({...prev, [botId]: true}));
        foldTimers.current[botId] = window.setTimeout(() => {
            delete foldTimers.current[botId];
            resetLimit(botId);
            cancelFold(botId);
        }, quick);
    };

    const showMore = (botId: string) => {
        cancelFold(botId);
        setLimits((prev) => ({...prev, [botId]: Infinity}));
    };

    const toggle = (botId: string) => {
        cancelFold(botId);
        setCollapsed((prev) => ({...prev, [botId]: !prev[botId]}));

        // Collapsing starts the agent over at one page.
        setLimits((prev) => Object.fromEntries(Object.entries(prev).filter(([id]) => id !== botId)));
    };

    const sectionTitle = title ?? intl.formatMessage({defaultMessage: 'Your agents'});

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

                // Once every chat is revealed the button stays at the end of the list as "Show less", which
                // folds the list back to one page. It is only offered where the user expanded a longer list.
                const canShowLess = hiddenCount === 0 && limit > THREADS_PAGE_SIZE && threads.length > THREADS_PAGE_SIZE;
                const isCollapsed = Boolean(collapsed[bot.id]) && threads.length > 0;
                const listId = `agent-threads-${bot.id}`;

                const openAgent = () => {
                    // Never land in a chat that is hidden in a collapsed list.
                    if (collapsed[bot.id]) {
                        setCollapsed((prev) => ({...prev, [bot.id]: false}));
                    }
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
                                        {shown.map((thread, index) => (
                                            <ThreadItem
                                                key={thread.id}
                                                thread={thread}
                                                {...rowMotion(index, thread.root_post_id, limit, Boolean(folding[bot.id]), isActiveAgent ? (activeRootId ?? '') : '')}
                                                active={isActiveAgent && activeRootId === thread.root_post_id}
                                                generating={Boolean(thread.root_post_id && generatingRoots?.has(thread.root_post_id))}
                                                unread={Boolean(thread.root_post_id && unreadRoots?.has(thread.root_post_id))}
                                                onSelect={() => onSelectThread(bot.id, thread.root_post_id as string)}
                                                chatActions={chatActions}
                                            />
                                        ))}
                                        {(hiddenCount > 0 || canShowLess) && (
                                            <li>
                                                <MoreRow>
                                                    <Button
                                                        emphasis='quaternary'
                                                        appearance='inverted'
                                                        size='x-small'
                                                        data-more-threads={true}
                                                        aria-label={canShowLess ? intl.formatMessage({defaultMessage: 'Show less {agent} chats'}, {agent: bot.displayName}) : intl.formatMessage({defaultMessage: 'Show more {agent} chats'}, {agent: bot.displayName})}
                                                        onClick={() => {
                                                            if (canShowLess) {
                                                                foldBack(bot.id);
                                                                return;
                                                            }
                                                            showMore(bot.id);
                                                        }}
                                                    >
                                                        {canShowLess ? intl.formatMessage({defaultMessage: 'Show less'}) : intl.formatMessage({defaultMessage: 'Show more'})}
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

    // The row was revealed by "Show more" (enter), or is being folded away by "Show less" (leave).
    motion?: 'enter' | 'leave';
    onSelect: () => void;
    chatActions: ChatActions;
}

// A compass ChannelSidebarItem only renders its own kebab for channel and DM rows, so the
// actions button is overlaid on the row (as the agent rows do for their chevron and [+]),
// with the same hover/focus reveal as the compass kebab.
const ThreadItem = ({thread, active, generating, unread, motion, onSelect, chatActions}: ThreadItemProps) => {
    const intl = useIntl();
    const [menuOpen, setMenuOpen] = useState(false);
    const title = chatTitle(intl, thread.title);
    const actionsLabel = intl.formatMessage({defaultMessage: 'Actions for {title}'}, {title});

    return (
        <ThreadListItem
            data-thread-item={true}
            $motion={motion}
            {...(menuOpen && {'data-menu-open': true})}
        >
            <ThreadRowBody $motion={motion}>
                <ThreadIcon
                    aria-hidden={true}
                    $active={active}
                    $emphasized={generating || unread}
                >
                    {generating && (
                        <span data-testid='thread-generating'>
                            <WaveDots
                                animate={!prefersReducedMotion()}
                                half={true}
                            />
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
                {!generating && unread && (
                    <VisuallyHidden>{intl.formatMessage({defaultMessage: 'The agent has replied'})}</VisuallyHidden>
                )}
                <ChannelSidebarItem
                    name={title}
                    leadingVisual='threads'
                    status={unread && !generating ? 'unread' : 'read'}
                    active={active}
                    onClick={onSelect}
                />
                <ThreadActionsSlot>
                    <ChatRowActionsMenu
                        chat={{conversationId: thread.id, title: thread.title}}
                        actions={chatActions}
                        label={actionsLabel}
                        appearance='sidebar'
                        onOpenChange={setMenuOpen}
                    />
                </ThreadActionsSlot>
            </ThreadRowBody>
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
    left: 0;
    z-index: 2;
    display: flex;
    align-items: center;
    opacity: 0;
    transition: opacity var(--duration-quick) var(--ease-transition);

    &:focus-within {
        opacity: ${QUIET_OPACITY};
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

    /* A 24px target around the 12px glyph, which stays where it was (6px in from the row's edge). */
    width: var(--spacing-xxl);
    height: var(--spacing-xxl);
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
    opacity: ${QUIET_OPACITY};
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

    &:hover ${NewChatSlot} {
        opacity: 1;
    }

    /* The chevron is a quiet affordance, at the same strength as the sidebar icons. */
    &:hover ${ChevronSlot},
    &:focus-within ${ChevronSlot} {
        opacity: ${QUIET_OPACITY};
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
// `$emphasized` draws the slot at full opacity instead of the dimmed resting strength (the wave and the unread dot).
const ThreadIcon = styled.span<{$active: boolean; $emphasized: boolean}>`
    position: absolute;
    top: 0;
    bottom: 0;
    left: 0;
    z-index: 1;
    display: flex;
    align-items: center;
    padding-left: calc(var(--spacing-l) + var(--spacing-xxxxs) + var(--spacing-xxxs) + var(--spacing-xxxxs));
    color: var(--sidebar-text);
    opacity: ${(p) => (p.$active || p.$emphasized ? 1 : QUIET_OPACITY)};
    pointer-events: none;

    /* The wave takes the icon's color (the sidebar text, at the icon's opacity). */
    --wave-dot-color: currentColor;

    /* The wave and the dot are narrower than the 16px slot, so they are centered in a 16px wrapper. The
       overlay's offset already includes the 12px icon's 2px centering, so the wrapper is pulled back by
       that 2px to put their center where the icon's and the avatar's is. */
    > span {
        display: inline-flex;
        justify-content: center;
        width: var(--spacing-l);
        margin-left: calc(-1 * var(--spacing-xxxxs));
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

// Rows revealed by "Show more" grow open and fade in; "Show less" folds them away the same way back.
// grid-template-rows 0fr/1fr animates the row's own height, whatever it is.
const rowReveal = keyframes`
    from {
        grid-template-rows: 0fr;
        opacity: 0;
    }

    to {
        grid-template-rows: 1fr;
        opacity: 1;
    }
`;

const rowFold = keyframes`
    from {
        grid-template-rows: 1fr;
        opacity: 1;
    }

    to {
        grid-template-rows: 0fr;
        opacity: 0;
    }
`;

// The row's content, which is what the grid row animation squeezes. It only clips while a row moves, so a
// focus ring or the open actions menu is not cut off at rest.
const ThreadRowBody = styled.div<{$motion?: 'enter' | 'leave'}>`
    position: relative;
    min-height: 0;
    ${({$motion}) => $motion && 'overflow: clip;'}
`;

const ThreadListItem = styled.li<{$motion?: 'enter' | 'leave'}>`
    position: relative;
    ${({$motion}) => $motion && css`
        display: grid;
        animation: ${$motion === 'enter' ? rowReveal : rowFold} var(--duration-quick) var(--ease-transition) both;

        @media (prefers-reduced-motion: reduce) {
            animation: none;
        }
    `}

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

export default AgentThreadsList;
