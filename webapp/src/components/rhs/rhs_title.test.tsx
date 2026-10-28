// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {act, fireEvent, render, screen, within} from '@testing-library/react';

import manifest from '@/manifest';
import {PREFERENCE_CATEGORY_AGENTS, PREFERENCE_NAME_SELECTED_AGENT} from '@/bots';

import RHSTitle from './rhs_title';

const mockSavePreferences = jest.fn();
jest.mock('@/client', () => ({
    getProfilePictureUrl: (id: string) => `/users/${id}/image`,
    getAIBots: jest.fn(() => Promise.resolve(null)),
    getUserMCPTools: jest.fn(() => Promise.resolve({servers: []})),
    getUserToolPreferences: jest.fn(() => Promise.resolve({disabled_servers: []})),
    savePreferences: (...args: unknown[]) => mockSavePreferences(...args),
}));

jest.mock('@mattermost/shared/components/tooltip', () => ({
    WithTooltip: ({children}: {children: React.ReactNode}) => <>{children}</>,
}));

// The popover's own behavior is covered by tool_provider_popover.test.tsx.
jest.mock('./tool_provider_popover', () => ({
    __esModule: true,
    default: () => <button type='button'>{'Tools'}</button>,
}));

// The name menu's own data: this agent's chats and the open chat's conversation.
let mockThreads: Array<{id: string; root_post_id: string; title: string}> = [];
jest.mock('@/hooks/use_agent_threads', () => ({
    useAgentThreads: (bots: Array<{id: string}>) => ({threadsByBot: {[bots[0].id]: mockThreads}, refresh: jest.fn(), refreshWithTitleRetry: jest.fn()}),
}));
jest.mock('@/hooks/use_conversation', () => ({
    useConversation: () => ({conversation: null}),
}));
jest.mock('@/websocket', () => ({
    subscribeToAllPostUpdates: () => () => null,
    onAgentThreadsRefreshRequested: () => () => null,
    requestAgentThreadsRefresh: jest.fn(),
}));

jest.mock('@/hooks/use_conversation_id_for_thread', () => ({
    useConversationIdForThread: (rootId: string) => (rootId ? 'conv1' : ''),
}));

jest.mock('./context_usage_indicator', () => ({
    __esModule: true,
    default: ({conversationId}: {conversationId: string}) => <span data-testid='context-usage'>{conversationId}</span>,
}));

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string>) => (
            defaultMessage.replace(/\{(\w+)\}/g, (_m, k) => values?.[k] ?? '')
        ),
    };
    return {...actual, useIntl: () => intl, FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage};
});

const pluginKey = `plugins-${manifest.id}`;
const preferenceKey = `${PREFERENCE_CATEGORY_AGENTS}--${PREFERENCE_NAME_SELECTED_AGENT}`;

const makeBot = (id: string, displayName: string): {id: string; displayName: string; username: string; lastIconUpdate: number; dmChannelID: string; useServiceAccountAuth?: boolean} => ({
    id,
    displayName,
    username: id,
    lastIconUpdate: 0,
    dmChannelID: `dm-${id}`,
});

// A reducer that applies the preference the title writes, so the title re-renders.
const dispatched: Array<{type: string; postId?: string}> = [];

const makeStore = (bots: ReturnType<typeof makeBot>[] | null, selected: string, rhs: {selectedPostId?: string; rhsTab?: string} = {}) => {
    const initial = {
        [pluginKey]: {bots, ...rhs},
        entities: {
            users: {currentUserId: 'me'},
            preferences: {myPreferences: selected ? {[preferenceKey]: {value: selected}} : {}},
        },
    };
    const reducer = (state: any = initial, action: any) => {
        dispatched.push(action);
        if (action.type === 'RECEIVED_PREFERENCES') {
            const pref = action.data[0];
            return {...state, entities: {...state.entities, preferences: {myPreferences: {[preferenceKey]: {value: pref.value}}}}};
        }
        return state;
    };
    return createStore(reducer);
};

const renderTitle = (bots: ReturnType<typeof makeBot>[] | null, selected: string, rhs: {selectedPostId?: string; rhsTab?: string} = {}) => render(
    <Provider store={makeStore(bots, selected, rhs)}>
        <RHSTitle/>
    </Provider>,
);

const two = [makeBot('a', 'Alpha'), makeBot('b', 'Beta')];

describe('RHSTitle', () => {
    beforeEach(() => {
        mockSavePreferences.mockReset();
        mockSavePreferences.mockResolvedValue(true);
        dispatched.length = 0;
        mockThreads = [];
    });

    const rhsActions = () => dispatched.filter((a) => a.type === 'SELECT_AI_POST' || a.type === 'AI_RHS_NEW_CHAT');

    test.each([
        ['bots still loading', null],
        ['no agents', []],
    ])('falls back to the generic title with %s', (_name, bots) => {
        renderTitle(bots, '');

        expect(screen.getByText('Agents')).not.toBeNull();
        expect(screen.queryByRole('button', {name: /options$/})).toBeNull();
    });

    test.each([
        {name: 'a single agent', bots: [makeBot('a', 'Alpha')], selected: 'a', label: 'Alpha', switchOffered: false},
        {name: 'several agents', bots: two, selected: 'b', label: 'Beta', switchOffered: true},
    ])('$name: the title is the agent\'s menu button (the product chat header\'s menu)', ({bots, selected, label, switchOffered}) => {
        renderTitle(bots, selected);

        const button = screen.getByRole('button', {name: `${label} options`});
        expect(button.textContent).toBe(label);
        expect(button.getAttribute('aria-haspopup')).toBe('menu');
        expect(button.getAttribute('aria-expanded')).toBe('false');
        expect(screen.queryByText('Agents')).toBeNull();
        expect(document.querySelector('h1,h2,h3')).toBeNull();

        fireEvent.click(button);
        expect(button.getAttribute('aria-expanded')).toBe('true');
        expect(screen.queryByRole('menuitem', {name: 'Switch agent'}) !== null).toBe(switchOffered);
        expect(screen.getByRole('menuitem', {name: 'View all agents'})).not.toBeNull();
    });

    test('a new chat\'s menu lists recent chats and has no chat actions; an open chat adds them', () => {
        mockThreads = [{id: 'c1', root_post_id: 'root1', title: 'Quarterly plan'}];

        const {unmount} = renderTitle(two, 'a');
        fireEvent.click(screen.getByTestId('bot-selector-rhs'));
        expect(screen.getAllByRole('menuitem').map((i) => i.textContent)).toEqual(['Switch agent', 'Recent chats', 'View all agents']);
        unmount();

        renderTitle(two, 'a', {selectedPostId: 'root1'});
        fireEvent.click(screen.getByTestId('bot-selector-rhs'));

        // In an open chat the agent picker is the New chat row, in place of Switch agent.
        expect(screen.getAllByRole('menuitem').map((i) => i.textContent)).toEqual(['New chat', 'Recent chats', 'Rename chat', 'Archive chat', 'View all agents']);
    });

    test('with a single agent an open chat has the plain New chat item', () => {
        renderTitle([makeBot('a', 'Alpha')], 'a', {selectedPostId: 'root1'});

        fireEvent.click(screen.getByTestId('bot-selector-rhs'));

        expect(screen.getAllByRole('menuitem').map((i) => i.textContent)).toEqual(['New chat', 'Rename chat', 'Archive chat', 'View all agents']);
    });

    test('choosing a recent chat opens it in the RHS', () => {
        mockThreads = [{id: 'c1', root_post_id: 'root1', title: 'Quarterly plan'}];
        renderTitle(two, 'a');

        fireEvent.click(screen.getByTestId('bot-selector-rhs'));
        fireEvent.click(screen.getByRole('menuitem', {name: 'Recent chats'}));
        fireEvent.click(screen.getByRole('menuitem', {name: 'Quarterly plan'}));

        expect(dispatched.filter((a) => a.type === 'SELECT_AI_POST')).toEqual([{type: 'SELECT_AI_POST', postId: 'root1'}]);
    });

    test('Switch agent lists the agents, and choosing one saves it and starts a new chat with it', () => {
        renderTitle(two, 'a');

        fireEvent.click(screen.getByTestId('bot-selector-rhs'));
        fireEvent.click(screen.getByRole('menuitem', {name: 'Switch agent'}));
        const submenu = screen.getByRole('menu', {name: 'Switch agent'});
        expect(within(submenu).getAllByRole('menuitem').map((i) => i.textContent)).toEqual(['Alpha', 'Beta']);

        fireEvent.click(within(submenu).getByRole('menuitem', {name: 'Beta'}));

        expect(mockSavePreferences).toHaveBeenCalledWith('me', [expect.objectContaining({name: PREFERENCE_NAME_SELECTED_AGENT, value: 'b'})]);
        expect(screen.getByTestId('bot-selector-rhs').textContent).toBe('Beta');
        expect(rhsActions()).toEqual([
            {type: 'SELECT_AI_POST', postId: ''},
            {type: 'AI_RHS_NEW_CHAT'},
        ]);
    });

    test('choosing the current agent changes nothing and just closes the menu', () => {
        renderTitle(two, 'a');

        fireEvent.click(screen.getByTestId('bot-selector-rhs'));
        fireEvent.click(screen.getByRole('menuitem', {name: 'Switch agent'}));
        fireEvent.click(within(screen.getByRole('menu', {name: 'Switch agent'})).getByRole('menuitem', {name: 'Alpha'}));

        expect(mockSavePreferences).not.toHaveBeenCalled();
        expect(rhsActions()).toEqual([]);
        expect(screen.getByTestId('bot-selector-rhs').getAttribute('aria-expanded')).toBe('false');
    });

    test.each([
        {name: 'another agent', pick: 'Beta'},
        {name: 'the same agent', pick: 'Alpha'},
    ])('in an open chat, New chat > $name starts a new chat with it', ({pick}) => {
        renderTitle(two, 'a', {selectedPostId: 'root1'});

        fireEvent.click(screen.getByTestId('bot-selector-rhs'));
        fireEvent.click(screen.getByRole('menuitem', {name: 'New chat'}));
        fireEvent.click(within(screen.getByRole('menu', {name: 'New chat'})).getByRole('menuitem', {name: pick}));

        expect(rhsActions()).toEqual([
            {type: 'SELECT_AI_POST', postId: ''},
            {type: 'AI_RHS_NEW_CHAT'},
        ]);
    });

    test('opens from the keyboard and closes with Escape', async () => {
        renderTitle(two, 'a');
        const button = screen.getByRole('button', {name: 'Alpha options'});

        // A native button turns Enter/Space into a click; the click is the keyboard path.
        button.focus();
        expect(document.activeElement).toBe(button);
        fireEvent.click(button);
        expect(button.getAttribute('aria-expanded')).toBe('true');

        await act(async () => {
            fireEvent.keyDown(document.activeElement ?? document.body, {key: 'Escape'});
        });
        expect(button.getAttribute('aria-expanded')).toBe('false');
    });

    describe('Back and tools', () => {
        const backButton = () => screen.queryByRole('button', {name: 'Back to chat history'});
        const toolsButton = () => screen.queryByRole('button', {name: 'Tools'});

        test.each([
            {name: 'new chat', rhs: {}, back: true, tools: true},
            {name: 'an open thread', rhs: {selectedPostId: 'root1'}, back: true, tools: false},
            {name: 'the chat history', rhs: {rhsTab: 'threads'}, back: false, tools: false},
        ])('on $name: back=$back, tools=$tools', ({rhs, back, tools}) => {
            renderTitle(two, 'a', rhs);

            expect(backButton() !== null).toBe(back);
            expect(toolsButton() !== null).toBe(tools);
        });

        test.each([
            {name: 'new chat', rhs: {}, newChat: false, ring: false},
            {name: 'an open thread', rhs: {selectedPostId: 'root1'}, newChat: true, ring: true},
            {name: 'the chat history', rhs: {rhsTab: 'threads'}, newChat: true, ring: false},
        ])('New chat button and context ring on $name: newChat=$newChat, ring=$ring', ({rhs, newChat, ring}) => {
            renderTitle(two, 'a', rhs);

            expect(screen.queryByTestId('new-chat') !== null).toBe(newChat);
            expect(screen.queryByTestId('context-usage') !== null).toBe(ring);
        });

        test('New chat leaves the open chat for a fresh one', () => {
            renderTitle(two, 'a', {selectedPostId: 'root1'});

            fireEvent.click(screen.getByTestId('new-chat'));

            expect(dispatched.filter((a) => a.type === 'SELECT_AI_POST' || a.type === 'AI_RHS_SET_TAB')).toEqual([
                {type: 'SELECT_AI_POST', postId: ''},
                {type: 'AI_RHS_SET_TAB', tab: 'new'},
            ]);
        });

        test('the chat history has the generic Agents title: no agent menu, no Back, no tools', () => {
            renderTitle(two, 'a', {rhsTab: 'threads'});

            expect(screen.getByText('Agents')).not.toBeNull();
            expect(screen.queryByRole('button', {name: /options$/})).toBeNull();
            expect(screen.queryByText('Alpha')).toBeNull();
            expect(screen.queryByText('Chat history')).toBeNull();
        });

        test('Back leaves the open chat and shows the chat history', () => {
            renderTitle(two, 'a', {selectedPostId: 'root1'});

            fireEvent.click(backButton()!);

            expect(dispatched.filter((a) => a.type === 'SELECT_AI_POST' || a.type === 'AI_RHS_SET_TAB')).toEqual([
                {type: 'SELECT_AI_POST', postId: ''},
                {type: 'AI_RHS_SET_TAB', tab: 'threads'},
            ]);
        });

        test('Back and New chat use the host tooltip, not the browser\'s native one', () => {
            renderTitle(two, 'a', {selectedPostId: 'root1'});

            expect(backButton()!.getAttribute('title')).toBeNull();
            expect(screen.getByTestId('new-chat').getAttribute('title')).toBeNull();
        });

        test('Back is also there while the agents are loading or there is a single agent', () => {
            const {unmount} = renderTitle(null, '');
            expect(backButton()).not.toBeNull();
            unmount();

            renderTitle([makeBot('a', 'Alpha')], '');
            expect(backButton()).not.toBeNull();
            expect(toolsButton()).not.toBeNull();
        });

        test('no tools button for a service account agent', () => {
            renderTitle([{...makeBot('a', 'Alpha'), useServiceAccountAuth: true}, makeBot('b', 'Beta')], 'a');

            expect(toolsButton()).toBeNull();
        });
    });
});
