// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';

import type {Channel} from '@mattermost/types/channels';

import {setAgentDMRawView} from '@/agent_dm_view';
import manifest from '@/manifest';

import AgentDMChannelView, {dmChannelPath} from './agent_dm_channel_view';
import {ALL_AGENTS_PATH, buildChatPath} from './use_agents_route';

const mockGetPost = jest.fn();
jest.mock('@/client', () => ({
    getPost: (...args: unknown[]) => mockGetPost(...args),
    getAIBots: jest.fn(() => new Promise(() => null)),
    savePreferences: jest.fn(),
}));

// The chat pane and the chats list have their own tests; here only what the view hands them.
interface ChatPageProps {
    botId: string;
    rootId?: string;
    toChat: (botId: string, rootId?: string) => void;
    toAll: () => void;
    compactHeader?: boolean;
}
const mockChatPageProps = jest.fn();
jest.mock('./agent_chat_page', () => ({
    __esModule: true,
    default: (props: ChatPageProps) => {
        mockChatPageProps(props);
        return <div data-testid='chat-page'>{props.rootId || 'new chat'}</div>;
    },
}));

interface SectionProps {
    bots: Array<{id: string}> | null;
    activeBotId?: string;
    activeRootId?: string;
    onSelectThread: (botId: string, rootId: string) => void;
    onNewChat: (botId: string) => void;
}
const mockSectionProps = jest.fn();
jest.mock('./product_sidebar', () => ({
    AgentThreadsSection: (props: SectionProps) => {
        mockSectionProps(props);
        return <div data-testid='chat-list'/>;
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

// Every action the view dispatches.
const dispatched: unknown[] = [];

const makeStore = () => createStore((state: any, action: unknown) => {
    dispatched.push(action);
    return state;
}, {
    [pluginKey]: {bots: [makeBot('agent1'), makeBot('agent2')]},
    entities: {
        users: {currentUserId: 'me'},
        posts: {posts},
        preferences: {myPreferences: {}},
    },
} as any);

const channel = {id: 'dm1', type: 'D', name: 'agent1__me'} as Channel;

const history = {
    location: {pathname: '/team1/messages/@agent1-name'},
    push: jest.fn((path: string) => {
        history.location.pathname = path;
    }),
};

const renderView = (focusedPostId?: string) => {
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
    const result = render(view(focusedPostId));
    return {...result, setFocusedPost: (id?: string) => result.rerender(view(id))};
};

const lastChatPage = (): ChatPageProps => mockChatPageProps.mock.calls[mockChatPageProps.mock.calls.length - 1][0];
const lastSection = (): SectionProps => mockSectionProps.mock.calls[mockSectionProps.mock.calls.length - 1][0];

beforeEach(() => {
    jest.clearAllMocks();
    dispatched.length = 0;
    history.location.pathname = '/team1/messages/@agent1-name';
    window.WebappUtils = {browserHistory: history} as unknown as typeof window.WebappUtils;
});

afterEach(() => {
    delete window.WebappUtils;
});

describe('AgentDMChannelView', () => {
    test('opens a new chat with the DM agent, its chats beside it', () => {
        renderView();

        expect(screen.getByTestId('chat-page').textContent).toBe('new chat');
        expect(lastChatPage()).toMatchObject({botId: 'agent1', compactHeader: true});
        expect(lastSection()).toMatchObject({bots: [expect.objectContaining({id: 'agent1'})], activeBotId: 'agent1', activeRootId: ''});
    });

    test.each([
        ['a root post', 'root1', 'root1'],
        ['a reply opens its thread', 'reply1', 'root1'],
    ])('a permalink to %s opens that chat', (_name, focusedPostId, expectedRoot) => {
        renderView(focusedPostId);

        expect(screen.getByTestId('chat-page').textContent).toBe(expectedRoot);
        expect(lastSection().activeRootId).toBe(expectedRoot);
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

    test('a chat the user picks wins over a permalink lookup that resolves later', async () => {
        let resolve: (post: unknown) => void = () => null;
        mockGetPost.mockReturnValue(new Promise((r) => {
            resolve = r;
        }));
        renderView('reply9');

        act(() => lastSection().onSelectThread('agent1', 'root1'));
        expect(screen.getByTestId('chat-page').textContent).toBe('root1');

        await act(async () => resolve({id: 'reply9', root_id: 'root9', channel_id: 'dm1'}));
        expect(screen.getByTestId('chat-page').textContent).toBe('root1');
    });

    test('a later permalink in the same DM opens its chat', () => {
        const {setFocusedPost} = renderView();
        act(() => lastSection().onNewChat('agent1'));

        setFocusedPost('reply1');
        expect(screen.getByTestId('chat-page').textContent).toBe('root1');
    });

    test('picking a chat puts it in the DM URL, and a new chat takes it out', () => {
        renderView();

        act(() => lastSection().onSelectThread('agent1', 'root1'));
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
        history.location.pathname = '/_popout/channel/team1/messages/@agent1-name';
        renderView();

        act(() => lastSection().onSelectThread('agent1', 'root1'));
        expect(screen.getByTestId('chat-page').textContent).toBe('root1');
        expect(history.push).not.toHaveBeenCalled();
    });

    test.each([
        ['in the Channels app: that agent\'s DM', '/team1/messages/@agent1-name/root1', '/team1/messages/@agent2-name'],
        ['in a popout: the Agents product', '/_popout/channel/team1/messages/@agent1-name', buildChatPath('agent2')],
    ])('switching to another agent goes to %s', (_name, pathname, expected) => {
        history.location.pathname = pathname;
        renderView();

        act(() => lastChatPage().toChat('agent2'));
        expect(history.push).toHaveBeenCalledWith(expected);
        expect(screen.getByTestId('chat-page').textContent).toBe('new chat');
    });

    test('View as messages switches this DM to the plain messages', () => {
        renderView();

        fireEvent.click(screen.getByRole('button', {name: 'View as messages'}));
        expect(dispatched).toContainEqual(setAgentDMRawView('dm1', true));
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
