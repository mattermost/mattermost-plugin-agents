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

type SelectorFn = (state: unknown) => unknown;
const mockUseSelector = jest.fn<unknown, [SelectorFn]>();
const mockDispatch = jest.fn();

jest.mock('react-redux', () => ({
    useSelector: (selector: SelectorFn) => mockUseSelector(selector),
    useDispatch: () => mockDispatch,
}));

const postMessagePreviewMock = jest.fn<null, [{metadata: {post: {create_at?: number}}}]>(() => null);

jest.mock('@/mm_webapp', () => ({
    PostMessagePreview: (props: {metadata: {post: {create_at?: number}}}) => postMessagePreviewMock(props),
}));

const baseState = (posts: Record<string, {id: string; create_at: number}>) => ({
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
        posts: {
            posts,
        },
    },
});

beforeEach(() => {
    jest.clearAllMocks();
    mockGetPost.mockResolvedValue({id: 'post_1', user_id: 'user_1', channel_id: 'channel_1', message: 'hello', create_at: 1700000000000});
    mockGetProfilesByIds.mockResolvedValue([{id: 'user_1', username: 'someone'}]);
});

describe('PostPreview', () => {
    test('forwards the stored post create_at into PostMessagePreview metadata', async () => {
        mockUseSelector.mockImplementation((selector) => selector(baseState({post_1: {id: 'post_1', create_at: 1700000000000}})));

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

        const props = postMessagePreviewMock.mock.calls.at(-1)?.[0];
        expect(props?.metadata.post.create_at).toBe(1700000000000);
    });

    test('renders without create_at until the fetched post is in the store', async () => {
        mockUseSelector.mockImplementation((selector) => selector(baseState({})));

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

        const props = postMessagePreviewMock.mock.calls.at(-1)?.[0];
        expect(props?.metadata.post.create_at).toBeUndefined();
    });

    test('dispatches RECEIVED_POST and RECEIVED_PROFILES after fetching', async () => {
        mockUseSelector.mockImplementation((selector) => selector(baseState({})));

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
            expect(mockDispatch).toHaveBeenCalledWith(expect.objectContaining({type: 'RECEIVED_POST'}));
            expect(mockDispatch).toHaveBeenCalledWith(expect.objectContaining({type: 'RECEIVED_PROFILES'}));
        });
    });
});
