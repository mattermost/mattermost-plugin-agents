// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, render, waitFor} from '@testing-library/react';

import {PostPreview} from './post_preview';

interface PostPreviewMetadata {
    channel_display_name: string | null;
    channel_id: string;
    channel_type?: string;
    post_id: string;
    team_name: string;
    post: {
        id: string;
        message: string;
        user_id: string;
        channel_id: string;
        create_at?: number;
    };
}

interface PostMessagePreviewProps {
    metadata: PostPreviewMetadata;
}

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

const postMessagePreviewMock = jest.fn<null, [PostMessagePreviewProps]>(() => null);

jest.mock('@/mm_webapp', () => ({
    PostMessagePreview: (props: PostMessagePreviewProps) => postMessagePreviewMock(props),
}));

const baseState = {
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
};

beforeEach(() => {
    jest.clearAllMocks();
    mockUseSelector.mockImplementation((selector) => selector(baseState));
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

        await waitFor(() => {
            const lastCall = postMessagePreviewMock.mock.calls.at(-1)?.[0];
            expect(lastCall?.metadata.post.create_at).toBe(1700000000000);
        });

        const lastProps = postMessagePreviewMock.mock.calls.at(-1)?.[0];
        expect(lastProps?.metadata.post_id).toBe('post_1');
        expect(lastProps?.metadata.channel_id).toBe('channel_1');
        expect(lastProps?.metadata.team_name).toBe('team-name');
        expect(lastProps?.metadata.post.message).toBe('hello world');
        expect(lastProps?.metadata.post.user_id).toBe('user_1');
        expect(lastProps?.metadata.post.id).toBe('post_1');
    });

    test('clears create_at when navigating to a post without a timestamp', async () => {
        mockGetPost.mockResolvedValueOnce({
            id: 'post_1',
            user_id: 'user_1',
            channel_id: 'channel_1',
            message: 'first',
            create_at: 1700000000000,
        });
        mockGetProfilesByIds.mockResolvedValue([{id: 'user_1', username: 'someone'}]);

        let rerender: ReturnType<typeof render>['rerender'] | undefined;
        await act(async () => {
            const result = render(
                <PostPreview
                    postId='post_1'
                    userId='user_1'
                    channelId='channel_1'
                    content='first'
                />,
            );
            rerender = result.rerender;
        });

        await waitFor(() => {
            const lastCall = postMessagePreviewMock.mock.calls.at(-1)?.[0];
            expect(lastCall?.metadata.post.create_at).toBe(1700000000000);
        });

        // Simulate a follow-up post that has no create_at on the response.
        mockGetPost.mockResolvedValueOnce({
            id: 'post_2',
            user_id: 'user_1',
            channel_id: 'channel_1',
            message: 'second',
        });

        await act(async () => {
            rerender?.(
                <PostPreview
                    postId='post_2'
                    userId='user_1'
                    channelId='channel_1'
                    content='second'
                />,
            );
        });

        await waitFor(() => {
            expect(mockGetPost).toHaveBeenCalledWith('post_2');
        });

        await waitFor(() => {
            const lastCall = postMessagePreviewMock.mock.calls.at(-1)?.[0];
            expect(lastCall?.metadata.post_id).toBe('post_2');
            expect(lastCall?.metadata.post.create_at).toBeUndefined();
        });
    });

    test('does not retain stale create_at when the fetch fails', async () => {
        mockGetPost.mockResolvedValueOnce({
            id: 'post_1',
            user_id: 'user_1',
            channel_id: 'channel_1',
            message: 'first',
            create_at: 1700000000000,
        });
        mockGetProfilesByIds.mockResolvedValue([{id: 'user_1', username: 'someone'}]);

        let rerender: ReturnType<typeof render>['rerender'] | undefined;
        await act(async () => {
            const result = render(
                <PostPreview
                    postId='post_1'
                    userId='user_1'
                    channelId='channel_1'
                    content='first'
                />,
            );
            rerender = result.rerender;
        });

        await waitFor(() => {
            const lastCall = postMessagePreviewMock.mock.calls.at(-1)?.[0];
            expect(lastCall?.metadata.post.create_at).toBe(1700000000000);
        });

        // Suppress expected error log emitted by the catch handler.
        const consoleError = jest.spyOn(console, 'error').mockImplementation(() => {
            // no-op
        });

        try {
            mockGetPost.mockRejectedValueOnce(new Error('boom'));

            await act(async () => {
                rerender?.(
                    <PostPreview
                        postId='post_2'
                        userId='user_1'
                        channelId='channel_1'
                        content='second'
                    />,
                );
            });

            await waitFor(() => {
                expect(mockGetPost).toHaveBeenCalledWith('post_2');
            });

            await waitFor(() => {
                const lastCall = postMessagePreviewMock.mock.calls.at(-1)?.[0];
                expect(lastCall?.metadata.post_id).toBe('post_2');
                expect(lastCall?.metadata.post.create_at).toBeUndefined();
            });
        } finally {
            consoleError.mockRestore();
        }
    });
});
