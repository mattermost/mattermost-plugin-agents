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

jest.mock('@/client', () => ({
    getAIThreads: () => mockGetAIThreads(),
    getUserMCPTools: () => mockGetUserMCPTools(),
    getUserToolPreferences: () => mockGetUserToolPreferences(),
    createPost: (...args: unknown[]) => mockCreatePost(...args),
    getProfilesByIds: () => Promise.resolve([]),
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

jest.mock('./rhs_header', () => {
    const ReactActual = jest.requireActual<typeof import('react')>('react');

    return {
        __esModule: true,
        default: (props: {setCurrentTab: (tab: string) => void}) => ReactActual.createElement('button', {
            'data-testid': 'rhs-header',
            onClick: () => props.setCurrentTab('threads'),
        }),
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
        default: () => ReactActual.createElement('div', {'data-testid': 'rhs-thread-item'}),
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

    test('a new-chat request from the header title returns to the new-chat tab, but not on first render', async () => {
        mockGetAIThreads.mockResolvedValue([]);
        const state = {
            ...baseState,
            'plugins-mattermost-ai': {selectedPostId: '', newChatRequest: 0},
        };
        mockUseSelector.mockImplementation((selector) => selector(state));

        const {rerender} = renderRHS();
        expect(screen.getByTestId('rhs-new-tab')).toBeTruthy();

        // Leave the new-chat tab for the history tab.
        fireEvent.click(screen.getByTestId('rhs-header'));
        await waitFor(() => expect(screen.queryByTestId('rhs-new-tab')).toBeNull());

        // Unrelated re-render with the same request counter: stays on the history tab.
        rerender(<Rhs/>);
        expect(screen.queryByTestId('rhs-new-tab')).toBeNull();

        // The agent was switched in the header: back to a fresh new chat.
        state['plugins-mattermost-ai'] = {selectedPostId: '', newChatRequest: 1};
        rerender(<Rhs/>);
        await waitFor(() => expect(screen.getByTestId('rhs-new-tab')).toBeTruthy());
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
