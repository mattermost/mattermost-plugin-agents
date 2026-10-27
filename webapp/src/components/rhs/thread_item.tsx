// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';
import DotsHorizontalIcon from '@mattermost/compass-icons/components/dots-horizontal';
import ReplyOutlineIcon from '@mattermost/compass-icons/components/reply-outline';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {UserAvatar} from '@mattermost/compass-ui/components/user-avatar';

import {Timestamp} from '@/mm_webapp';

// No compass token for the translucent hover fill (the Agents chat header's name button uses the same).
const HOVER_PERCENT = '8%';

// No compass token for the muted text strength of the meta line.
const META_COLOR = 'rgba(var(--center-channel-color-rgb), 0.64)';

type Props = {

    // The chat's title; the item's heading. Falls back to the first message, then a generic label.
    title: string;

    // The chat's first message; empty while it loads or if it is unavailable.
    preview: string;
    agentName: string;
    agentAvatarUrl: string;
    lastActivityDate: number;

    // Absent until the root post is loaded.
    replyCount?: number;
    onClick: () => void;

    // The item's actions button was used; the list opens the menu next to it.
    onMenuClick: (anchor: HTMLElement) => void;
}

// A chat in the history: the agent's avatar, then the chat's title, the start of its first message,
// and a line of the agent's name, the time and the number of replies. The actions button shows on
// hover and focus.
export default function ThreadItem(props: Props) {
    const intl = useIntl();
    const title = props.title.trim();
    const preview = props.preview.trim();
    const heading = title || preview || intl.formatMessage({defaultMessage: 'Conversation with Agents'});

    // The first message is the heading when there is no title, so it is not repeated below.
    const body = title ? preview : '';
    const replies = typeof props.replyCount === 'number' ? intl.formatMessage({defaultMessage: '{count, plural, one {# reply} other {# replies}}'}, {count: props.replyCount}) : '';
    const actionsLabel = intl.formatMessage({defaultMessage: 'Chat actions'});

    const handleKeyDown = (e: React.KeyboardEvent) => {
        if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            props.onClick();
        }
    };

    return (
        <Item>
            <Main
                role='button'
                tabIndex={0}
                aria-label={`${heading}, ${props.agentName}`}
                onClick={props.onClick}
                onKeyDown={handleKeyDown}
            >
                <UserAvatar
                    src={props.agentAvatarUrl}
                    alt=''
                    aria-hidden='true'
                    name={props.agentName}
                    size='32'
                />
                <Text>
                    <Heading>{heading}</Heading>
                    {body && <Preview>{body}</Preview>}
                    <Meta>
                        <span>{props.agentName}</span>
                        <Dot aria-hidden='true'>{'·'}</Dot>
                        <span>
                            <Timestamp // Matches the timestamp format in the threads view
                                value={props.lastActivityDate}
                                units={['now', 'minute', 'hour', 'day', 'week']}
                                useTime={false}
                                day={'numeric'}
                            />
                        </span>
                        {replies && (
                            <>
                                <Dot aria-hidden='true'>{'·'}</Dot>
                                <Replies>
                                    <Icon
                                        glyph={<ReplyOutlineIcon/>}
                                        size='10'
                                    />
                                    <span aria-hidden='true'>{props.replyCount}</span>
                                    <VisuallyHidden>{replies}</VisuallyHidden>
                                </Replies>
                            </>
                        )}
                    </Meta>
                </Text>
            </Main>
            <Actions>
                <IconButton
                    size='small'
                    padding='compact'
                    icon={<Icon glyph={<DotsHorizontalIcon/>}/>}
                    aria-label={actionsLabel}
                    data-testid='thread-item-actions'
                    onClick={(e) => {
                        e.stopPropagation();
                        props.onMenuClick(e.currentTarget);
                    }}
                />
            </Actions>
        </Item>
    );
}

// Rounded, with the divider drawn as a pseudo-element inset from the corners. It disappears while
// the item is hovered, and the one above it (that sits against the hovered item) goes with it.
const Item = styled.div`
    position: relative;
    border-radius: var(--radius-m);
    transition: background var(--duration-quick) var(--ease-transition);

    &::after {
        position: absolute;
        right: var(--spacing-m);
        bottom: 0;
        left: var(--spacing-m);
        border-bottom: var(--border-light);
        content: '';
        pointer-events: none;
        transition: opacity var(--duration-quick) var(--ease-transition);
    }

    &:hover,
    &:focus-within {
        background: color-mix(in srgb, var(--center-channel-color) ${HOVER_PERCENT}, transparent);
    }

    &:hover::after,
    &:focus-within::after,
    &:has(+ &:hover)::after,
    &:has(+ &:focus-within)::after {
        opacity: 0;
    }

    @media (prefers-reduced-motion: reduce) {
        transition: none;

        &::after {
            transition: none;
        }
    }
`;

const Main = styled.div`
    display: flex;
    align-items: flex-start;
    gap: var(--spacing-m);
    padding: var(--spacing-m) var(--spacing-m) calc(var(--spacing-m) + var(--spacing-xxxxs));
    border-radius: var(--radius-m);
    cursor: pointer;

    & > :first-child {
        flex: 0 0 auto;
    }

    &:focus-visible {
        outline: var(--spacing-xxxxs) solid var(--focus-ring-color);
        outline-offset: calc(-1 * var(--spacing-xxxxs));
    }
`;

const Text = styled.div`
    display: flex;
    min-width: 0;
    flex: 1 1 auto;
    flex-direction: column;
    gap: var(--spacing-xxxxs);
`;

const Heading = styled.div`
    overflow: hidden;
    color: var(--center-channel-color);
    font-size: var(--font-size-100);
    font-weight: var(--font-weight-semibold);
    line-height: var(--line-height-100);
    text-overflow: ellipsis;
    white-space: nowrap;
`;

// Two lines of the first message.
const Preview = styled.div`
    display: -webkit-box;
    overflow: hidden;
    font-size: var(--font-size-100);
    line-height: var(--line-height-100);
    overflow-wrap: anywhere;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
`;

const Meta = styled.div`
    display: flex;
    margin-top: var(--spacing-xxxxs);
    min-width: 0;
    align-items: center;
    gap: var(--spacing-xxs);
    color: ${META_COLOR};
    font-size: var(--font-size-75);
    line-height: var(--line-height-75);
    white-space: nowrap;
`;

const Dot = styled.span`
    flex: 0 0 auto;
`;

const Replies = styled.span`
    display: inline-flex;
    align-items: center;
    gap: var(--spacing-xxxxs);
`;

// Hover (or focus) reveals the actions button at the item's top right.
const Actions = styled.div`
    position: absolute;
    top: var(--spacing-s);
    right: var(--spacing-s);
    z-index: 1;
    opacity: 0;
    transition: opacity var(--duration-quick) var(--ease-transition);

    ${Item}:hover &,
    ${Item}:focus-within & {
        opacity: 1;
    }

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
