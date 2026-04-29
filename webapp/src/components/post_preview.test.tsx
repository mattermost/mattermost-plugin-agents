// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, render, waitFor} from '@testing-library/react';

import {PostPreview} from './post_preview';

const mockGetPost = jest.fn();
const mockGetProfilesByIds = jest.fn();

jest.mock('@/client', () => ({
    getPost: (id: string) => mockGetPost(id),
    getProfilesByIds: (ids: string[]) => mockGetProfilesByIds(ids),
}));

const mockUseSelector = jest.fn();
const mockDispatch = jest.fn();

jest.mock('react-redux', () => ({
    useSelector: (selector: any) => mockUseSelector(selector),
    useDispatch: () => mockDispatch,
}));

const postMessagePreviewMock: jest.Mock<null, [any]> = jest.fn<null, [any]>(() => null);

jest.mock('@/mm_webapp', () => ({
    PostMessagePreview: (props: any) => postMessagePreviewMock(props),
}));

beforeEach(() => {
    jest.clearAllMocks();
    mockUseSelector.mockImplementation((selector: any) => selector({
        entities: {
            channels: {
                channels: {
                    channel_1: {id: 'channel_1', team_id: 'team_1', type: 'O'},
                },
            },
            teams: {
                teams: {
                    team_1: {id: 'team_1', name: 'team-name'},
                },
            },
        },
    }));
});

describe('PostPreview', () => {
    test('passes the source post create_at into PostMessagePreview metadata', async () => {
        const sourcePost = {
            id: 'post_1',
            user_id: 'user_1',
            channel_id: 'channel_1',
            message: 'hello world',
            create_at: 1700000000000,
        };

        mockGetPost.mockResolvedValue(sourcePost);
        mockGetProfilesByIds.mockResolvedValue([{id: 'user_1', username: 'someone'}]);

        await act(async () => {
            render(
                <PostPreview
                    postId='post_1'
                    userId='user_1'
                    channelId='channel_1'
                    content='hello world'
                />,
            );
        });

        await waitFor(() => {
            expect(mockGetPost).toHaveBeenCalledWith('post_1');
        });

        // Wait for the async fetch to update component state and re-render.
        await waitFor(() => {
            const lastCall = postMessagePreviewMock.mock.calls.at(-1)?.[0] as any;
            expect(lastCall?.metadata?.post?.create_at).toBe(1700000000000);
        });

        const lastProps = postMessagePreviewMock.mock.calls.at(-1)?.[0] as any;
        expect(lastProps.metadata.post_id).toBe('post_1');
        expect(lastProps.metadata.channel_id).toBe('channel_1');
        expect(lastProps.metadata.team_name).toBe('team-name');
        expect(lastProps.metadata.post.message).toBe('hello world');
        expect(lastProps.metadata.post.user_id).toBe('user_1');
        expect(lastProps.metadata.post.id).toBe('post_1');
    });
});
