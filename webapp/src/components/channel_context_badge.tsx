// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import styled from 'styled-components';

import {CreationOutlineIcon} from '@mattermost/compass-icons/components';
import {Post} from '@mattermost/types/posts';

import {isChannelContextPinned, useChannelContextPosts} from '@/channel_context';

type Props = {
    post: Post;
};

// Post-header label for posts pinned to agent context. Deliberately distinct
// from the host's "Pinned" label: agent-context pins and pinned messages are
// separate lists.
const ChannelContextBadge = ({post}: Props) => {
    const intl = useIntl();
    const contextPosts = useChannelContextPosts(post.channel_id);

    if (!isChannelContextPinned(contextPosts, post.id)) {
        return null;
    }

    return (
        <Badge
            title={intl.formatMessage({defaultMessage: 'Pinned to agent context: agents working in this channel receive this post as background context.'})}
            data-testid='channel-context-badge'
        >
            <CreationOutlineIcon size={12}/>
            <FormattedMessage defaultMessage='Agent context'/>
        </Badge>
    );
};

const Badge = styled.span`
    display: inline-flex;
    align-items: center;
    gap: 2px;
    margin-left: 4px;
    padding: 0 4px;
    border-radius: 4px;
    background: rgba(var(--button-bg-rgb), 0.08);
    color: var(--button-bg);
    font-size: 10px;
    font-weight: 600;
    line-height: 16px;
    text-transform: uppercase;
    white-space: nowrap;
`;

export default ChannelContextBadge;
