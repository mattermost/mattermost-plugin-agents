// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useMemo, useState} from 'react';
import styled from 'styled-components';
import {useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

import {getPost} from '@/client';
import {PostMessagePreview} from '@/mm_webapp';

import {ToolCall, ToolCallStatus} from '../tool_types';
import ToolCard from '../tool_card';
import ToolArguments from '../tool_arguments';
import {PostPreview} from '../post_preview';

import ToolCardShell, {RichCardProps} from './tool_card_shell';

const PreviewWrap = styled.div`
    margin-top: 12px;
`;

// The preview cards only render while the call still needs a decision: that is
// when seeing the post has approval value. Executed calls render the generic
// card (the response already carries the outcome).
function isAwaitingDecision(tool: ToolCall): boolean {
    return tool.status === ToolCallStatus.Pending || tool.status === ToolCallStatus.Accepted;
}

interface ReadPostParsed {
    postId: string;
}

function parseReadPost(args: ToolCall['arguments']): ReadPostParsed | null {
    if (args == null || typeof args !== 'object' || Array.isArray(args)) {
        return null;
    }
    const postId = (args as {[key: string]: unknown}).post_id;
    if (typeof postId !== 'string' || postId === '') {
        return null;
    }
    return {postId};
}

interface FetchedPost {
    userId: string;
    channelId: string;
    message: string;
}

/**
 * Card for read_post: while the call awaits approval, shows the referenced
 * post as a Mattermost permalink-style preview so the user sees what the tool
 * will actually read. Falls back to the generic card when the call has already
 * executed (the response carries the content, and a preview would re-render
 * the post's markdown next to the deliberately-unrendered result), when the
 * arguments don't parse, or when the post can't be fetched.
 */
const PostPreviewCard: React.FC<RichCardProps> = (props) => {
    const parsed = parseReadPost(props.tool.arguments);
    const awaiting = isAwaitingDecision(props.tool);
    const postId = awaiting ? parsed?.postId : undefined; // eslint-disable-line no-undefined

    const [post, setPost] = useState<FetchedPost | null>(null);
    const [failed, setFailed] = useState(false);

    useEffect(() => {
        let cancelled = false;
        if (postId) {
            getPost(postId).then((fetched) => {
                if (!cancelled) {
                    setPost({userId: fetched.user_id, channelId: fetched.channel_id, message: fetched.message});
                }
            }).catch(() => {
                if (!cancelled) {
                    setFailed(true);
                }
            });
        }
        return () => {
            cancelled = true;
        };
    }, [postId]);

    if (!parsed || failed || !awaiting) {
        return <ToolCard {...props}/>;
    }

    return (
        <ToolCardShell {...props}>
            {post ? (
                <PreviewWrap>
                    <PostPreview
                        postId={parsed.postId}
                        userId={post.userId}
                        channelId={post.channelId}
                        content={post.message}
                    />
                </PreviewWrap>
            ) : (
                <ToolArguments arguments={props.tool.arguments}/>
            )}
        </ToolCardShell>
    );
};

export default PostPreviewCard;

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
export const CreatePostPreviewCard: React.FC<RichCardProps> = (props) => {
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
            <PreviewWrap>
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
            </PreviewWrap>
        </ToolCardShell>
    );
};
