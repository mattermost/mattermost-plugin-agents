// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {act, render, screen, waitFor} from '@testing-library/react';

import type {Channel} from '@mattermost/types/channels';

import {agentDMOpenChat, setAgentDMOpenChat} from '@/agent_dm_open_chat';
import manifest from '@/manifest';

import AgentDMChannelView from './agent_dm_channel_view';
import AgentDMSidebarChats from './agent_dm_sidebar_chats';

interface SectionProps {
    bots: Array<{id: string}> | null;
    activeBotId?: string;
    activeRootId?: string;
    onSelectThread: (botId: string, rootId: string) => void;
    onNewChat: (botId: string) => void;
    hideAgentRows?: boolean;
}
const mockSectionProps = jest.fn();
jest.mock('./product_sidebar', () => ({
    AgentThreadsSection: (props: SectionProps) => {
        mockSectionProps(props);
        return <div data-testid='chat-list'/>;
    },
}));

const mockGetPost = jest.fn();
jest.mock('@/client', () => ({
    getAIBots: jest.fn(() => new Promise(() => null)),
    getPost: (...args: unknown[]) => mockGetPost(...args),
}));

jest.mock('react-intl', () => ({
    ...jest.requireActual('react-intl'),
    useIntl: () => ({formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage}),
}));

jest.mock('./agent_chat_page', () => ({
    __esModule: true,
    default: () => <div data-testid='chat-page'/>,
}));

const makeBot = (id: string) => ({id, displayName: `Agent ${id}`, username: `${id}-name`, dmChannelID: ''});

// The host's store, with this plugin's open-chat slice and RECEIVED_POST for the posts it fetches.
const makeStore = () => createStore((state: any, action: any) => {
    const plugin = state[`plugins-${manifest.id}`];
    const next = {...state, [`plugins-${manifest.id}`]: {...plugin, agentDMOpenChat: agentDMOpenChat(plugin.agentDMOpenChat, action)}};
    if (action.type === 'RECEIVED_POST') {
        next.entities = {...state.entities, posts: {posts: {...state.entities.posts.posts, [action.data.id]: action.data}}};
    }
    return next;
}, {
    [`plugins-${manifest.id}`]: {bots: [makeBot('agent1')]},
    entities: {
        users: {currentUserId: 'me'},
        posts: {posts: {
            root1: {id: 'root1', root_id: ''},
            reply1: {id: 'reply1', root_id: 'root1'},
        }},
    },
} as any);
let store = makeStore();

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

const agentDM = {id: 'dm1', type: 'D', name: 'agent1__me'} as Channel;
const lastSection = (): SectionProps => mockSectionProps.mock.calls[mockSectionProps.mock.calls.length - 1][0];

const renderChats = (channel = agentDM, isCurrentChannel = true) => render(
    <Provider store={store}>
        <AgentDMSidebarChats
            channel={channel}
            currentTeamName='team1'
            isCurrentChannel={isCurrentChannel}
        />
    </Provider>,
);

beforeEach(() => {
    jest.clearAllMocks();
    store = makeStore();
    listeners.clear();
    history.location.pathname = '/team1/messages/@agent1-name';
    window.WebappUtils = {browserHistory: history} as unknown as typeof window.WebappUtils;
});

afterEach(() => {
    delete window.WebappUtils;
});

describe('AgentDMSidebarChats', () => {
    test('lists the DM agent\'s chats without an agent row', () => {
        renderChats();

        expect(screen.getByTestId('chat-list')).toBeTruthy();
        expect(lastSection()).toMatchObject({bots: [expect.objectContaining({id: 'agent1'})], activeBotId: 'agent1', activeRootId: '', hideAgentRows: true});
    });

    test('lists nothing for a DM whose agent is not loaded', () => {
        renderChats({id: 'dm2', type: 'D', name: 'agent9__me'} as Channel);

        expect(screen.queryByTestId('chat-list')).toBeNull();
    });

    test('highlights the chat the DM view has open', () => {
        renderChats();
        expect(lastSection().activeRootId).toBe('');

        act(() => {
            store.dispatch(setAgentDMOpenChat('dm1', 'root1'));
        });
        expect(lastSection().activeRootId).toBe('root1');

        act(() => {
            store.dispatch(setAgentDMOpenChat('dm1', ''));
        });
        expect(lastSection().activeRootId).toBe('');
    });

    test('highlights nothing in a DM that is not the open channel', () => {
        store.dispatch(setAgentDMOpenChat('dm1', 'root1'));
        renderChats(agentDM, false);

        expect(lastSection()).toMatchObject({activeBotId: undefined, activeRootId: ''}); // eslint-disable-line no-undefined
    });

    test('opening a chat navigates to its DM URL, and a new chat to the DM', () => {
        renderChats();

        act(() => lastSection().onSelectThread('agent1', 'root 1'));
        expect(history.push).toHaveBeenLastCalledWith('/team1/messages/@agent1-name/root%201');

        act(() => lastSection().onNewChat('agent1'));
        expect(history.push).toHaveBeenLastCalledWith('/team1/messages/@agent1-name');
    });

    describe('with the DM view', () => {
        const renderBoth = (focusedPostId: string) => render(
            <Provider store={store}>
                <AgentDMChannelView
                    channel={agentDM}
                    channelId='dm1'
                    teamId='team1'
                    focusedPostId={focusedPostId}
                />
                <AgentDMSidebarChats
                    channel={agentDM}
                    currentTeamName='team1'
                    isCurrentChannel={true}
                />
            </Provider>,
        );

        test('highlights the chat of a reply permalink the view had to fetch', async () => {
            mockGetPost.mockResolvedValue({id: 'reply9', root_id: 'root9', channel_id: 'dm1'});
            history.location.pathname = '/team1/messages/@agent1-name/reply9';
            renderBoth('reply9');

            await waitFor(() => expect(lastSection().activeRootId).toBe('root9'));
        });

        test('keeps highlighting a permalinked chat after the host drops the post id from the URL', () => {
            history.location.pathname = '/team1/messages/@agent1-name/reply1';
            renderBoth('reply1');
            expect(lastSection().activeRootId).toBe('root1');

            act(() => {
                history.location.pathname = '/team1/messages/@agent1-name';
                listeners.forEach((listener) => listener(history.location, 'REPLACE'));
            });
            expect(lastSection().activeRootId).toBe('root1');
        });

        test('follows a chat opened from the sidebar', () => {
            renderBoth('');
            act(() => lastSection().onSelectThread('agent1', 'root1'));

            expect(lastSection().activeRootId).toBe('root1');
        });
    });
});
