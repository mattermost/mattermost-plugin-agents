// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';

import type {LLMBot} from '@/bots';
import {clearConversationCache} from '@/hooks/use_conversation';

import AgentChatHeader from './agent_chat_header';

const mockRename = jest.fn();
const mockArchive = jest.fn();
const mockGetConversation = jest.fn();
jest.mock('@/client', () => ({
    renameConversation: (...args: unknown[]) => mockRename(...args),
    archiveConversation: (...args: unknown[]) => mockArchive(...args),
    getConversation: (...args: unknown[]) => mockGetConversation(...args),
    getProfilePictureUrl: (id: string) => `/users/${id}/image`,
}));

const mockRequestRefresh = jest.fn();
jest.mock('@/websocket', () => ({
    requestAgentThreadsRefresh: () => mockRequestRefresh(),
}));

jest.mock('@mattermost/shared/components/tooltip', () => ({
    WithTooltip: ({children}: {children: React.ReactNode}) => <>{children}</>,
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

const CONVERSATION_ID = 'c'.repeat(26);
const bot = {id: 'bot1', displayName: 'Matty', lastIconUpdate: 0} as LLMBot;

// The conversation id comes from the bot reply's props, as the plugin writes it.
const makeStore = (withBotReply: boolean) => createStore(() => ({
    entities: {
        posts: {
            posts: {
                root1: {id: 'root1', props: {}},
                ...(withBotReply ? {reply1: {id: 'reply1', props: {conversation_id: CONVERSATION_ID}}} : {}),
            },
            postsInThread: {root1: withBotReply ? ['reply1'] : []},
        },
    },
}));

const renderHeader = (props: Partial<React.ComponentProps<typeof AgentChatHeader>> = {}, withBotReply = true) => {
    const handlers = {onNewChat: jest.fn(), onArchived: jest.fn()};
    render(
        <Provider store={makeStore(withBotReply)}>
            <AgentChatHeader
                bot={bot}
                {...handlers}
                {...props}
            />
        </Provider>,
    );
    return handlers;
};

const openMenu = () => fireEvent.click(screen.getByRole('button', {name: 'Matty'}));

// The menu stays mounted (and its focus manager hides the rest) until its exit animation ends.
const clickItem = async (name: string) => {
    fireEvent.click(screen.getByRole('menuitem', {name}));
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull());
};
const menuItems = () => screen.getAllByRole('menuitem').map((item) => item.textContent);

describe('AgentChatHeader', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        clearConversationCache();
        mockGetConversation.mockResolvedValue({id: CONVERSATION_ID, title: 'Quarterly plan', turns: []});
        jest.spyOn(console, 'error').mockImplementation(() => null);
    });

    afterEach(() => {
        jest.restoreAllMocks();
    });

    test('the agent name is the level-1 heading and opens the menu', () => {
        renderHeader();
        expect(screen.getByRole('heading', {level: 1, name: 'Matty'})).toBeTruthy();
        const trigger = screen.getByRole('button', {name: 'Matty'});
        expect(trigger.getAttribute('aria-haspopup')).toBe('menu');
        expect(trigger.getAttribute('aria-expanded')).toBe('false');
        openMenu();
        expect(trigger.getAttribute('aria-expanded')).toBe('true');
    });

    test.each([
        {name: 'open thread shows the chat title', props: {rootId: 'root1'}, reply: true, title: 'Quarterly plan'},
        {name: 'new chat state shows no title', props: {}, reply: true, title: null},
    ])('ChannelHeader landmark: $name', async ({props, reply, title}) => {
        renderHeader(props, reply);
        const header = document.querySelector('header[aria-label="Matty"]');
        expect(header).toBeTruthy();
        if (title) {
            expect(await screen.findByText(title)).toBeTruthy();
        } else {
            expect(header!.textContent).toBe('Matty');
        }
    });

    test.each([
        {name: 'new chat state, no edit access', props: {}, reply: true, expected: ['New chat']},
        {name: 'new chat state, can edit', props: {onOpenSettings: jest.fn()}, reply: true, expected: ['New chat', 'Agent settings']},
        {name: 'open thread, no edit access', props: {rootId: 'root1'}, reply: true, expected: ['New chat', 'Rename chat', 'Archive chat']},
        {name: 'open thread, can edit', props: {rootId: 'root1', onOpenSettings: jest.fn()}, reply: true, expected: ['New chat', 'Agent settings', 'Rename chat', 'Archive chat']},
        {name: 'thread with no agent reply yet (no conversation id)', props: {rootId: 'root1'}, reply: false, expected: ['New chat']},
    ])('menu items: $name', ({props, reply, expected}) => {
        renderHeader(props, reply);
        openMenu();
        expect(menuItems()).toEqual(expected);
    });

    test.each([
        {name: 'New chat', handler: 'onNewChat'},
        {name: 'Agent settings', handler: 'onOpenSettings'},
    ])('$name calls its handler and closes the menu', async ({name, handler}) => {
        const onOpenSettings = jest.fn();
        const handlers = renderHeader({onOpenSettings});
        openMenu();
        await clickItem(name);
        const all: Record<string, jest.Mock> = {...handlers, onOpenSettings};
        expect(all[handler]).toHaveBeenCalledTimes(1);
        expect(screen.getByTestId('agent-chat-menu-button').getAttribute('aria-expanded')).toBe('false');
    });

    test('rename prefills the current title, saves the trimmed title and refreshes the sidebar', async () => {
        mockRename.mockResolvedValue({});
        renderHeader({rootId: 'root1'});
        await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith(CONVERSATION_ID));

        openMenu();
        await clickItem('Rename chat');
        const input = await screen.findByLabelText('Chat name') as HTMLInputElement;
        await waitFor(() => expect(input.value).toBe('Quarterly plan'));

        fireEvent.change(input, {target: {value: '  Q3 plan  '}});
        await act(async () => {
            fireEvent.click(screen.getByRole('button', {name: 'Save'}));
        });
        expect(mockRename).toHaveBeenCalledWith(CONVERSATION_ID, 'Q3 plan');
        expect(await screen.findByText('Q3 plan')).toBeTruthy();
        expect(mockRequestRefresh).toHaveBeenCalledTimes(1);
        await waitFor(() => expect(screen.queryByLabelText('Chat name')).toBeNull());
    });

    test('rename failure keeps the dialog open with an error and does not refresh', async () => {
        mockRename.mockRejectedValue(new Error('500'));
        renderHeader({rootId: 'root1'});
        openMenu();
        await clickItem('Rename chat');
        const input = await screen.findByLabelText('Chat name');
        fireEvent.change(input, {target: {value: 'New name'}});
        await act(async () => {
            fireEvent.click(screen.getByRole('button', {name: 'Save'}));
        });
        expect(screen.getByRole('alert').textContent).toBe('Couldn\'t rename this chat. Try again.');
        expect(screen.getByLabelText('Chat name')).toBeTruthy();
        expect(mockRequestRefresh).not.toHaveBeenCalled();
    });

    test('rename with an empty or unchanged title cannot be saved', async () => {
        renderHeader({rootId: 'root1'});
        await waitFor(() => expect(mockGetConversation).toHaveBeenCalled());
        openMenu();
        await clickItem('Rename chat');
        const input = await screen.findByLabelText('Chat name') as HTMLInputElement;
        await waitFor(() => expect(input.value).toBe('Quarterly plan'));
        const save = screen.getByRole('button', {name: 'Save'}) as HTMLButtonElement;
        expect(save.disabled).toBe(true);
        fireEvent.change(input, {target: {value: '   '}});
        expect(save.disabled).toBe(true);
    });

    test('archive asks for confirmation, archives, refreshes and hands off', async () => {
        mockArchive.mockResolvedValue({});
        const {onArchived} = renderHeader({rootId: 'root1'});
        openMenu();
        await clickItem('Archive chat');
        expect(mockArchive).not.toHaveBeenCalled();

        await act(async () => {
            fireEvent.click(await screen.findByRole('button', {name: 'Archive'}));
        });
        expect(mockArchive).toHaveBeenCalledWith(CONVERSATION_ID);
        expect(mockRequestRefresh).toHaveBeenCalledTimes(1);
        expect(onArchived).toHaveBeenCalledTimes(1);
    });

    test('archive failure shows an error and stays on the chat', async () => {
        mockArchive.mockRejectedValue(new Error('500'));
        const {onArchived} = renderHeader({rootId: 'root1'});
        openMenu();
        await clickItem('Archive chat');
        await act(async () => {
            fireEvent.click(await screen.findByRole('button', {name: 'Archive'}));
        });
        expect(screen.getByRole('alert').textContent).toBe('Couldn\'t archive this chat. Try again.');
        expect(onArchived).not.toHaveBeenCalled();
        expect(mockRequestRefresh).not.toHaveBeenCalled();
    });
});
