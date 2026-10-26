// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';
import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import MessageTextOutlineIcon from '@mattermost/compass-icons/components/message-text-outline';
import PlusIcon from '@mattermost/compass-icons/components/plus';
import {WithTooltip} from '@mattermost/shared/components/tooltip';
import {Button} from '@mattermost/compass-ui/components/button';
import {ChannelSidebarItem} from '@mattermost/compass-ui/components/channel-sidebar-item';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';

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
}

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
}: AgentThreadsListProps) => {
    const intl = useIntl();
    const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});

    const toggle = (botId: string) => setCollapsed((prev) => ({...prev, [botId]: !prev[botId]}));

    const sectionTitle = intl.formatMessage({defaultMessage: 'Your agents'});

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
                const isCollapsed = Boolean(collapsed[bot.id]) && threads.length > 0;
                const isActiveAgent = activeBotId === bot.id;
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
                                {threads.map((thread) => (
                                    <li key={thread.id}>
                                        <ThreadRow
                                            thread={thread}
                                            active={isActiveAgent && activeRootId === thread.root_post_id}
                                            placeholder={intl.formatMessage({defaultMessage: 'New chat'})}
                                            onSelect={() => onSelectThread(bot.id, thread.root_post_id as string)}
                                        />
                                    </li>
                                ))}
                            </ThreadList>
                        )}
                    </AgentBlock>
                );
            })}
        </Section>
    );
};

interface ThreadRowProps {
    thread: AIThread;
    active: boolean;
    placeholder: string;
    onSelect: () => void;
}

// Titles are generated asynchronously, so a blank title falls back to the placeholder.
const ThreadRow = ({thread, active, placeholder, onSelect}: ThreadRowProps) => {
    const title = thread.title.trim() || placeholder;
    return (
        <ThreadButton
            type='button'
            {...(active && {'aria-current': 'page' as const})}
            onClick={onSelect}
        >
            <ThreadIcon aria-hidden={true}>
                <Icon
                    size='16'
                    glyph={<MessageTextOutlineIcon/>}
                />
            </ThreadIcon>
            <ThreadLabel>{title}</ThreadLabel>
        </ThreadButton>
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
    opacity: 0;
    transition: opacity var(--duration-quick) var(--ease-transition);

    &:focus-within {
        opacity: 1;
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

    [class*='icon-container'] > * {
        visibility: hidden;
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

// No compass equivalent for a nested text-only list row, so this is a bare button
// (as in the prototype). It is declared here, next to its handler, for the a11y lint rules.
const ThreadButton = styled.button`
    position: relative;
    display: flex;
    align-items: center;
    gap: var(--spacing-xxxs);
    width: 100%;
    margin: 0;
    padding: var(--spacing-xxxs) var(--spacing-l) var(--spacing-xxxs) calc(var(--spacing-xxxl) + var(--spacing-xs));
    border: 0;
    border-radius: 0;
    background: transparent;
    color: var(--sidebar-text);
    font-family: var(--font-family-body);
    text-align: left;
    cursor: pointer;

    &::after {
        content: '';
        position: absolute;
        inset: 0;
        background: currentColor;
        opacity: 0;
        pointer-events: none;
    }

    &:hover::after,
    &[aria-current='page']::after {
        opacity: 0.08;
    }

    &[aria-current='page']::before {
        content: '';
        position: absolute;
        top: 0;
        bottom: 0;
        left: 0;
        width: var(--spacing-xxxxs);
        background: var(--sidebar-text-active-border);
    }

    &:focus-visible {
        outline: var(--spacing-xxxxs) solid var(--sidebar-text);
        outline-offset: calc(-1 * var(--spacing-xxxxs));
    }
`;

const ThreadIcon = styled.span`
    display: inline-flex;
    flex-shrink: 0;
    align-items: center;
    justify-content: center;
    opacity: 0.72;
`;

const ThreadLabel = styled.span`
    flex: 1 1 auto;
    min-width: 0;
    overflow: hidden;
    font-size: var(--font-size-100);
    font-weight: var(--font-weight-regular);
    line-height: var(--line-height-100);
    text-overflow: ellipsis;
    white-space: nowrap;
`;

export default AgentThreadsList;
