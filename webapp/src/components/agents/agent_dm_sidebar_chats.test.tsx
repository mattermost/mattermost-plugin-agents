// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {act, render, screen} from '@testing-library/react';

import type {Channel} from '@mattermost/types/channels';

import manifest from '@/manifest';

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

jest.mock('@/client', () => ({
    getAIBots: jest.fn(() => new Promise(() => null)),
}));

const makeBot = (id: string) => ({id, displayName: `Agent ${id}`, username: `${id}-name`, dmChannelID: ''});

const store = createStore((state: any) => state, {
    [`plugins-${manifest.id}`]: {bots: [makeBot('agent1')]},
    entities: {
        users: {currentUserId: 'me'},
        posts: {posts: {
            root1: {id: 'root1', root_id: ''},
            reply1: {id: 'reply1', root_id: 'root1'},
        }},
    },
} as any);

const listeners = new Set<() => void>();
const history = {
    location: {pathname: '/team1/messages/@agent1-name'},
    push: jest.fn((path: string) => {
        history.location.pathname = path;
        listeners.forEach((listener) => listener());
    }),
    listen: (listener: () => void) => {
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

    test.each([
        ['a chat', '/team1/messages/@agent1-name/root1', 'root1'],
        ['a reply, which belongs to its thread', '/team1/messages/@agent1-name/reply1', 'root1'],
    ])('highlights the chat open in the DM URL: %s', (_name, pathname, expected) => {
        history.location.pathname = pathname;
        renderChats();

        expect(lastSection().activeRootId).toBe(expected);
    });

    test('highlights nothing in a DM that is not the open channel', () => {
        history.location.pathname = '/team1/messages/@agent1-name/root1';
        renderChats(agentDM, false);

        expect(lastSection()).toMatchObject({activeBotId: undefined, activeRootId: ''}); // eslint-disable-line no-undefined
    });

    test('follows the host navigating to another chat', () => {
        renderChats();

        act(() => history.push('/team1/messages/@agent1-name/root1'));
        expect(lastSection().activeRootId).toBe('root1');
    });

    test('opening a chat navigates to its DM URL, and a new chat to the DM', () => {
        renderChats();

        act(() => lastSection().onSelectThread('agent1', 'root 1'));
        expect(history.push).toHaveBeenLastCalledWith('/team1/messages/@agent1-name/root%201');

        act(() => lastSection().onNewChat('agent1'));
        expect(history.push).toHaveBeenLastCalledWith('/team1/messages/@agent1-name');
    });
});
