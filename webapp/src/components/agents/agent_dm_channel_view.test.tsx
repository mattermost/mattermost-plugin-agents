// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {act, render, screen, waitFor} from '@testing-library/react';

import type {Channel} from '@mattermost/types/channels';

import manifest from '@/manifest';

import AgentDMChannelView from './agent_dm_channel_view';
import {dmChannelPath} from './agent_dm_route';
import {ALL_AGENTS_PATH, buildChatPath} from './use_agents_route';

const mockGetPost = jest.fn();
jest.mock('@/client', () => ({
    getPost: (...args: unknown[]) => mockGetPost(...args),
    getAIBots: jest.fn(() => new Promise(() => null)),
    savePreferences: jest.fn(),
}));

// The chat pane has its own tests; here only what the view hands it.
interface ChatPageProps {
    botId: string;
    rootId?: string;
    toChat: (botId: string, rootId?: string) => void;
    toAll: () => void;
}
const mockChatPageProps = jest.fn();
jest.mock('./agent_chat_page', () => ({
    __esModule: true,
    default: (props: ChatPageProps) => {
        mockChatPageProps(props);
        return <div data-testid='chat-page'>{props.rootId || 'new chat'}</div>;
    },
}));

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string>) => (
            defaultMessage.replace(/\{(\w+)\}/g, (_m, k) => values?.[k] ?? '')
        ),
    };
    return {...actual, useIntl: () => intl};
});

const pluginKey = `plugins-${manifest.id}`;

const makeBot = (id: string) => ({id, displayName: `Agent ${id}`, username: `${id}-name`, dmChannelID: ''});

const posts: Record<string, {id: string; root_id: string; channel_id: string}> = {
    root1: {id: 'root1', root_id: '', channel_id: 'dm1'},
    reply1: {id: 'reply1', root_id: 'root1', channel_id: 'dm1'},
};

const makeStore = () => createStore((state: any) => state, {
    [pluginKey]: {bots: [makeBot('agent1'), makeBot('agent2')]},
    entities: {
        users: {currentUserId: 'me'},
        posts: {posts},
        preferences: {myPreferences: {}},
    },
} as any);

const channel = {id: 'dm1', type: 'D', name: 'agent1__me'} as Channel;

const listeners = new Set<(...args: unknown[]) => void>();
const history = {
    location: {pathname: '/team1/messages/@agent1-name'},
    push: jest.fn((path: string) => {
        history.location.pathname = path;
        listeners.forEach((listener) => listener(history.location, 'PUSH'));
    }),
    listen: (listener: (...args: unknown[]) => void) => {
        listeners.add(listener);
        return () => listeners.delete(listener);
    },
};

// A navigation the host makes itself (the sidebar's chats, a permalink).
const navigate = (path: string) => act(() => history.push(path));

// The host rewriting the URL in place, as it does to a permalink's after a moment, reported the way
// history v4 (location, action) or v5 ({location, action}) does.
const replace = (path: string, report: 'v4' | 'v5') => act(() => {
    history.location.pathname = path;
    listeners.forEach((listener) => (report === 'v4' ? listener(history.location, 'REPLACE') : listener({location: history.location, action: 'REPLACE'})));
});

const DM = '/team1/messages/@agent1-name';

// The host opens a permalinked DM with the post in the URL and as focusedPostId.
const renderView = (focusedPostId?: string, pathname = focusedPostId ? `${DM}/${focusedPostId}` : DM) => {
    history.location.pathname = pathname;
    const store = makeStore();
    const view = (id?: string) => (
        <Provider store={store}>
            <AgentDMChannelView
                channel={channel}
                channelId='dm1'
                teamId='team1'
                focusedPostId={id}
            />
        </Provider>
    );
    return render(view(focusedPostId));
};

const lastChatPage = (): ChatPageProps => mockChatPageProps.mock.calls[mockChatPageProps.mock.calls.length - 1][0];

beforeEach(() => {
    jest.clearAllMocks();
    listeners.clear();
    history.location.pathname = '/team1/messages/@agent1-name';
    window.WebappUtils = {browserHistory: history} as unknown as typeof window.WebappUtils;
});

afterEach(() => {
    jest.restoreAllMocks();
    delete window.WebappUtils;
});

describe('AgentDMChannelView', () => {
    test('opens a new chat with the DM agent', () => {
        renderView();

        expect(screen.getByTestId('chat-page').textContent).toBe('new chat');
        expect(lastChatPage()).toMatchObject({botId: 'agent1'});
    });

    test.each([
        ['a root post', 'root1', 'root1'],
        ['a reply opens its thread', 'reply1', 'root1'],
    ])('a permalink to %s opens that chat', (_name, focusedPostId, expectedRoot) => {
        renderView(focusedPostId);

        expect(screen.getByTestId('chat-page').textContent).toBe(expectedRoot);
        expect(mockGetPost).not.toHaveBeenCalled();
    });

    test('a permalink to a post not in the store is looked up', async () => {
        mockGetPost.mockResolvedValue({id: 'reply9', root_id: 'root9', channel_id: 'dm1'});
        renderView('reply9');

        expect(screen.queryByTestId('chat-page')).toBeNull();
        expect(await screen.findByText('root9')).toBeTruthy();
        expect(mockGetPost).toHaveBeenCalledWith('reply9');
    });

    test('a failed lookup still opens the chat pane, which checks the chat', async () => {
        mockGetPost.mockRejectedValue(new Error('nope'));
        jest.spyOn(console, 'error').mockImplementation(() => null);
        renderView('gone1');

        expect(await screen.findByText('gone1')).toBeTruthy();
    });

    test('a chat opened meanwhile wins over a permalink lookup that resolves later', async () => {
        let resolve: (post: unknown) => void = () => null;
        mockGetPost.mockReturnValue(new Promise((r) => {
            resolve = r;
        }));
        renderView('reply9');

        navigate(`${DM}/root1`);
        expect(screen.getByTestId('chat-page').textContent).toBe('root1');

        await act(async () => resolve({id: 'reply9', root_id: 'root9', channel_id: 'dm1'}));
        expect(screen.getByTestId('chat-page').textContent).toBe('root1');
    });

    test('a permalink lookup pending in one DM does not open its chat in another DM', async () => {
        let resolve: (post: unknown) => void = () => null;
        mockGetPost.mockReturnValue(new Promise((r) => {
            resolve = r;
        }));
        const {rerender} = renderView('reply9');

        // The host leaves the permalink's URL along with the channel.
        history.location.pathname = '/team1/messages/@agent1-name';
        rerender(
            <Provider store={makeStore()}>
                <AgentDMChannelView
                    channel={{id: 'dm2', type: 'D', name: 'agent1__me'} as Channel}
                    channelId='dm2'
                    teamId='team1'
                />
            </Provider>,
        );
        expect(screen.getByTestId('chat-page').textContent).toBe('new chat');

        await act(async () => resolve({id: 'reply9', root_id: 'root9', channel_id: 'dm1'}));
        expect(screen.getByTestId('chat-page').textContent).toBe('new chat');
    });

    test('a picked chat that is in the URL is neither looked up nor remounted', () => {
        renderView();

        act(() => lastChatPage().toChat('agent1', 'rootX'));
        const chatPage = screen.getByTestId('chat-page');

        expect(history.location.pathname).toBe(`${DM}/rootX`);
        expect(mockGetPost).not.toHaveBeenCalled();
        expect(screen.getByTestId('chat-page')).toBe(chatPage);
        expect(chatPage.textContent).toBe('rootX');
    });

    test.each([
        ['a chat', 'root1', 'root1'],
        ['a reply, which opens its thread', 'reply1', 'root1'],
    ])('the host navigating the DM URL to %s opens it', (_name, postId, expectedRoot) => {
        renderView();

        navigate(`${DM}/${postId}`);
        expect(screen.getByTestId('chat-page').textContent).toBe(expectedRoot);
    });

    test('the host navigating to the bare DM URL starts a new chat', () => {
        renderView('root1');
        expect(screen.getByTestId('chat-page').textContent).toBe('root1');

        navigate(DM);
        expect(screen.getByTestId('chat-page').textContent).toBe('new chat');
    });

    test.each(['v4', 'v5'] as const)('the host tidying a permalink\'s URL keeps its chat open (history %s)', (_name) => {
        const {rerender} = renderView('reply1');
        expect(screen.getByTestId('chat-page').textContent).toBe('root1');

        replace(DM, _name);
        expect(screen.getByTestId('chat-page').textContent).toBe('root1');

        // The host keeps focusedPostId; a new chat afterwards still starts one.
        rerender(
            <Provider store={makeStore()}>
                <AgentDMChannelView
                    channel={channel}
                    channelId='dm1'
                    teamId='team1'
                    focusedPostId='reply1'
                />
            </Provider>,
        );
        navigate(DM);
        expect(screen.getByTestId('chat-page').textContent).toBe('new chat');
    });

    test('the same chat can be opened again after a new chat, from outside the view', () => {
        renderView('root1');

        act(() => lastChatPage().toChat('agent1'));
        expect(screen.getByTestId('chat-page').textContent).toBe('new chat');

        navigate(`${DM}/root1`);
        expect(screen.getByTestId('chat-page').textContent).toBe('root1');
    });

    test('outside the Channels route (a popout) the host\'s focusedPostId opens its chat', () => {
        renderView('reply1', '/_popout/channel/team1/messages/@agent1-name');

        expect(screen.getByTestId('chat-page').textContent).toBe('root1');
    });

    test('picking a chat puts it in the DM URL, and a new chat takes it out', () => {
        renderView();

        act(() => lastChatPage().toChat('agent1', 'root1'));
        expect(history.push).toHaveBeenLastCalledWith('/team1/messages/@agent1-name/root1');
        expect(screen.getByTestId('chat-page').textContent).toBe('root1');

        act(() => lastChatPage().toChat('agent1'));
        expect(history.push).toHaveBeenLastCalledWith('/team1/messages/@agent1-name');
        expect(screen.getByTestId('chat-page').textContent).toBe('new chat');

        // Already there: no duplicate history entry.
        act(() => lastChatPage().toChat('agent1'));
        expect(history.push).toHaveBeenCalledTimes(2);
    });

    test('outside the Channels route (a popout) the chat changes without touching the URL', () => {
        renderView(undefined, '/_popout/channel/team1/messages/@agent1-name'); // eslint-disable-line no-undefined

        act(() => lastChatPage().toChat('agent1', 'root1'));
        expect(screen.getByTestId('chat-page').textContent).toBe('root1');
        expect(history.push).not.toHaveBeenCalled();
    });

    test.each([
        ['in the Channels app: that agent\'s DM', '/team1/messages/@agent1-name/root1', '/team1/messages/@agent2-name'],
        ['in a popout: the Agents product', '/_popout/channel/team1/messages/@agent1-name', buildChatPath('agent2')],
    ])('switching to another agent goes to %s', (_name, pathname, expected) => {
        renderView(undefined, pathname); // eslint-disable-line no-undefined

        act(() => lastChatPage().toChat('agent2'));
        expect(history.push).toHaveBeenCalledWith(expected);
        expect(screen.getByTestId('chat-page').textContent).toBe('new chat');
    });

    test('View all agents goes to the Agents product', async () => {
        renderView();
        act(() => lastChatPage().toAll());
        await waitFor(() => expect(history.push).toHaveBeenCalledWith(ALL_AGENTS_PATH));
    });
});

describe('dmChannelPath', () => {
    test.each([
        ['DM URL', '/team1/messages/@agent', '/team1/messages/@agent'],
        ['DM URL with a post', '/team1/messages/@agent/abc', '/team1/messages/@agent'],
        ['trailing slash', '/team1/messages/@agent/', '/team1/messages/@agent'],
        ['channel URL', '/team1/channels/town-square', ''],
        ['popout', '/_popout/channel/team1/messages/@agent', ''],
        ['product route', '/plug/x/agents/chat/a', ''],
        ['missing', '', ''],
    ])('%s', (_name, pathname, expected) => {
        expect(dmChannelPath(pathname)).toBe(expected);
    });
});
