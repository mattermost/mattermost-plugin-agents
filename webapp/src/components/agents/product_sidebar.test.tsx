// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor, within} from '@testing-library/react';
import {IntlProvider} from 'react-intl';
import {Provider} from 'react-redux';
import {combineReducers, createStore} from 'redux';

import {getAIThreads, renameConversation} from '@/client';
import manifest from '@/manifest';
import {aiThreads} from '@/redux';
import type {AIThread} from '@/types/ai_thread';

import ProductSidebar from './product_sidebar';
import {AGENTS_ROUTE, buildChatPath} from './use_agents_route';

jest.mock('@/client', () => ({
    getAIThreads: jest.fn(),
    renameConversation: jest.fn(),
    getProfilePictureUrl: (id: string) => `/api/v4/users/${id}/image`,
}));
jest.mock('@/bots', () => ({
    useBotlist: () => ({bots: mockBots}),
}));
jest.mock('@mattermost/shared/components/tooltip', () => ({
    WithTooltip: ({children}: {children: React.ReactNode}) => <>{children}</>,
}));

const mockBots = [{id: 'a', displayName: 'Alpha', dmChannelID: 'dm_a'}];

const mockGetAIThreads = getAIThreads as jest.Mock;
const mockRenameConversation = renameConversation as jest.Mock;

const thread = (id: string, title: string): AIThread => ({
    id,
    channel_id: 'dm_a',
    bot_id: 'a',
    root_post_id: `root_${id}`,
    title,
    turn_count: 1,
    update_at: Number(id),
});

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const real = actual.createIntl({locale: 'en', defaultLocale: 'en', onError: () => null});
    const intl = {
        formatMessage: (descriptor: {id?: string; defaultMessage: string}, values?: Record<string, string | number>) =>
            real.formatMessage({id: descriptor.id ?? descriptor.defaultMessage, ...descriptor}, values),
    };
    return {...actual, useIntl: () => intl};
});

// The plugin's real thread-list reducer, and the host's thread read state.
const makeStore = (host: {collapsedThreads?: string; threads?: Record<string, {unread_replies: number}>} = {}) => createStore(combineReducers({
    [`plugins-${manifest.id}`]: combineReducers({aiThreads}),
    entities: (state = {
        users: {currentUserId: 'me', profiles: {me: {id: 'me', roles: 'system_user'}}},
        roles: {roles: {}},
        posts: {posts: {}},
        general: {config: {CollapsedThreads: host.collapsedThreads ?? 'always_on'}},
        preferences: {myPreferences: {}},
        threads: {threads: host.threads ?? {}},
    }) => state,
} as any));

const renderSidebar = (props: Partial<React.ComponentProps<typeof ProductSidebar>> = {}) => {
    const onSelectAll = jest.fn();
    const onSelectPrompts = jest.fn();
    mockGetAIThreads.mockResolvedValue([]);
    render(
        <Provider store={makeStore()}>
            <ProductSidebar
                activeView='all'
                onSelectAll={onSelectAll}
                onSelectPrompts={onSelectPrompts}
                {...props}
            />
        </Provider>,
    );
    return {onSelectAll, onSelectPrompts};
};

describe('ProductSidebar', () => {
    test('exposes a labelled landmark with the two nav rows as buttons', () => {
        renderSidebar();
        expect(screen.getAllByRole('navigation', {name: 'Agents'})).toHaveLength(1);
        expect(screen.getByRole('button', {name: /All agents/})).toBeTruthy();
        expect(screen.getByRole('button', {name: /Custom Prompts/})).toBeTruthy();
    });

    test('rows invoke their handlers', () => {
        const {onSelectAll, onSelectPrompts} = renderSidebar({activeView: 'prompts'});
        fireEvent.click(screen.getByRole('button', {name: /Custom Prompts/}));
        fireEvent.click(screen.getByRole('button', {name: /All agents/}));
        expect(onSelectPrompts).toHaveBeenCalledTimes(1);
        expect(onSelectAll).toHaveBeenCalledTimes(1);
    });
});

describe('ProductSidebar "Your agents" section', () => {
    const makeHistory = (initial: string) => {
        const listeners = new Set<() => void>();
        const history = {
            location: {pathname: initial},
            push: jest.fn((path: string) => {
                history.location = {pathname: path};
                listeners.forEach((l) => l());
            }),
            listen: (l: () => void) => {
                listeners.add(l);
                return () => listeners.delete(l);
            },
        };
        return history;
    };

    const renderWithStore = (initialPath: string, store = makeStore()) => {
        const history = makeHistory(initialPath);
        window.WebappUtils = {browserHistory: history} as any;
        render(
            <IntlProvider
                locale='en'
                onError={() => null}
            >
                <Provider store={store}>
                    <ProductSidebar
                        activeView='all'
                        onSelectAll={jest.fn()}
                        onSelectPrompts={jest.fn()}
                    />
                </Provider>
            </IntlProvider>,
        );
        return history;
    };

    afterEach(() => {
        delete window.WebappUtils;
        mockGetAIThreads.mockReset();
        mockRenameConversation.mockReset();
    });

    test('lists fetched threads and navigates on click', async () => {
        mockGetAIThreads.mockResolvedValue([thread('1', 'Older'), thread('2', 'Newest')]);
        const history = renderWithStore(buildChatPath('a', 'root_1'));
        await screen.findByRole('button', {name: 'Newest'});
        expect(screen.getByRole('button', {name: 'Older'}).getAttribute('aria-current')).toBe('true');

        fireEvent.click(screen.getByRole('button', {name: 'Newest'}));
        expect(history.push).toHaveBeenCalledWith(buildChatPath('a', 'root_2'));

        fireEvent.click(screen.getByRole('button', {name: 'New chat with Alpha'}));
        expect(history.push).toHaveBeenCalledWith(buildChatPath('a'));
    });

    test('surfaces a fetch failure', async () => {
        mockGetAIThreads.mockRejectedValue(new Error('nope'));
        renderWithStore(AGENTS_ROUTE);
        expect((await screen.findByRole('alert')).textContent).toContain('Couldn\'t load your chats.');
    });

    test.each([
        {name: 'collapsed reply threads on: the host\'s unread thread shows the dot', collapsedThreads: 'always_on', path: AGENTS_ROUTE, dots: ['Older']},
        {name: 'the open chat never shows it', collapsedThreads: 'always_on', path: buildChatPath('a', 'root_1'), dots: []},
        {name: 'collapsed reply threads off: no dot (the host keeps no thread read state)', collapsedThreads: 'disabled', path: AGENTS_ROUTE, dots: []},
    ])('unread dot: $name', async ({collapsedThreads, path, dots}) => {
        mockGetAIThreads.mockResolvedValue([thread('1', 'Older'), thread('2', 'Newest')]);
        renderWithStore(path, makeStore({collapsedThreads, threads: {root_1: {unread_replies: 2}, root_2: {unread_replies: 0}}}));
        await screen.findByRole('button', {name: 'Newest'});

        const unread = screen.queryAllByTestId('thread-unread').map((dot) => dot.closest('li')?.textContent ?? '');
        expect(unread).toHaveLength(dots.length);
        dots.forEach((title, i) => expect(unread[i]).toContain(title));
    });

    test('renaming a chat shows the new title at once, and restores it when the rename fails', async () => {
        mockGetAIThreads.mockResolvedValue([thread('1', 'Older')]);
        let fail!: (e: Error) => void;
        mockRenameConversation.mockReturnValue(new Promise((_resolve, reject) => {
            fail = reject;
        }));
        renderWithStore(AGENTS_ROUTE);
        await screen.findByRole('button', {name: 'Older'});

        fireEvent.click(screen.getByRole('button', {name: 'Actions for Older'}));
        fireEvent.click(await screen.findByText('Rename chat'));
        const input = await screen.findByLabelText('Chat name');
        fireEvent.change(input, {target: {value: 'Renamed'}});
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        // The dialog hides the page from the accessibility tree while it is open.
        const list = screen.getByRole('list', {name: 'Alpha chats', hidden: true});
        await waitFor(() => expect(within(list).getByRole('button', {name: 'Renamed', hidden: true})).toBeTruthy());
        expect(mockRenameConversation).toHaveBeenCalledWith('1', 'Renamed');

        fail(new Error('nope'));
        await waitFor(() => expect(within(list).getByRole('button', {name: 'Older', hidden: true})).toBeTruthy());
        expect((await screen.findByRole('alert')).textContent).toBe('Couldn\'t rename this chat. Try again.');
    });
});
