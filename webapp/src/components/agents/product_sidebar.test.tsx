// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';
import {Provider} from 'react-redux';
import {createStore} from 'redux';

import {archiveConversation, getAIThreads} from '@/client';
import type {AIThread} from '@/types/ai_thread';
import PostEventListener, {requestAgentThreadsRefresh} from '@/websocket';

import ProductSidebar from './product_sidebar';
import {AGENTS_ROUTE, buildChatPath} from './use_agents_route';

jest.mock('@/client', () => ({
    getAIThreads: jest.fn(),
    archiveConversation: jest.fn(),
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
const mockArchive = archiveConversation as jest.Mock;

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

const renderSidebar = (props: Partial<React.ComponentProps<typeof ProductSidebar>> = {}) => {
    const onSelectAll = jest.fn();
    const onSelectPrompts = jest.fn();
    render(
        <ProductSidebar
            activeView='all'
            onSelectAll={onSelectAll}
            onSelectPrompts={onSelectPrompts}
            {...props}
        />,
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

    test('renders the extension point children', () => {
        renderSidebar({children: <div>{'Your agents section'}</div>});
        expect(screen.getByText('Your agents section')).toBeTruthy();
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

    const renderWithStore = (initialPath: string) => {
        const history = makeHistory(initialPath);
        window.WebappUtils = {browserHistory: history} as any;
        render(
            <IntlProvider
                locale='en'
                onError={() => null}
            >
                <Provider store={createStore(() => ({}))}>
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
        mockArchive.mockReset();
    });

    test('lists fetched threads and navigates on click', async () => {
        mockGetAIThreads.mockResolvedValue([thread('1', 'Older'), thread('2', 'Newest')]);
        const history = renderWithStore(buildChatPath('a', 'root_1'));
        await screen.findByRole('button', {name: 'Newest'});
        expect(screen.getByRole('button', {name: 'Older'}).getAttribute('aria-current')).toBe('page');

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

    test('refreshes when a reply finishes, and on chat-page refresh requests, not on other updates', async () => {
        mockGetAIThreads.mockResolvedValue([]);
        renderWithStore(AGENTS_ROUTE);
        await waitFor(() => expect(mockGetAIThreads).toHaveBeenCalledTimes(1));

        const events = new PostEventListener();
        const msg = (control: string) => ({data: {post_id: 'p', control}} as any);
        await act(async () => {
            events.handlePostUpdateWebsockets(msg('start'));
        });
        expect(mockGetAIThreads).toHaveBeenCalledTimes(1);

        await act(async () => {
            events.handlePostUpdateWebsockets(msg('end'));
        });
        await waitFor(() => expect(mockGetAIThreads).toHaveBeenCalledTimes(2));

        jest.useFakeTimers();
        await act(async () => {
            requestAgentThreadsRefresh();
        });
        expect(mockGetAIThreads).toHaveBeenCalledTimes(3);
        jest.useRealTimers();
    });

    describe('archiving a chat', () => {
        const archiveFirst = async (title: string) => {
            fireEvent.click(await screen.findByRole('button', {name: `Actions for ${title}`}));
            fireEvent.click(await screen.findByText('Archive chat'));
            fireEvent.click(await screen.findByRole('button', {name: 'Archive'}));
        };

        const cases = [
            {name: 'open chat is archived: navigates to a new chat', path: buildChatPath('a', 'root_2'), navigates: true},
            {name: 'another chat is archived: stays put', path: buildChatPath('a', 'root_1'), navigates: false},
        ];
        test.each(cases)('$name, then refreshes', async ({path, navigates}) => {
            mockGetAIThreads.mockResolvedValue([thread('2', 'Open one'), thread('1', 'Other')]);
            mockArchive.mockResolvedValue(null);
            const history = renderWithStore(path);
            await screen.findByRole('button', {name: 'Open one'});
            await archiveFirst('Open one');
            await waitFor(() => expect(mockArchive).toHaveBeenCalledWith('2'));
            await waitFor(() => expect(mockGetAIThreads).toHaveBeenCalledTimes(2));
            if (navigates) {
                expect(history.push).toHaveBeenCalledWith(buildChatPath('a'));
            } else {
                expect(history.push).not.toHaveBeenCalled();
            }
        });

        test('failure shows an error, does not navigate or refresh', async () => {
            mockGetAIThreads.mockResolvedValue([thread('2', 'Open one')]);
            mockArchive.mockRejectedValue(new Error('server said no'));
            const history = renderWithStore(buildChatPath('a', 'root_2'));
            await screen.findByRole('button', {name: 'Open one'});
            await archiveFirst('Open one');
            expect((await screen.findByRole('alert')).textContent).toContain('Couldn\'t archive this chat');
            expect(history.push).not.toHaveBeenCalled();
            expect(mockGetAIThreads).toHaveBeenCalledTimes(1);
        });
    });
});
