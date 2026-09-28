// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useMemo} from 'react';
import styled from 'styled-components';
import {useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

import {PostMessagePreview} from '@/mm_webapp';

import {ToolCall} from '../../tool_types';
import ToolCard from '../../tool_card';
import ToolCardShell, {RichCardProps} from '../tool_card_shell';

import {PreviewWrap, isAwaitingDecision} from './common';

// The create_post preview shows a post that does not exist yet, so none of the
// preview's interactive elements (permalink jump, avatar, username) can lead
// anywhere real.
const StaticPreviewWrap = styled(PreviewWrap)`
    pointer-events: none;
`;

interface CreatePostParsed {
    message: string;
    channelId?: string;
    channelDisplayName?: string;
}

function parseCreatePost(args: ToolCall['arguments']): CreatePostParsed | null {
    if (args == null || typeof args !== 'object' || Array.isArray(args)) {
        return null;
    }
    const obj = args as {[key: string]: unknown};
    if (typeof obj.message !== 'string' || obj.message === '') {
        return null;
    }
    return {
        message: obj.message,
        channelId: typeof obj.channel_id === 'string' && obj.channel_id !== '' ? obj.channel_id : undefined, // eslint-disable-line no-undefined
        channelDisplayName: typeof obj.channel_display_name === 'string' && obj.channel_display_name !== '' ? obj.channel_display_name : undefined, // eslint-disable-line no-undefined
    };
}

/**
 * Card for create_post: while the call awaits approval, shows the post-to-be
 * as a Mattermost permalink-style preview — the same rendering the message
 * gets once it is actually posted — so the user sees exactly what they are
 * approving. The author is the requesting user (embedded tools execute with
 * the requester's session). Falls back to the generic card when the arguments
 * don't parse or the call has already executed.
 */
const CreatePostPreviewCard: React.FC<RichCardProps> = (props) => {
    const parsed = parseCreatePost(props.tool.arguments);
    const awaiting = isAwaitingDecision(props.tool);

    const currentUserId = useSelector((state: GlobalState) => state.entities.users.currentUserId);
    const channel = useSelector((state: GlobalState) => state.entities.channels.channels[parsed?.channelId || '']);
    const team = useSelector((state: GlobalState) => state.entities.teams.teams[channel?.team_id || '']);

    // Stable timestamp so the preview doesn't re-date itself on every render.
    const createAt = useMemo(() => Date.now(), []);

    if (!parsed || !awaiting) {
        return <ToolCard {...props}/>;
    }

    return (
        <ToolCardShell {...props}>
            <StaticPreviewWrap>
                <PostMessagePreview
                    metadata={{
                        channel_display_name: channel?.display_name ?? parsed.channelDisplayName ?? null,
                        channel_id: parsed.channelId ?? '',
                        channel_type: channel?.type,
                        post_id: '',
                        team_name: team?.name ?? '',
                        post: {
                            id: '',
                            message: parsed.message,
                            user_id: currentUserId,
                            channel_id: parsed.channelId ?? '',
                            create_at: createAt,
                        },
                    }}
                />
            </StaticPreviewWrap>
        </ToolCardShell>
    );
};

export default CreatePostPreviewCard;
