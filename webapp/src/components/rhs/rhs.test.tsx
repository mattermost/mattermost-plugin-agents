// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';

import {fakeHostTextboxRenders} from '../../../tests/fake_host_textbox';

import Rhs from './rhs';

const mockGetAIThreads = jest.fn();
const mockGetUserMCPTools = jest.fn();
const mockGetUserToolPreferences = jest.fn();
const mockUpdateRead = jest.fn();
const mockCreatePost = jest.fn();
const mockGetPostsByIds = jest.fn();

jest.mock('@/client', () => ({
    getAIThreads: () => mockGetAIThreads(),
    getUserMCPTools: () => mockGetUserMCPTools(),
    getUserToolPreferences: () => mockGetUserToolPreferences(),
    createPost: (...args: unknown[]) => mockCreatePost(...args),
    getProfilesByIds: () => Promise.resolve([]),
    getPostsByIds: (ids: string[]) => mockGetPostsByIds(ids),
    getProfilePictureUrl: (id: string) => `/users/${id}/image`,
    updateRead: (userId: string, teamId: string, postId: string, timestamp: number) => (
        mockUpdateRead(userId, teamId, postId, timestamp)
    ),
}));

type SelectorFn = (state: unknown) => unknown;
const mockUseSelector = jest.fn<unknown, [SelectorFn]>();
const mockDispatch = jest.fn();

jest.mock('react-redux', () => ({
    useSelector: (selector: SelectorFn) => mockUseSelector(selector),
    useDispatch: () => mockDispatch,
}));

const mockUseBotlist = jest.fn();

jest.mock('@/bots', () => ({
    useBotlist: () => mockUseBotlist(),
}));

jest.mock('react-intl', () => {
    const ReactActual = jest.requireActual<typeof import('react')>('react');

    return {
        IntlProvider: ({children}: {children: React.ReactNode}) => ReactActual.createElement(ReactActual.Fragment, null, children),
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => ReactActual.createElement(ReactActual.Fragment, null, defaultMessage),
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string>) => (
                defaultMessage.replace(/\{(\w+)\}/g, (_m, k) => values?.[k] ?? '')
            ),
        }),
    };
});

const mockThreadViewer = jest.fn();

jest.mock('@/mm_webapp', () => {
    const ReactActual = jest.requireActual<typeof import('react')>('react');

    return {
        Textbox: jest.requireActual('../../../tests/fake_host_textbox').FakeHostTextbox,
        ThreadViewer: (props: Record<string, unknown>) => {
            mockThreadViewer(props);
            return ReactActual.createElement('div', {'data-testid': 'rhs-thread-viewer'});
        },
    };
});

jest.mock('./rhs_new_tab', () => {
    const ReactActual = jest.requireActual<typeof import('react')>('react');

    return {
        __esModule: true,
        default: () => ReactActual.createElement('div', {'data-testid': 'rhs-new-tab'}),
    };
});

jest.mock('./thread_item', () => {
    const ReactActual = jest.requireActual<typeof import('react')>('react');

    return {
        __esModule: true,
        default: (props: {preview: string; replyCount?: number}) => ReactActual.createElement('div', {'data-testid': 'rhs-thread-item'}, `${props.preview}|${props.replyCount ?? 'none'}`),
    };
});

const activeBot = {
    id: 'bot-id',
    displayName: 'Agents',
    username: 'ai',
    lastIconUpdate: 0,
    dmChannelID: 'dm-channel-id',
    channelAccessLevel: 'all',
    channelIDs: [],
    userAccessLevel: 'all',
    userIDs: [],
    teamIDs: [],
    enabledMCPTools: [],
    autoEnableNewMCPTools: false,
};

const baseState = {
    'plugins-mattermost-ai': {
        selectedPostId: 'post-id',
    },
    entities: {
        users: {
            currentUserId: 'user-id',
            profiles: {'user-id': {id: 'user-id', username: 'me', first_name: 'Me', last_name: 'Tester', last_picture_update: 0}},
        },
        teams: {
            currentTeamId: 'team-id',
        },
        posts: {
            posts: {
                'post-id': {
                    id: 'post-id',
                    channel_id: 'dm-channel-id',
                    user_id: 'user-id',
                    props: {
                        conversation_id: 'conversation-id',
                    },
                },
            },
            postsInThread: {
                'post-id': [],
            },
        },
    },
};

function renderRHS() {
    return render(
        <Rhs/>,
    );
}

describe('RHS', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockGetUserMCPTools.mockResolvedValue({servers: []});
        mockGetUserToolPreferences.mockResolvedValue({disabled_servers: []});
        mockUpdateRead.mockImplementation(() => Promise.resolve());
        mockUseBotlist.mockReturnValue({
            bots: [activeBot],
            activeBot,
            setActiveBot: jest.fn(),
        });
        mockUseSelector.mockImplementation((selector) => selector(baseState));
    });

    test('renders the thread viewer when the read marker rejects for missing thread membership', async () => {
        const error = new Error('User thread membership doesn\'t exist');
        const warn = jest.spyOn(console, 'warn').mockImplementation(() => null);
        mockUpdateRead.mockRejectedValue(error);

        const {unmount} = renderRHS();

        expect(screen.getByTestId('rhs-thread-viewer')).toBeTruthy();
        await waitFor(() => {
            expect(mockUpdateRead).toHaveBeenCalledWith('user-id', 'team-id', 'post-id', expect.any(Number));
        });
        await waitFor(() => {
            expect(warn).toHaveBeenCalledWith(
                'Skipping AI thread read marker because thread membership is missing.',
                error,
            );
        });
        expect(mockThreadViewer).toHaveBeenCalledWith(expect.objectContaining({rootPostId: 'post-id'}));

        await act(async () => {
            unmount();
        });
        await act(async () => {
            await Promise.resolve();
        });
        warn.mockRestore();
    });

    test('the pane follows the tab shared with the header title', async () => {
        mockGetAIThreads.mockResolvedValue([]);
        const state = {
            ...baseState,
            'plugins-mattermost-ai': {selectedPostId: '', rhsTab: 'new'},
        };
        mockUseSelector.mockImplementation((selector) => selector(state));

        const {rerender} = renderRHS();
        expect(screen.getByTestId('rhs-new-tab')).toBeTruthy();

        // The header title changes the tab in the store.
        state['plugins-mattermost-ai'] = {selectedPostId: '', rhsTab: 'threads'};
        rerender(<Rhs/>);
        await waitFor(() => expect(screen.getByTestId('rhs-threads-list')).toBeTruthy());
        expect(screen.queryByTestId('rhs-new-tab')).toBeNull();

        // An open thread takes over whichever list tab was last chosen.
        state['plugins-mattermost-ai'] = {selectedPostId: 'post-id', rhsTab: 'threads'};
        rerender(<Rhs/>);
        expect(screen.getByTestId('rhs-thread-viewer')).toBeTruthy();
    });

    test.each([
        {
            name: 'shows the first message and reply count once the root posts load',
            posts: () => Promise.resolve([{id: 'root1', message: 'First message', reply_count: 2}]),
            expected: ['First message|2', 'Title two|none'],
        },
        {
            name: 'keeps the chat titles when the root posts cannot be loaded',
            posts: () => Promise.reject(new Error('boom')),
            expected: ['Title one|none', 'Title two|none'],
        },
    ])('chat history $name', async ({posts, expected}) => {
        const errorLog = jest.spyOn(console, 'error').mockImplementation(() => null);
        mockGetAIThreads.mockResolvedValue([
            {id: 'c1', bot_id: 'bot-id', root_post_id: 'root1', title: 'Title one', turn_count: 3, update_at: 1},
            {id: 'c2', bot_id: 'bot-id', root_post_id: 'root2', title: 'Title two', turn_count: 1, update_at: 2},
        ]);
        mockGetPostsByIds.mockImplementation(posts);
        const state = {...baseState, 'plugins-mattermost-ai': {selectedPostId: '', rhsTab: 'threads'}};
        mockUseSelector.mockImplementation((selector) => selector(state));

        renderRHS();

        await waitFor(() => expect(screen.getAllByTestId('rhs-thread-item').map((el) => el.textContent)).toEqual(expected));
        expect(mockGetPostsByIds).toHaveBeenCalledWith(['root1', 'root2']);
        errorLog.mockRestore();
    });

    test('logs generic read marker rejections', async () => {
        const error = new Error('Unable to update read marker');
        const errorLog = jest.spyOn(console, 'error').mockImplementation(() => null);
        mockUpdateRead.mockRejectedValue(error);

        const {unmount} = renderRHS();

        expect(screen.getByTestId('rhs-thread-viewer')).toBeTruthy();
        await waitFor(() => {
            expect(mockUpdateRead).toHaveBeenCalledWith('user-id', 'team-id', 'post-id', expect.any(Number));
        });
        await waitFor(() => {
            expect(errorLog).toHaveBeenCalledWith(
                'Failed to update AI thread read marker:',
                error,
            );
        });
        expect(mockThreadViewer).toHaveBeenCalledWith(expect.objectContaining({rootPostId: 'post-id'}));

        await act(async () => {
            unmount();
        });
        await act(async () => {
            await Promise.resolve();
        });
        errorLog.mockRestore();
    });

    test.each([
        {name: 'the thread is in the agent\'s DM', post: {channel_id: 'dm-channel-id', user_id: 'user-id'}, label: 'Reply to Agents'},
        {name: 'the agent started the thread', post: {channel_id: 'other-dm', user_id: 'bot-id'}, label: 'Reply to Agents'},
        {name: 'the agent is unknown', post: {channel_id: 'other-dm', user_id: 'user-id'}, label: 'Reply'},
        {name: 'it is a channel thread the agent was mentioned in', post: {channel_id: 'town-square', user_id: 'user-id'}, label: 'Reply'},
    ])('replies from the pill composer into the selected thread when $name', async ({post, label}) => {
        window.sessionStorage.clear();
        mockCreatePost.mockResolvedValue({id: 'reply-id'});
        const state = {
            ...baseState,
            entities: {...baseState.entities, posts: {...baseState.entities.posts, posts: {'post-id': {...baseState.entities.posts.posts['post-id'], ...post}}}},
        };
        mockUseSelector.mockImplementation((selector) => selector(state));

        const {unmount} = renderRHS();
        const reply = screen.getByRole('textbox', {name: label});
        fireEvent.change(reply, {target: {value: 'follow up'}});
        await act(async () => {
            fireEvent.keyDown(reply, {key: 'Enter'});
        });
        expect(mockCreatePost).toHaveBeenCalledWith(expect.objectContaining({channel_id: post.channel_id, root_id: 'post-id', message: 'follow up'}));

        // Autocomplete and slash commands are scoped to that thread.
        expect(fakeHostTextboxRenders[fakeHostTextboxRenders.length - 1]).toEqual(expect.objectContaining({channelId: post.channel_id, rootId: 'post-id'}));

        await act(async () => {
            unmount();
        });
    });

    test('offers no reply composer until the thread root is loaded', async () => {
        const state = {...baseState, entities: {...baseState.entities, posts: {posts: {}, postsInThread: {}}}};
        mockUseSelector.mockImplementation((selector) => selector(state));

        const {unmount} = renderRHS();
        expect(screen.getByTestId('rhs-thread-viewer')).toBeTruthy();
        expect(screen.queryByRole('textbox')).toBeNull();
        await act(async () => {
            unmount();
        });
    });
});
