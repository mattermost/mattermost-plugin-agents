// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';
import {Provider} from 'react-redux';
import {createStore, type Store} from 'redux';

import {aiThreads} from '@/redux';

import Rhs from './rhs';

const mockGetAIThreads = jest.fn();
const mockGetUserMCPTools = jest.fn();
const mockGetUserToolPreferences = jest.fn();
const mockUpdateRead = jest.fn();
const mockRenameConversation = jest.fn();
const mockGetPostsByIds = jest.fn();

jest.mock('@/client', () => ({
    getAIThreads: () => mockGetAIThreads(),
    getUserMCPTools: () => mockGetUserMCPTools(),
    getUserToolPreferences: () => mockGetUserToolPreferences(),
    getProfilesByIds: () => Promise.resolve([]),
    getPostsByIds: (ids: string[]) => mockGetPostsByIds(ids),
    renameConversation: (id: string, title: string) => mockRenameConversation(id, title),
    getProfilePictureUrl: (id: string) => `/users/${id}/image`,
    updateRead: (userId: string, teamId: string, postId: string, timestamp: number) => (
        mockUpdateRead(userId, teamId, postId, timestamp)
    ),
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
        default: (props: {conversationId: string; title: string; preview: string; replyCount?: number; chatActions: {startRename: (target: {conversationId: string; title: string}) => void}}) => ReactActual.createElement(
            'div',
            {'data-testid': 'rhs-thread-item'},
            `${props.title}|${props.preview}|${props.replyCount ?? 'none'}`,
            ReactActual.createElement('button', {type: 'button', onClick: () => props.chatActions.startRename({conversationId: props.conversationId, title: props.title})}, `rename ${props.title}`),
        ),
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
                    reply_count: 1,
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

// The test sets the host and plugin state; the plugin's real thread-list reducer runs on top of it.
const pluginKey = 'plugins-mattermost-ai';
type TestState = Record<string, any>;
const reducer = (state: TestState | undefined, action: {type: string; state?: TestState}): TestState => {
    const base = action.type === 'TEST_SET_STATE' ? action.state! : state ?? {};
    const plugin = base[pluginKey] ?? {};
    return {...base, [pluginKey]: {...plugin, aiThreads: aiThreads(state?.[pluginKey]?.aiThreads, action)}};
};
let store: Store<TestState>;
const setState = (state: TestState) => {
    if (store) {
        act(() => {
            store.dispatch({type: 'TEST_SET_STATE', state});
        });
    } else {
        store = createStore(reducer as never);
        store.dispatch({type: 'TEST_SET_STATE', state});
    }
};

function renderRHS() {
    const result = render(
        <Provider store={store}>
            <Rhs/>
        </Provider>,
    );
    return result;
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
        store = createStore(reducer as never);
        store.dispatch({type: 'TEST_SET_STATE', state: baseState});
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
        setState({...baseState, 'plugins-mattermost-ai': {selectedPostId: '', rhsTab: 'new'}});

        renderRHS();
        expect(screen.getByTestId('rhs-new-tab')).toBeTruthy();

        // The header title changes the tab in the store.
        setState({...baseState, 'plugins-mattermost-ai': {selectedPostId: '', rhsTab: 'threads'}});
        await waitFor(() => expect(screen.getByTestId('rhs-threads-list')).toBeTruthy());

        // The new chat leaves once its exit transition is over.
        await waitFor(() => expect(screen.queryByTestId('rhs-new-tab')).toBeNull());

        // An open thread takes over whichever list tab was last chosen.
        setState({...baseState, 'plugins-mattermost-ai': {selectedPostId: 'post-id', rhsTab: 'threads'}});
        await waitFor(() => expect(screen.getByTestId('rhs-thread-viewer')).toBeTruthy());
    });

    test.each([
        {
            name: 'shows the first message and reply count once the root posts load',
            posts: () => Promise.resolve([{id: 'root1', message: 'First message', reply_count: 2}]),
            expected: ['Title one|First message|2', 'Title two||none'],
        },
        {
            name: 'keeps the chat titles when the root posts cannot be loaded',
            posts: () => Promise.reject(new Error('boom')),
            expected: ['Title one||none', 'Title two||none'],
        },
    ])('chat history $name', async ({posts, expected}) => {
        const errorLog = jest.spyOn(console, 'error').mockImplementation(() => null);
        mockGetAIThreads.mockResolvedValue([
            {id: 'c1', bot_id: 'bot-id', root_post_id: 'root1', title: 'Title one', turn_count: 3, update_at: 1},
            {id: 'c2', bot_id: 'bot-id', root_post_id: 'root2', title: 'Title two', turn_count: 1, update_at: 2},
        ]);
        mockGetPostsByIds.mockImplementation(posts);
        const state = {...baseState, 'plugins-mattermost-ai': {selectedPostId: '', rhsTab: 'threads'}};
        setState(state);

        renderRHS();

        await waitFor(() => expect(screen.getAllByTestId('rhs-thread-item').map((el) => el.firstChild?.textContent)).toEqual(expected));
        expect(mockGetPostsByIds).toHaveBeenCalledWith(['root1', 'root2']);
        errorLog.mockRestore();
    });

    describe('chat history actions', () => {
        const openHistory = async () => {
            mockGetAIThreads.mockResolvedValue([
                {id: 'c1', bot_id: 'bot-id', root_post_id: 'root1', title: 'Title one', turn_count: 3, update_at: 1},
                {id: 'c2', bot_id: 'bot-id', root_post_id: 'root2', title: 'Title two', turn_count: 1, update_at: 2},
            ]);
            mockGetPostsByIds.mockResolvedValue([]);
            const state = {...baseState, 'plugins-mattermost-ai': {selectedPostId: '', rhsTab: 'threads'}};
            setState(state);
            renderRHS();
            await waitFor(() => expect(screen.getAllByTestId('rhs-thread-item')).toHaveLength(2));
        };

        test('Rename chat asks for a title, renames that chat and shows the new title', async () => {
            mockRenameConversation.mockResolvedValue(true);
            await openHistory();

            fireEvent.click(screen.getByRole('button', {name: 'rename Title one'}));
            const input = await screen.findByLabelText('Chat name');
            expect((input as HTMLInputElement).value).toBe('Title one');
            fireEvent.change(input, {target: {value: 'Renamed'}});
            fireEvent.click(screen.getByRole('button', {name: 'Save'}));

            await waitFor(() => expect(mockRenameConversation).toHaveBeenCalledWith('c1', 'Renamed'));
            await waitFor(() => expect(screen.getAllByTestId('rhs-thread-item').map((el) => el.firstChild?.textContent)).toEqual(['Renamed||none', 'Title two||none']));
        });
    });

    test('marks the open chat read now (not ahead of time), again on a new reply, and on leaving', async () => {
        const {unmount} = renderRHS();
        await waitFor(() => expect(mockUpdateRead).toHaveBeenCalledTimes(1));
        const [, , postId, timestamp] = mockUpdateRead.mock.calls[0];
        expect(postId).toBe('post-id');

        // A future read time would hide every later reply from the host's unread state.
        expect(timestamp).toBeLessThanOrEqual(Date.now());

        const post = baseState.entities.posts.posts['post-id'];
        setState({...baseState, entities: {...baseState.entities, posts: {...baseState.entities.posts, posts: {'post-id': {...post, reply_count: 2}}}}});
        await waitFor(() => expect(mockUpdateRead).toHaveBeenCalledTimes(2));

        await act(async () => {
            unmount();
        });
        expect(mockUpdateRead).toHaveBeenCalledTimes(3);
        expect(mockUpdateRead).toHaveBeenLastCalledWith('user-id', 'team-id', 'post-id', expect.any(Number));
    });

    test('a chat without replies is not marked (the user has no thread membership yet)', async () => {
        const post = baseState.entities.posts.posts['post-id'];
        setState({...baseState, entities: {...baseState.entities, posts: {...baseState.entities.posts, posts: {'post-id': {...post, reply_count: 0}}}}});
        const {unmount} = renderRHS();
        await act(async () => {
            unmount();
        });
        expect(mockUpdateRead).not.toHaveBeenCalled();
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
        {name: 'the thread is in the agent\'s DM', post: {channel_id: 'dm-channel-id', user_id: 'user-id'}, placeholder: 'Reply to Agents'},
        {name: 'the agent started the thread', post: {channel_id: 'other-dm', user_id: 'bot-id'}, placeholder: 'Reply to Agents'},
        {name: 'the agent is unknown', post: {channel_id: 'other-dm', user_id: 'user-id'}, placeholder: 'Reply…'},
        {name: 'it is a channel thread the agent was mentioned in', post: {channel_id: 'town-square', user_id: 'user-id'}, placeholder: 'Reply…'},
    ])('the host thread viewer brings its own reply composer when $name', async ({post, placeholder}) => {
        const state = {
            ...baseState,
            entities: {...baseState.entities, posts: {...baseState.entities.posts, posts: {'post-id': {...baseState.entities.posts.posts['post-id'], ...post}}}},
        };
        setState(state);

        const {unmount} = renderRHS();
        expect(mockThreadViewer).toHaveBeenLastCalledWith(expect.objectContaining({rootPostId: 'post-id', inputPlaceholder: placeholder}));

        await act(async () => {
            unmount();
        });
    });
});
