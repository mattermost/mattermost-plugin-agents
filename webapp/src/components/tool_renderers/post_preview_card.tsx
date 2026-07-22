// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useState} from 'react';
import styled from 'styled-components';

import {getPost} from '@/client';

import {ToolCall} from '../tool_types';
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
 * Card for read_post: shows the referenced post as a Mattermost permalink-style
 * preview so the user sees what the tool will actually read. Falls back to the
 * generic card when the arguments don't parse or the post can't be fetched
 * (e.g. deleted or inaccessible).
 */
const PostPreviewCard: React.FC<RichCardProps> = (props) => {
    const parsed = parseReadPost(props.tool.arguments);
    const postId = parsed?.postId;

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

    if (!parsed || failed) {
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
