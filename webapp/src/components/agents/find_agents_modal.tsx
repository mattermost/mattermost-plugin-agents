// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useMemo, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';
import MessageTextOutlineIcon from '@mattermost/compass-icons/components/message-text-outline';
import {Button} from '@mattermost/compass-ui/components/button';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {MenuItem} from '@mattermost/compass-ui/components/menu-item';
import {Modal} from '@mattermost/compass-ui/components/modal';
import {Scrollbar} from '@mattermost/compass-ui/components/scrollbar';
import {SearchInput} from '@mattermost/compass-ui/components/search-input';
import {UserAvatar} from '@mattermost/compass-ui/components/user-avatar';

import {useBotlist, type LLMBot} from '@/bots';
import {getProfilePictureUrl} from '@/client';
import {AnimatedModalShell, MODAL_SHEET_CLASS} from '@/components/animated_modal_shell';
import {useAgentThreads} from '@/hooks/use_agent_threads';

import {recentAgents, searchAgents, type ChatResult} from './find_agents_search';
import {useAgentsRoute} from './use_agents_route';

const LISTBOX_ID = 'find-agents-listbox';
const optionId = (index: number) => `find-agents-option-${index}`;
const headingId = (key: string) => `find-agents-heading-${key}`;

type ResultItem =
    | {kind: 'agent'; key: string; bot: LLMBot}
    | {kind: 'chat'; key: string; bot: LLMBot; chat: ChatResult};

interface Section {
    key: 'recent' | 'agents' | 'chats';
    items: ResultItem[];
}

interface Props {
    show: boolean;
    onClose: () => void;
}

// The shell unmounts its children after the exit animation, so every open
// mounts a fresh Content: empty query, first row selected, threads refetched.
const FindAgentsModal = ({show, onClose}: Props) => {
    const intl = useIntl();
    return (
        <AnimatedModalShell
            show={show}
            onBackdropClick={onClose}
            zIndex={2000}
        >
            <Sheet className={MODAL_SHEET_CLASS}>
                <FindModal
                    closeOnEscape={false}
                    size='small'
                    title={intl.formatMessage({defaultMessage: 'Find agents'})}
                    subtitle={intl.formatMessage(
                        {defaultMessage: 'Type to find an agent or chat. Use <b>UP/DOWN</b> to browse, <b>ENTER</b> to select, <b>ESC</b> to dismiss.'},
                        {b: (chunks) => <b>{chunks}</b>},
                    )}
                    onClose={onClose}
                    closeLabel={intl.formatMessage({defaultMessage: 'Close'})}
                    closeButtonProps={{title: intl.formatMessage({defaultMessage: 'Close'})}}
                    headerDivider={false}
                    bodyPadding='menu'
                    scrollable={false}
                >
                    <Content onClose={onClose}/>
                </FindModal>
            </Sheet>
        </AnimatedModalShell>
    );
};

const Content = ({onClose}: {onClose: () => void}) => {
    const intl = useIntl();
    const {bots} = useBotlist();
    const {threadsByBot, loading, error, refresh} = useAgentThreads(bots);
    const {toChat} = useAgentsRoute();

    const [query, setQuery] = useState('');
    const [selected, setSelected] = useState(0);
    const inputRef = useRef<HTMLInputElement>(null);
    const listRef = useRef<HTMLDivElement>(null);

    // The shell focuses the dialog while it enters; claim focus for the input afterwards.
    useEffect(() => {
        const frame = window.requestAnimationFrame(() => inputRef.current?.focus());
        return () => window.cancelAnimationFrame(frame);
    }, []);

    const placeholderTitle = intl.formatMessage({defaultMessage: 'New chat'});
    const searching = query.trim() !== '';

    const sections = useMemo<Section[]>(() => {
        const allBots = bots ?? [];
        if (!searching) {
            return [{
                key: 'recent',
                items: recentAgents(allBots, threadsByBot).map((bot) => ({kind: 'agent', key: `agent-${bot.id}`, bot})),
            }];
        }
        const results = searchAgents(allBots, threadsByBot, query, placeholderTitle);
        return [
            {key: 'agents', items: results.agents.map((bot): ResultItem => ({kind: 'agent', key: `agent-${bot.id}`, bot}))},
            {key: 'chats', items: results.chats.map((chat): ResultItem => ({kind: 'chat', key: `chat-${chat.thread.id}`, bot: chat.bot, chat}))},
        ].filter((section): section is Section => section.items.length > 0);
    }, [bots, threadsByBot, query, searching, placeholderTitle]);

    const flat = useMemo(() => sections.flatMap((section) => section.items), [sections]);
    const activeIndex = flat.length === 0 ? -1 : Math.min(selected, flat.length - 1);

    useEffect(() => {
        if (activeIndex < 0) {
            return;
        }
        listRef.current?.querySelector<HTMLElement>(`#${optionId(activeIndex)}`)?.scrollIntoView?.({block: 'nearest'});
    }, [activeIndex]);

    const choose = (item: ResultItem) => {
        if (item.kind === 'chat') {
            toChat(item.bot.id, item.chat.thread.root_post_id as string);
        } else {
            // Same target as clicking the agent in the sidebar.
            const latest = threadsByBot[item.bot.id]?.[0]?.root_post_id;
            if (latest) {
                toChat(item.bot.id, latest);
            } else {
                toChat(item.bot.id);
            }
        }
        onClose();
    };

    const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
        if (flat.length === 0) {
            return;
        }
        switch (e.key) {
        case 'ArrowDown':
            e.preventDefault();
            setSelected((activeIndex + 1) % flat.length);
            break;
        case 'ArrowUp':
            e.preventDefault();
            setSelected(activeIndex === 0 ? flat.length - 1 : activeIndex - 1);
            break;
        case 'Enter':
            e.preventDefault();
            choose(flat[activeIndex]);
            break;
        }
    };

    const onQueryChange = (value: string) => {
        setQuery(value);
        setSelected(0);
    };

    const showError = Boolean(error) && !loading;
    const waiting = bots === null || (loading && !error);

    const sectionLabel = (key: Section['key']) => {
        switch (key) {
        case 'recent':
            return intl.formatMessage({defaultMessage: 'Recent'});
        case 'agents':
            return intl.formatMessage({defaultMessage: 'Agents'});
        default:
            return intl.formatMessage({defaultMessage: 'Chats'});
        }
    };

    let offset = 0;

    return (
        <Body>
            <SearchWrapper>
                <SearchInput
                    ref={inputRef}
                    size='medium'
                    role='combobox'
                    aria-label={intl.formatMessage({defaultMessage: 'Find agents'})}
                    aria-expanded={true}
                    aria-haspopup='listbox'
                    aria-autocomplete='list'
                    aria-controls={LISTBOX_ID}
                    {...(activeIndex >= 0 && {'aria-activedescendant': optionId(activeIndex)})}
                    placeholder={intl.formatMessage({defaultMessage: 'Find agents'})}
                    autoComplete='off'
                    spellCheck={false}
                    maxLength={64}
                    value={query}
                    onChange={(e: React.ChangeEvent<HTMLInputElement>) => onQueryChange(e.target.value)}
                    onClear={() => onQueryChange('')}
                    onKeyDown={onKeyDown}
                />
            </SearchWrapper>
            {showError && (
                <StatusMessage role='alert'>
                    <span>{intl.formatMessage({defaultMessage: 'Couldn\'t load your chats. Showing agents only.'})}</span>
                    <Button
                        emphasis='link'
                        size='x-small'
                        onClick={refresh}
                    >
                        {intl.formatMessage({defaultMessage: 'Try again'})}
                    </Button>
                </StatusMessage>
            )}
            {waiting && (
                <StatusMessage
                    role='status'
                    aria-live='polite'
                >
                    {intl.formatMessage({defaultMessage: 'Loading…'})}
                </StatusMessage>
            )}
            <Results ref={listRef}>
                <ScrollArea>
                    {flat.length > 0 && (
                        <Listbox
                            id={LISTBOX_ID}
                            role='listbox'
                            aria-label={intl.formatMessage({defaultMessage: 'Agents and chats'})}
                        >
                            {sections.map((section) => {
                                const start = offset;
                                offset += section.items.length;
                                return (
                                    <li
                                        key={section.key}
                                        role='presentation'
                                    >
                                        <SectionHeading id={headingId(section.key)}>
                                            {sectionLabel(section.key)}
                                        </SectionHeading>
                                        <Group
                                            role='group'
                                            aria-labelledby={headingId(section.key)}
                                        >
                                            {section.items.map((item, i) => {
                                                const index = start + i;
                                                return (
                                                    <li
                                                        key={item.key}
                                                        role='presentation'
                                                    >
                                                        <MenuItem
                                                            id={optionId(index)}
                                                            role='option'
                                                            aria-selected={index === activeIndex}
                                                            active={index === activeIndex}
                                                            label={item.kind === 'chat' ? item.chat.title : item.bot.displayName}
                                                            secondaryLabel={item.kind === 'chat' ? item.bot.displayName : (item.bot.username && `@${item.bot.username}`)}
                                                            secondaryLabelPosition='inline'
                                                            leadingVisual={(
                                                                <AvatarSlot aria-hidden={true}>
                                                                    <UserAvatar
                                                                        src={getProfilePictureUrl(item.bot.id, item.bot.lastIconUpdate)}
                                                                        alt=''
                                                                        name={item.bot.displayName}
                                                                        size='24'
                                                                    />
                                                                    {item.kind === 'chat' && (
                                                                        <ChatBadge>
                                                                            <Icon
                                                                                size='12'
                                                                                glyph={<MessageTextOutlineIcon/>}
                                                                            />
                                                                        </ChatBadge>
                                                                    )}
                                                                </AvatarSlot>
                                                            )}
                                                            onClick={() => choose(item)}
                                                            onMouseMove={() => {
                                                                if (index !== activeIndex) {
                                                                    setSelected(index);
                                                                }
                                                            }}
                                                        />
                                                    </li>
                                                );
                                            })}
                                        </Group>
                                    </li>
                                );
                            })}
                        </Listbox>
                    )}
                    {flat.length === 0 && !waiting && !showError && (
                        <NoResults role='status'>
                            {searching ? (
                                <>
                                    <NoResultsTitle>
                                        {intl.formatMessage({defaultMessage: 'No results for "{query}"'}, {query: query.trim()})}
                                    </NoResultsTitle>
                                    {intl.formatMessage({defaultMessage: 'Check the spelling or try another search.'})}
                                </>
                            ) : intl.formatMessage({defaultMessage: 'No agents available.'})}
                        </NoResults>
                    )}
                </ScrollArea>
            </Results>
        </Body>
    );
};

const Sheet = styled.div`
    display: flex;
    max-width: calc(100vw - var(--spacing-xxxl));
`;

// Fixed height so filtering never resizes the dialog, as in the host switcher.
const FindModal = styled(Modal)`
    && {
        height: 60vh;
        max-width: 100%;
    }
`;

const Body = styled.div`
    display: flex;
    flex-direction: column;
    height: 100%;
    min-height: 0;
`;

const SearchWrapper = styled.div`
    flex: 0 0 auto;
    padding: var(--spacing-xxxs) var(--spacing-xs) var(--spacing-xs);
`;

const StatusMessage = styled.div`
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--spacing-xxxs);
    padding: var(--spacing-xxxs) var(--spacing-l);
    font-size: var(--font-size-75);
    line-height: var(--line-height-75);
    color: var(--center-channel-color);
`;

const Results = styled.div`
    position: relative;
    flex: 1 1 auto;
    min-height: 0;
`;

// Fills the results pane and scrolls inside it.
const ScrollArea = styled(Scrollbar)`
    && {
        position: absolute;
        inset: 0;
        width: auto;
        height: auto;
    }
`;

const Listbox = styled.ul`
    margin: 0;
    padding: 0;
    list-style: none;
`;

const Group = styled.ul`
    margin: 0;
    padding: 0;
    list-style: none;
`;

const SectionHeading = styled.h3`
    margin: 0;
    padding: var(--spacing-xs) var(--spacing-l) var(--spacing-xxxs);
    font-family: var(--font-family-body);
    font-size: var(--font-size-75);
    font-weight: var(--font-weight-semibold);
    line-height: var(--line-height-75);
    text-transform: uppercase;
    color: var(--center-channel-color);
    opacity: 0.64;
`;

const AvatarSlot = styled.span`
    position: relative;
    display: inline-flex;
    flex-shrink: 0;
`;

// Marks a chat row (vs an agent row) on the agent's avatar.
const ChatBadge = styled.span`
    position: absolute;
    right: calc(-1 * var(--spacing-xxxs));
    bottom: calc(-1 * var(--spacing-xxxs));
    display: inline-flex;
    border-radius: var(--radius-pill);
    background: var(--center-channel-bg);
    color: var(--center-channel-color);
`;

const NoResults = styled.div`
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--spacing-xxxs);
    padding: var(--spacing-xxxl) var(--spacing-l);
    font-size: var(--font-size-100);
    line-height: var(--line-height-100);
    text-align: center;
    color: var(--center-channel-color);
`;

const NoResultsTitle = styled.span`
    font-size: var(--font-size-200);
    font-weight: var(--font-weight-semibold);
    line-height: var(--line-height-200);
`;

export default FindAgentsModal;
