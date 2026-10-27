// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';
import DotsHorizontalIcon from '@mattermost/compass-icons/components/dots-horizontal';

import {Tag} from '@mattermost/compass-ui/components/tag';
import {UserAvatar} from '@mattermost/compass-ui/components/user-avatar';

import ChatActionsMenuItems from '@/components/agents/chat_actions_menu_items';
import DotMenu from '@/components/dot_menu';
import {Timestamp} from '@/mm_webapp';

// No compass token for the translucent hover fill (a lighter one than the Agents chat header's name button).
const HOVER_PERCENT = '4%';

// Avatars overlap by a quarter of their width, as in the host's Threads list.
const AVATAR_OVERLAP = 'calc(-1 * var(--spacing-xxs))';

type Props = {

    // The chat's first message; falls back to the chat title while it loads or if it is unavailable.
    preview: string;
    authorName: string;
    authorAvatarUrl: string;
    agentName: string;
    agentAvatarUrl: string;
    lastActivityDate: number;

    // Absent until the root post is loaded.
    replyCount?: number;
    onClick: () => void;

    // The hover actions' menu, the same as the Agents sidebar's chat rows: rename and archive
    // (the list asks for a new title and confirms the archive).
    onRename: () => void;
    onArchive: () => void;
}

// A chat in the history, laid out like an item of the host's Threads list: author, agent tag and
// time above the first message, then the participants and the reply count.
export default function ThreadItem(props: Props) {
    const intl = useIntl();
    const handleKeyDown = (e: React.KeyboardEvent) => {
        if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            props.onClick();
        }
    };
    const preview = props.preview.trim() || intl.formatMessage({defaultMessage: 'Conversation with Agents'});
    const actionsLabel = intl.formatMessage({defaultMessage: 'Chat actions'});
    return (
        <ThreadItemContainer>
            <Content
                role='button'
                tabIndex={0}
                aria-label={`${props.authorName}, ${props.agentName}: ${preview}`}
                onClick={props.onClick}
                onKeyDown={handleKeyDown}
            >
                <Heading>
                    <Author>{props.authorName}</Author>
                    <Tag
                        label={props.agentName}
                        type='default'
                        size='x-small'
                    />
                    <LastActivityDate>
                        <Timestamp // Matches the timestap format in the threads view
                            value={props.lastActivityDate}
                            units={['now', 'minute', 'hour', 'day', 'week']}
                            useTime={false}
                            day={'numeric'}
                        />
                    </LastActivityDate>
                </Heading>
                <Preview>{preview}</Preview>
                <Footer>
                    <Avatars aria-hidden='true'>
                        <UserAvatar
                            src={props.authorAvatarUrl}
                            alt=''
                            name={props.authorName}
                            size='24'
                        />
                        <OverlappedAvatar>
                            <UserAvatar
                                src={props.agentAvatarUrl}
                                alt=''
                                name={props.agentName}
                                size='24'
                            />
                        </OverlappedAvatar>
                    </Avatars>
                    {typeof props.replyCount === 'number' && (
                        <Replies>
                            {intl.formatMessage({defaultMessage: '{count, plural, one {# reply} other {# replies}}'}, {count: props.replyCount})}
                        </Replies>
                    )}
                </Footer>
            </Content>
            <Actions>
                <DotMenu
                    icon={<DotsHorizontalIcon/>}
                    title={actionsLabel}
                    size='x-small'
                    placement='bottom-end'
                    testId='thread-item-actions'
                >
                    <ChatActionsMenuItems
                        onRename={props.onRename}
                        onArchive={props.onArchive}
                    />
                </DotMenu>
            </Actions>
        </ThreadItemContainer>
    );
}

// Inset from the list's edges, with rounded corners. The divider is a pseudo-element, so it can be
// inset inside the item and disappear while the item is hovered. The actions sit over the item's
// top right, as a sibling of the clickable content (their events must not select the chat).
const ThreadItemContainer = styled.div`
    position: relative;
    margin: 0 var(--spacing-xs);
    border-radius: var(--radius-m);
    transition: background var(--duration-quick) var(--ease-transition);

    &::after {
        position: absolute;
        right: var(--spacing-l);
        bottom: 0;
        left: var(--spacing-l);
        border-bottom: var(--border-default);
        content: '';
        pointer-events: none;
        transition: opacity var(--duration-quick) var(--ease-transition);
    }

    &:hover,
    &:focus-within {
        background: color-mix(in srgb, var(--center-channel-color) ${HOVER_PERCENT}, transparent);
    }

    &:hover::after,
    &:focus-within::after {
        opacity: 0;
    }

    @media (prefers-reduced-motion: reduce) {
        transition: none;

        &::after {
            transition: none;
        }
    }
`;

const Content = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs);
    padding: var(--spacing-m) var(--spacing-l);
    border-radius: var(--radius-m);
    cursor: pointer;

    &:focus-visible {
        outline: var(--spacing-xxxxs) solid var(--focus-ring-color);
        outline-offset: calc(-1 * var(--spacing-xxxxs));
    }
`;

// Hover (or focus, or an open menu) reveals the actions button over the time, in a small raised
// pad like the host Threads list's item actions.
const Actions = styled.div`
    position: absolute;
    top: var(--spacing-xs);
    right: var(--spacing-s);
    z-index: 1;
    display: flex;
    border: var(--border-default);
    border-radius: var(--radius-s);
    background: var(--center-channel-bg);
    box-shadow: var(--elevation-1);
    opacity: 0;
    transition: opacity var(--duration-quick) var(--ease-transition);

    ${ThreadItemContainer}:hover &,
    ${ThreadItemContainer}:focus-within & {
        opacity: 1;
    }

    @media (prefers-reduced-motion: reduce) {
        transition: none;
    }
`;

const Heading = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-xs);
    min-width: 0;
`;

const Author = styled.span`
    min-width: 0;
    overflow: hidden;
    color: var(--center-channel-color);
    font-size: var(--font-size-100);
    font-weight: var(--font-weight-semibold);
    line-height: var(--line-height-100);
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const LastActivityDate = styled.span`
    flex: 0 0 auto;
    margin-inline-start: auto;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    font-size: var(--font-size-75);
    line-height: var(--line-height-75);
    white-space: nowrap;
`;

// Two lines of the first message, as in the host's Threads list.
const Preview = styled.div`
    display: -webkit-box;
    overflow: hidden;
    color: var(--center-channel-color);
    font-size: var(--font-size-100);
    line-height: var(--line-height-100);
    overflow-wrap: anywhere;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
`;

const Footer = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-xs);
`;

const Avatars = styled.span`
    display: inline-flex;
    align-items: center;
`;

const OverlappedAvatar = styled.span`
    display: inline-flex;
    margin-inline-start: ${AVATAR_OVERLAP};
`;

const Replies = styled.span`
    color: rgba(var(--center-channel-color-rgb), 0.64);
    font-size: var(--font-size-75);
    line-height: var(--line-height-75);
`;
