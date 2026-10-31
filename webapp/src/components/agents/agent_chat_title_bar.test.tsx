// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {combineReducers, createStore} from 'redux';
import {act, fireEvent, render, screen, waitFor, within} from '@testing-library/react';

import type {LLMBot} from '@/bots';
import {clearConversationCache} from '@/hooks/use_conversation';
import {resetToolPreferences} from '@/components/tool_providers/use_tool_preferences';
import manifest from '@/manifest';
import {aiThreads} from '@/redux';

import AgentChatTitleBar from './agent_chat_title_bar';

const mockRename = jest.fn();
const mockGetConversation = jest.fn();
const mockGetThreads = jest.fn();
const mockGetTools = jest.fn();
jest.mock('@/client', () => ({
    renameConversation: (...args: unknown[]) => mockRename(...args),
    getConversation: (...args: unknown[]) => mockGetConversation(...args),
    getProfilePictureUrl: (id: string) => `/users/${id}/image`,
    getAIThreads: () => mockGetThreads(),
    getUserMCPTools: () => mockGetTools(),
    getUserToolPreferences: () => Promise.resolve({disabled_servers: []}),
}));

jest.mock('@/components/rhs/context_usage_indicator', () => ({
    __esModule: true,
    default: ({conversationId}: {conversationId: string}) => <span data-testid='context-usage'>{conversationId}</span>,
}));

jest.mock('@/websocket', () => ({
    subscribeToAllPostUpdates: () => () => null,
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
const LISTED_CONVERSATION_ID = 'e'.repeat(26);
const bot = {id: 'bot1', displayName: 'Matty', lastIconUpdate: 0, dmChannelID: 'dm1', autoEnableNewMCPTools: true} as LLMBot;
const thread = (id: string, root: string, title: string, update: number, channel = 'dm1', botId = 'bot1') => ({id, channel_id: channel, bot_id: botId, root_post_id: root, title, turn_count: 1, update_at: update});

// The conversation id comes from the bot reply's props, as the plugin writes it.
// The plugin's real thread-list reducer, so the name menu lists the fetched chats.
const makeStore = (withBotReply: boolean) => createStore(combineReducers({
    [`plugins-${manifest.id}`]: combineReducers({aiThreads}),
    entities: (state = {
        general: {config: {EnableTesting: 'true', EnableDeveloper: 'true'}, license: {}},
        posts: {
            posts: {
                root1: {id: 'root1', props: {}},
                ...(withBotReply ? {reply1: {id: 'reply1', props: {conversation_id: CONVERSATION_ID}}} : {}),
            },
            postsInThread: {root1: withBotReply ? ['reply1'] : []},
        },
    }) => state,
} as any));

const renderHeader = (props: Partial<React.ComponentProps<typeof AgentChatTitleBar>> = {}, withBotReply = true) => {
    const handlers = {onNewChat: jest.fn(), onSwitchAgent: jest.fn(), onSelectChat: jest.fn(), onViewAll: jest.fn()};
    render(
        <Provider store={makeStore(withBotReply)}>
            <AgentChatTitleBar
                surface='page'
                bot={bot}
                bots={[]}
                {...handlers}
                {...props}
            />
        </Provider>,
    );
    return handlers;
};

const menuItems = () => screen.getAllByRole('menuitem').map((item) => item.textContent);
const openMenu = () => fireEvent.click(screen.getByTestId('agent-chat-menu-button'));
const openActions = async () => {
    await waitFor(() => expect(mockGetThreads).toHaveBeenCalled());
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull());
    openMenu();
    await waitFor(() => expect(menuItems()).toContain('Recent chats'));
};
const openChats = async () => {
    fireEvent.click(screen.getByRole('menuitem', {name: 'Recent chats'}));
    await waitFor(() => expect(menuItems()).toContain('Older chat'));
};

// The menu stays mounted (and its focus manager hides the rest) until its exit animation ends.
const clickItem = async (name: string) => {
    fireEvent.click(screen.getAllByRole('menuitem', {name})[0]);
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull());
};

describe('AgentChatTitleBar', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        clearConversationCache();
        resetToolPreferences();
        mockGetConversation.mockResolvedValue({id: CONVERSATION_ID, title: 'Quarterly plan', turns: []});

        // In the server's order: most recent turn first.
        mockGetThreads.mockResolvedValue([
            thread('t4', 'other-dm', 'Not in the DM', 9, 'elsewhere'),
            thread('t5', 'other-bot', 'Other bot', 9, 'dm1', 'bot2'),
            thread('d'.repeat(26), 'foreign-root', 'Not in the DM', 5, 'elsewhere'),
            thread(LISTED_CONVERSATION_ID, 'listed-root', 'Listed chat', 4),
            thread(CONVERSATION_ID, 'root1', 'Quarterly plan', 3),
            thread('t3', 'blank', '  ', 2),
            thread('t1', 'old', 'Older chat', 1),
        ]);
        mockGetTools.mockResolvedValue({servers: []});
        jest.spyOn(console, 'error').mockImplementation(() => null);
    });

    afterEach(() => {
        jest.restoreAllMocks();
    });

    test('renders the agent avatar, the level-1 name and a menu button', () => {
        renderHeader();
        expect(screen.getByRole('heading', {level: 1, name: 'Matty'})).toBeTruthy();
        expect(document.querySelector('header[aria-label="Matty"]')).toBeTruthy();
        expect(document.querySelector('header img[src="/users/bot1/image"]')).toBeTruthy();
        const trigger = screen.getByRole('button', {name: 'Matty options'});
        expect(trigger.getAttribute('aria-haspopup')).toBe('menu');
        expect(trigger.getAttribute('aria-expanded')).toBe('false');
        openMenu();
        expect(trigger.getAttribute('aria-expanded')).toBe('true');
    });

    test.each([
        {name: 'new chat state shows the agent name', props: {}, reply: true, label: 'Matty', aria: 'Matty options'},
        {name: 'open chat shows its title, keeping the agent avatar', props: {rootId: 'root1'}, reply: true, label: 'Quarterly plan', aria: 'Quarterly plan, Matty options'},
        {name: 'open chat with a blank title shows the placeholder', props: {rootId: 'blank'}, reply: false, label: 'New chat', aria: 'New chat, Matty options'},
    ])('name button: $name', async ({props, reply, label, aria}) => {
        mockGetConversation.mockResolvedValue({id: CONVERSATION_ID, title: '', turns: []});
        if (props.rootId === 'root1') {
            mockGetConversation.mockResolvedValue({id: CONVERSATION_ID, title: 'Quarterly plan', turns: []});
        }
        renderHeader(props, reply);
        await waitFor(() => expect(screen.getByRole('heading', {level: 1}).textContent).toBe(label));
        const trigger = screen.getByRole('button', {name: aria});
        expect(trigger.getAttribute('title')).toBe(label);
        expect(trigger.getAttribute('aria-haspopup')).toBe('menu');
        expect(document.querySelector('header[aria-label="Matty"] img[src="/users/bot1/image"]')).toBeTruthy();
    });

    test.each([
        {name: 'a new chat has no agent name next to the title (the title is already the agent)', props: {}, expected: null},
        {name: 'an open chat shows the agent name outside the dropdown, to its right', props: {rootId: 'root1'}, expected: 'Matty'},
    ])('agent name: $name', async ({props, expected}) => {
        mockGetConversation.mockResolvedValue({id: CONVERSATION_ID, title: 'Quarterly plan', turns: []});
        renderHeader(props, true);
        const button = await screen.findByTestId('agent-chat-menu-button');
        if (expected === null) {
            expect(screen.queryByTestId('agent-chat-agent-name')).toBeNull();
            return;
        }
        const name = await screen.findByTestId('agent-chat-agent-name');
        expect(name.textContent).toBe(expected);
        expect(button.contains(name)).toBe(false);
        expect(button.textContent).toBe('Quarterly plan');

        // To the right of the dropdown in the title row.
        expect(button.compareDocumentPosition(name) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    });

    const CHATS = ['Listed chat', 'Quarterly plan', 'New chat', 'Older chat'];
    test.each([
        {name: 'new chat, no edit access', props: {}, expected: ['Recent chats', 'View all agents']},
        {name: 'new chat, can edit', props: {onOpenSettings: jest.fn()}, expected: ['Recent chats', 'Agent settings', 'View all agents']},
        {name: 'open chat, no edit access', props: {rootId: 'root1'}, expected: ['Recent chats', 'New chat', 'Rename chat', 'View all agents']},
        {name: 'open chat, can edit', props: {rootId: 'root1', onOpenSettings: jest.fn()}, expected: ['Recent chats', 'New chat', 'Rename chat', 'Agent settings', 'View all agents']},
    ])('name menu items, chats in a submenu: $name', async ({props, expected}) => {
        renderHeader(props);
        await openActions();
        expect(menuItems()).toEqual(expected);
        expect(screen.getByRole('menuitem', {name: 'Recent chats'}).getAttribute('aria-haspopup')).toBe('menu');

        await openChats();
        expect(within(screen.getByRole('menu', {name: 'Recent chats'})).getAllByRole('menuitem').map((item) => item.textContent)).toEqual(CHATS);
    });

    test('name menu marks the active chat and selecting another chat opens it', async () => {
        const {onSelectChat} = renderHeader({rootId: 'root1'});
        await openActions();
        await openChats();
        const active = screen.getAllByRole('menuitem').filter((item) => item.getAttribute('aria-current') === 'true');
        expect(active.map((item) => item.textContent)).toEqual(['Quarterly plan']);
        await clickItem('Quarterly plan');
        expect(onSelectChat).not.toHaveBeenCalled();

        openMenu();
        await openChats();
        await clickItem('Older chat');
        expect(onSelectChat).toHaveBeenCalledWith('old');
    });

    test.each([
        {name: 'shows the agent avatar and name', compact: false, shown: true},
        {name: 'compact (under the host header of an agent DM) leaves them out', compact: true, shown: false},
    ])('open chat title: $name', async ({compact, shown}) => {
        renderHeader({rootId: 'root1', compact});
        await waitFor(() => expect(screen.getByRole('heading', {level: 1}).textContent).toBe('Quarterly plan'));
        expect(Boolean(document.querySelector('header img'))).toBe(shown);
        expect(Boolean(screen.queryByTestId('agent-chat-agent-name'))).toBe(shown);
    });

    test('name menu omits the chats submenu when the agent has no chats', async () => {
        mockGetThreads.mockResolvedValue([]);
        renderHeader({rootId: 'root1'});
        await waitFor(() => expect(mockGetThreads).toHaveBeenCalled());
        openMenu();
        expect(screen.queryByRole('menuitem', {name: 'Recent chats'})).toBeNull();
        expect(menuItems()).toEqual(['New chat', 'Rename chat', 'View all agents']);
    });

    test('name menu opens with the keyboard-reachable button and Escape returns focus', async () => {
        renderHeader({rootId: 'root1'});
        const trigger = screen.getByTestId('agent-chat-menu-button');
        trigger.focus();
        openMenu();
        expect(trigger.getAttribute('aria-expanded')).toBe('true');
        fireEvent.keyDown(screen.getByRole('menu'), {key: 'Escape'});
        await waitFor(() => expect(screen.queryByRole('menu')).toBeNull());
        expect(trigger.getAttribute('aria-expanded')).toBe('false');
        await waitFor(() => expect(document.activeElement).toBe(trigger));
    });

    describe('new chat button and context ring', () => {
        test('an open chat has New chat and the context usage ring', async () => {
            const {onNewChat} = renderHeader({rootId: 'root1'});
            fireEvent.click(screen.getByRole('button', {name: 'New chat'}));
            expect(onNewChat).toHaveBeenCalledTimes(1);
            expect(screen.getByTestId('agent-chat-new-chat-button')).toBeTruthy();
            expect((await screen.findByTestId('context-usage')).textContent).toBe(CONVERSATION_ID);
        });

        test('a new chat has neither (it already is one)', () => {
            renderHeader();
            expect(screen.queryByRole('button', {name: 'New chat'})).toBeNull();
            expect(screen.queryByTestId('context-usage')).toBeNull();
        });

        test('the RHS surface uses its own test ids and no heading', () => {
            renderHeader({surface: 'rhs', rootId: 'root1'});
            expect(screen.getByTestId('rhs-title')).toBeTruthy();
            expect(screen.getByTestId('bot-selector-rhs')).toBeTruthy();
            expect(screen.getByTestId('new-chat')).toBeTruthy();
            expect(screen.queryByRole('heading')).toBeNull();
        });
    });

    test.each([
        {name: 'Agent settings', handler: 'onOpenSettings'},
    ])('name menu $name calls its handler and closes', async ({name, handler}) => {
        const onOpenSettings = jest.fn();
        renderHeader({onOpenSettings});
        openMenu();
        await clickItem(name);
        expect({onOpenSettings}[handler as 'onOpenSettings']).toHaveBeenCalledTimes(1);
        expect(screen.getByTestId('agent-chat-menu-button').getAttribute('aria-expanded')).toBe('false');
    });

    test.each([
        {name: 'confirmed thread', reply: true, disabled: false},
        {name: 'thread without a known conversation id', reply: false, disabled: true},
    ])('name menu Rename enabled state: $name', ({reply, disabled}) => {
        renderHeader({rootId: 'root1'}, reply);
        openMenu();
        expect((screen.getByRole('menuitem', {name: 'Rename chat'}) as HTMLButtonElement).disabled).toBe(disabled);
    });

    test.each([
        {name: 'listed in this agent\'s DM, no bot post in the store', rootId: 'listed-root', disabled: false},
        {name: 'listed only outside this agent\'s DM', rootId: 'foreign-root', disabled: true},
        {name: 'not listed at all', rootId: 'unlisted-root', disabled: true},
    ])('Rename falls back to the thread list id: $name', async ({rootId, disabled}) => {
        renderHeader({rootId}, false);
        await waitFor(() => expect(mockGetThreads).toHaveBeenCalled());
        await waitFor(() => {
            openMenu();
            const item = screen.getByRole('menuitem', {name: 'Rename chat'}) as HTMLButtonElement;
            expect(item.disabled).toBe(disabled);
        });
    });

    test('name menu New chat calls onNewChat; Rename opens its dialog', async () => {
        const {onNewChat} = renderHeader({rootId: 'root1'});
        openMenu();
        await clickItem('New chat');
        expect(onNewChat).toHaveBeenCalledTimes(1);

        fireEvent.click(screen.getByTestId('agent-chat-menu-button'));
        await clickItem('Rename chat');
        expect(await screen.findByLabelText('Chat name')).toBeTruthy();
        fireEvent.click(screen.getByRole('button', {name: 'Cancel'}));
        await waitFor(() => expect(screen.queryByLabelText('Chat name')).toBeNull());
    });

    test('name menu View all agents navigates to the all-agents view', async () => {
        const {onViewAll} = renderHeader();
        openMenu();
        await clickItem('View all agents');
        expect(onViewAll).toHaveBeenCalledTimes(1);
    });

    describe('switch agent', () => {
        const other = {id: 'bot2', displayName: 'Other agent', lastIconUpdate: 0, dmChannelID: 'dm2'} as LLMBot;

        test('a new chat offers a submenu to switch agents and opens a new chat with the pick', async () => {
            const {onSwitchAgent} = renderHeader({bots: [bot, other]});
            openMenu();
            await waitFor(() => expect(menuItems()).toContain('Switch agent'));
            fireEvent.click(screen.getByRole('menuitem', {name: 'Switch agent'}));
            await waitFor(() => expect(menuItems()).toContain('Other agent'));
            await clickItem('Other agent');
            expect(onSwitchAgent).toHaveBeenCalledWith(other);
        });

        test.each([
            {name: 'an established thread', props: {rootId: 'root1', bots: [bot, other]}},
            {name: 'a new chat with only one agent', props: {bots: [bot]}},
            {name: 'a new chat with no other agents listed', props: {}},
            {name: 'a chat that is still loading (open, but its root not yet validated)', props: {inThread: true, bots: [bot, other]}},
        ])('$name does not offer it', async ({props}) => {
            renderHeader(props);
            await openActions();
            expect(menuItems()).not.toContain('Switch agent');
        });
    });

    test.each([
        {name: 'new chat state shows the tools button', props: {}, bot, shown: true},
        {name: 'open thread hides it', props: {rootId: 'root1'}, bot, shown: false},
        {name: 'service account agent hides it', props: {}, bot: {...bot, useServiceAccountAuth: true} as LLMBot, shown: false},
        {name: 'a chat still loading hides it too', props: {inThread: true}, bot, shown: false},
    ])('tools button: $name', ({props, bot: b, shown}) => {
        renderHeader({...props, bot: b});
        expect(Boolean(screen.queryByText('Tools'))).toBe(shown);
    });

    test('the tools popover lists only servers the chat\'s agent may use', async () => {
        mockGetTools.mockResolvedValue({servers: [
            {name: 'Allowed', serverOrigin: 'https://a.example', tools: [], needsOAuth: false},
            {name: 'Denied', serverOrigin: 'https://b.example', tools: [], needsOAuth: false},
        ]});
        const restricted = {...bot, autoEnableNewMCPTools: false, enabledMCPTools: [{server_origin: 'https://a.example', tool_name: 't'}]} as unknown as LLMBot;
        renderHeader({bot: restricted});
        fireEvent.click(screen.getByText('Tools'));
        expect(await screen.findByText('Allowed')).toBeTruthy();
        expect(screen.queryByText('Denied')).toBeNull();
    });

    test('rename prefills the current title, saves the trimmed title and shows it', async () => {
        mockRename.mockResolvedValue({});
        renderHeader({rootId: 'root1'});
        await waitFor(() => expect(mockGetConversation).toHaveBeenCalledWith(CONVERSATION_ID));

        await openActions();
        await clickItem('Rename chat');
        const input = await screen.findByLabelText('Chat name') as HTMLInputElement;
        await waitFor(() => expect(input.value).toBe('Quarterly plan'));

        fireEvent.change(input, {target: {value: '  Q3 plan  '}});
        await act(async () => {
            fireEvent.click(screen.getByRole('button', {name: 'Save'}));
        });
        expect(mockRename).toHaveBeenCalledWith(CONVERSATION_ID, 'Q3 plan');
        await waitFor(() => expect(screen.queryByLabelText('Chat name')).toBeNull());
        expect(screen.getByTestId('agent-chat-menu-button').textContent).toBe('Q3 plan');
    });

    test('rename failure keeps the dialog open with an error and the old title', async () => {
        mockRename.mockRejectedValue(new Error('500'));
        renderHeader({rootId: 'root1'});
        await openActions();
        await clickItem('Rename chat');
        const input = await screen.findByLabelText('Chat name');
        fireEvent.change(input, {target: {value: 'New name'}});
        await act(async () => {
            fireEvent.click(screen.getByRole('button', {name: 'Save'}));
        });
        expect(screen.getByRole('alert').textContent).toBe('Couldn\'t rename this chat. Try again.');
        expect(screen.getByLabelText('Chat name')).toBeTruthy();
        expect(screen.getByTestId('agent-chat-menu-button').textContent).toBe('Quarterly plan');
    });

    test('rename with an empty or unchanged title cannot be saved', async () => {
        renderHeader({rootId: 'root1'});
        await waitFor(() => expect(mockGetConversation).toHaveBeenCalled());
        await openActions();
        await clickItem('Rename chat');
        const input = await screen.findByLabelText('Chat name') as HTMLInputElement;
        await waitFor(() => expect(input.value).toBe('Quarterly plan'));
        const save = screen.getByRole('button', {name: 'Save'}) as HTMLButtonElement;
        expect(save.disabled).toBe(true);
        fireEvent.change(input, {target: {value: '   '}});
        expect(save.disabled).toBe(true);
    });
});
