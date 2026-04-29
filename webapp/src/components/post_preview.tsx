// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect} from 'react';
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
    const storedPost = useSelector((state: GlobalState) => state.entities.posts.posts[postId]);

    useEffect(() => {
        async function fetchData() {
            try {
                const [post, profiles] = await Promise.all([
                    getPost(postId),
                    getProfilesByIds([userId]),
                ]);

                dispatch({
                    type: 'RECEIVED_POST',
                    data: post,
                });

                const profilesById = profiles.reduce<Record<string, any>>((acc, profile) => {
                    acc[profile.id] = profile;
                    return acc;
                }, {});

                dispatch({
                    type: 'RECEIVED_PROFILES',
                    data: profilesById,
                });
            } catch (err) {
                // eslint-disable-next-line no-console
                console.error('PostPreview: failed to fetch source post or profile', err);
            }
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
                        create_at: storedPost?.create_at,
                    },
                }}
            />
        </MessagePreviewWrapper>
    );
};
