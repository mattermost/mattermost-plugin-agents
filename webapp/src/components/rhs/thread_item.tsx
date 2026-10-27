// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';

import {ThreadListItem} from '@mattermost/compass-ui/components/thread-list-item';
import {UserAvatar} from '@mattermost/compass-ui/components/user-avatar';

import {Timestamp} from '@/mm_webapp';

type Props = {

    // The chat's title; shown above the first message when it has one.
    title: string;

    // The chat's first message; empty while it loads or if it is unavailable.
    preview: string;
    agentName: string;
    agentAvatarUrl: string;
    lastActivityDate: number;

    // Absent until the root post is loaded.
    replyCount?: number;
    onClick: () => void;

    // The item's hover actions button was used; the list opens the menu next to it.
    onMenuClick: (anchor: HTMLElement) => void;
}

// A chat in the history: the compass Thread List Item, the same entry as the host's Threads view
// (name, time, first message and reply count, with the hover actions button). The name is the
// agent's, with its avatar; there is no tag and no participants row, as every chat is with one agent.
export default function ThreadItem(props: Props) {
    const intl = useIntl();
    const title = props.title.trim();
    const known = typeof props.replyCount === 'number';
    return (
        <ThreadListItem
            authorName={(
                <AgentName>
                    <UserAvatar
                        src={props.agentAvatarUrl}
                        alt=''
                        aria-hidden='true'
                        name={props.agentName}
                        size='20'
                    />
                    {props.agentName}
                </AgentName>
            )}
            {...(title ? {threadTitle: title} : {})}
            previewText={props.preview.trim() || (title ? '' : intl.formatMessage({defaultMessage: 'Conversation with Agents'}))}
            timestamp={(
                <Timestamp // Matches the timestamp format in the threads view
                    value={props.lastActivityDate}
                    units={['now', 'minute', 'hour', 'day', 'week']}
                    useTime={false}
                    day={'numeric'}
                />
            )}
            replyCount={props.replyCount ?? 0}
            formatReplyCount={(count) => (known ? intl.formatMessage({defaultMessage: '{count, plural, one {# reply} other {# replies}}'}, {count}) : '')}
            participants={[]}
            menuLabel={intl.formatMessage({defaultMessage: 'Chat actions'})}
            onClick={props.onClick}
            onMenuClick={(e) => props.onMenuClick(e.currentTarget)}
        />
    );
}

const AgentName = styled.span`
    display: inline-flex;
    align-items: center;
    gap: var(--spacing-xs);
`;
