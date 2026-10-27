// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';

import {ThreadListItem} from '@mattermost/compass-ui/components/thread-list-item';

import {Timestamp} from '@/mm_webapp';

type Props = {

    // The chat's title; shown above the first message when it has one.
    title: string;

    // The chat's first message; empty while it loads or if it is unavailable.
    preview: string;
    authorName: string;
    authorAvatarUrl: string;
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
// (author and agent tag, time, first message, participants and reply count, with the hover
// actions button).
export default function ThreadItem(props: Props) {
    const intl = useIntl();
    const title = props.title.trim();
    const known = typeof props.replyCount === 'number';
    return (
        <ThreadListItem
            authorName={props.authorName}
            channelLabel={props.agentName}
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
            participants={[
                {key: 'author', src: props.authorAvatarUrl, name: props.authorName},
                {key: 'agent', src: props.agentAvatarUrl, name: props.agentName},
            ]}
            menuLabel={intl.formatMessage({defaultMessage: 'Chat actions'})}
            onClick={props.onClick}
            onMenuClick={(e) => props.onMenuClick(e.currentTarget)}
        />
    );
}
