// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useState} from 'react';
import styled from 'styled-components';

import {getPost} from '@/client';

import {ToolCall, ToolCallStatus} from '../tool_types';
import ToolCard from '../tool_card';
import ToolArguments from '../tool_arguments';
import {PostPreview} from '../post_preview';

import ToolCardShell, {RichCardProps} from './tool_card_shell';

const PreviewWrap = styled.div`
    margin-top: 12px;
`;

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
    const isAwaitingDecision = props.tool.status === ToolCallStatus.Pending || props.tool.status === ToolCallStatus.Accepted;
    const postId = isAwaitingDecision ? parsed?.postId : undefined; // eslint-disable-line no-undefined

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

    if (!parsed || failed || !isAwaitingDecision) {
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
