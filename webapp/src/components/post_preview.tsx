// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useState} from 'react';
import {useSelector, useDispatch} from 'react-redux';
import styled from 'styled-components';

import {GlobalState} from '@mattermost/types/store';

import {PostMessagePreview} from '@/mm_webapp';
import {getPost, getProfilesByIds} from '@/client';

const MessagePreviewWrapper = styled.div`
    margin-left: 20px;
    margin-top: 4px;
`;

interface Props {
    postId: string;
    userId: string;
    channelId: string;
    content: string;
}

export const PostPreview: React.FC<Props> = ({postId, userId, channelId, content}) => {
    const dispatch = useDispatch();
    const channel = useSelector((state: GlobalState) => state.entities.channels.channels[channelId]);
    const team = useSelector((state: GlobalState) => state.entities.teams.teams[channel?.team_id || '']);
    const teamName = team?.name || '';
    const [createAt, setCreateAt] = useState<number | undefined>();

    useEffect(() => {
        async function fetchData() {
            const [post, profiles] = await Promise.all([
                getPost(postId),
                getProfilesByIds([userId]),
            ]);

            // Capture the source post's creation timestamp so the preview's
            // header renders the correct relative time instead of defaulting
            // to "now" (a missing/0 create_at is rendered as "now").
            if (post?.create_at) {
                setCreateAt(post.create_at);
            }

            // Store post in Redux
            dispatch({
                type: 'RECEIVED_POST',
                data: post,
            });

            // Store profiles in Redux
            const profilesById = profiles.reduce<Record<string, any>>((acc, profile) => {
                acc[profile.id] = profile;
                return acc;
            }, {});

            dispatch({
                type: 'RECEIVED_PROFILES',
                data: profilesById,
            });
        }

        fetchData();
    }, [dispatch, postId, userId]);

    return (
        <MessagePreviewWrapper>
            <PostMessagePreview
                metadata={{
                    channel_display_name: null,
                    channel_id: channelId,
                    channel_type: channel?.type,
                    post_id: postId,
                    team_name: teamName,
                    post: {
                        id: postId,
                        message: content,
                        user_id: userId,
                        channel_id: channelId,
                        create_at: createAt,
                    },
                }}
            />
        </MessagePreviewWrapper>
    );
};
