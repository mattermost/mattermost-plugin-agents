// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor, within} from '@testing-library/react';

import {IntlProvider} from 'react-intl';

import type {LLMBot} from '@/bots';
import type {AIThread} from '@/types/ai_thread';

import AgentThreadsList, {THREADS_PAGE_SIZE} from './agent_threads_list';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const real = actual.createIntl({locale: 'en', defaultLocale: 'en', onError: () => null});
    const intl = {
        formatMessage: (descriptor: {id?: string; defaultMessage: string}, values?: Record<string, string | number>) =>
            real.formatMessage({id: descriptor.id ?? descriptor.defaultMessage, ...descriptor}, values),
    };
    return {...actual, useIntl: () => intl};
});

jest.mock('@/client', () => ({
    getProfilePictureUrl: (id: string) => `/api/v4/users/${id}/image`,
}));

jest.mock('@mattermost/shared/components/tooltip', () => ({
    WithTooltip: ({children}: {children: React.ReactNode}) => <>{children}</>,
}));

const bots = [
    {id: 'a', displayName: 'Alpha'},
    {id: 'b', displayName: 'Beta'},
] as LLMBot[];

const thread = (id: string, botId: string, title: string): AIThread => ({
    id,
    channel_id: `dm_${botId}`,
    bot_id: botId,
    root_post_id: `root_${id}`,
    title,
    turn_count: 1,
    update_at: 1,
});

const threadsByBot = {
    a: [thread('1', 'a', 'First chat'), thread('2', 'a', '   ')],
};

const manyThreads = (n: number) => Array.from({length: n}, (_, i) => thread(String(i + 1), 'a', `Chat ${i + 1}`));

const chatTitles = () => within(screen.getByRole('list', {name: 'Alpha chats'})).
    getAllByRole('button', {name: /^Chat \d+$/}).map((b) => b.textContent);

const renderList = (props: Partial<React.ComponentProps<typeof AgentThreadsList>> = {}) => {
    const handlers = {
        onSelectThread: jest.fn(),
        onNewChat: jest.fn(),
        onRetry: jest.fn(),
        onRenameThread: jest.fn(() => Promise.resolve()),
    };
    render(
        <IntlProvider
            locale='en'
            onError={() => null}
        >
            <AgentThreadsList
                bots={bots}
                threadsByBot={threadsByBot}
                loading={false}
                error={null}
                {...handlers}
                {...props}
            />
        </IntlProvider>,
    );
    return handlers;
};

describe('AgentThreadsList', () => {
    test('groups threads under their agent and shows a placeholder for blank titles', () => {
        renderList();
        const list = screen.getByRole('list', {name: 'Alpha chats'});
        const items = within(list).getAllByRole('button', {name: /^(First chat|New chat)$/});
        expect(items.map((b) => b.textContent)).toEqual(['First chat', 'New chat']);
        expect(screen.queryByRole('list', {name: 'Beta chats'})).toBeNull();
    });

    test('collapses and expands an agent group', () => {
        renderList();
        fireEvent.click(screen.getByRole('button', {name: 'Collapse Alpha chats'}));
        expect(screen.queryByRole('list', {name: 'Alpha chats'})).toBeNull();
        fireEvent.click(screen.getByRole('button', {name: 'Expand Alpha chats'}));
        expect(screen.getByRole('list', {name: 'Alpha chats'})).toBeTruthy();
    });

    test('only agents with threads have an expand/collapse chevron', () => {
        renderList();
        expect(screen.queryByRole('button', {name: 'Collapse Beta chats'})).toBeNull();
        expect(screen.getByRole('button', {name: 'Collapse Alpha chats'})).not.toBeNull();
    });

    const clickCases: Array<{name: string; act: () => void; expected: (h: ReturnType<typeof renderList>) => void}> = [
        {
            name: 'thread row opens that thread',
            act: () => fireEvent.click(screen.getByRole('button', {name: 'First chat'})),
            expected: (h) => expect(h.onSelectThread).toHaveBeenCalledWith('a', 'root_1'),
        },
        {
            name: 'agent with threads opens its most recent thread',
            act: () => fireEvent.click(screen.getByRole('button', {name: /^Alpha$/})),
            expected: (h) => expect(h.onSelectThread).toHaveBeenCalledWith('a', 'root_1'),
        },
        {
            name: 'agent without threads opens new chat',
            act: () => fireEvent.click(screen.getByRole('button', {name: /^Beta$/})),
            expected: (h) => expect(h.onNewChat).toHaveBeenCalledWith('b'),
        },
        {
            name: '[+] starts a new chat',
            act: () => fireEvent.click(screen.getByRole('button', {name: 'New chat with Alpha'})),
            expected: (h) => expect(h.onNewChat).toHaveBeenCalledWith('a'),
        },
    ];
    test.each(clickCases)('$name', ({act, expected}) => {
        const handlers = renderList();
        act();
        expected(handlers);
    });

    test('highlights only the active thread', () => {
        renderList({activeBotId: 'a', activeRootId: 'root_1'});
        expect(screen.getByRole('button', {name: 'First chat'}).getAttribute('aria-current')).toBe('true');
        expect(screen.getByRole('button', {name: 'New chat'}).getAttribute('aria-current')).toBeNull();
    });

    test('same root id under another agent is not highlighted', () => {
        renderList({activeBotId: 'b', activeRootId: 'root_1'});
        expect(screen.getByRole('button', {name: 'First chat'}).getAttribute('aria-current')).toBeNull();
    });

    test('shows a visible error with retry', () => {
        const handlers = renderList({error: new Error('boom'), threadsByBot: {}});
        expect(screen.getByRole('alert').textContent).toContain('Couldn\'t load your chats.');
        fireEvent.click(screen.getByRole('button', {name: 'Try again'}));
        expect(handlers.onRetry).toHaveBeenCalledTimes(1);
    });

    test('shows a loading status', () => {
        renderList({loading: true, threadsByBot: {}});
        expect(screen.getByRole('status').textContent).toContain('Loading');
    });

    test('shows the agent avatar in the row', () => {
        renderList();
        const imgs = Array.from(document.querySelectorAll('img')).map((i) => i.getAttribute('src'));
        expect(imgs).toEqual(expect.arrayContaining(['/api/v4/users/a/image', '/api/v4/users/b/image']));
    });

    describe('thread limit and Show more / Show less', () => {
        const limitCases = [
            {count: 3, shown: 3, more: false},
            {count: THREADS_PAGE_SIZE, shown: THREADS_PAGE_SIZE, more: false},
            {count: THREADS_PAGE_SIZE + 1, shown: THREADS_PAGE_SIZE, more: true},
            {count: 25, shown: THREADS_PAGE_SIZE, more: true},
        ];
        test.each(limitCases)('$count threads shows $shown, Show more=$more', ({count, shown, more}) => {
            renderList({threadsByBot: {a: manyThreads(count)}});
            expect(chatTitles()).toHaveLength(shown);
            expect(Boolean(screen.queryByRole('button', {name: 'Show more Alpha chats'}))).toBe(more);
        });

        test('Show more reveals every chat at once, then the button stays at the end of the list as Show less', () => {
            renderList({threadsByBot: {a: manyThreads(25)}});
            expect(screen.getByRole('button', {name: 'Show more Alpha chats'}).textContent).toBe('Show more');
            fireEvent.click(screen.getByRole('button', {name: 'Show more Alpha chats'}));
            expect(chatTitles()).toHaveLength(25);
            expect(screen.queryByRole('button', {name: 'Show more Alpha chats'})).toBeNull();

            const less = screen.getByRole('button', {name: 'Show less Alpha chats'});
            expect(less.textContent).toBe('Show less');

            // It is the last item of that agent's list.
            const list = document.getElementById('agent-threads-a') as HTMLElement;
            expect(list.lastElementChild?.contains(less)).toBe(true);
        });

        test('Show less folds the list back to one page and offers Show more again', async () => {
            renderList({threadsByBot: {a: manyThreads(25)}});
            fireEvent.click(screen.getByRole('button', {name: 'Show more Alpha chats'}));
            fireEvent.click(screen.getByRole('button', {name: 'Show less Alpha chats'}));

            // The extra rows fold away over the quick duration first, then the list drops them.
            expect(chatTitles()).toHaveLength(25);
            await waitFor(() => expect(chatTitles()).toHaveLength(THREADS_PAGE_SIZE));
            expect(screen.getByRole('button', {name: 'Show more Alpha chats'})).toBeTruthy();
            expect(screen.queryByRole('button', {name: 'Show less Alpha chats'})).toBeNull();
        });

        test('Show less is immediate under reduced motion', () => {
            const original = window.matchMedia;
            window.matchMedia = ((query: string) => ({matches: query.includes('reduce'), media: query, addEventListener: jest.fn(), removeEventListener: jest.fn()})) as unknown as typeof window.matchMedia;
            try {
                renderList({threadsByBot: {a: manyThreads(25)}});
                fireEvent.click(screen.getByRole('button', {name: 'Show more Alpha chats'}));
                fireEvent.click(screen.getByRole('button', {name: 'Show less Alpha chats'}));
                expect(chatTitles()).toHaveLength(THREADS_PAGE_SIZE);
            } finally {
                window.matchMedia = original;
            }
        });

        test.each([
            {name: 'a short list that was never expanded', count: 3},
            {name: 'a list of exactly one page', count: THREADS_PAGE_SIZE},
        ])('$name has no Show less', ({count}) => {
            renderList({threadsByBot: {a: manyThreads(count)}});
            expect(screen.queryByRole('button', {name: /Show (more|less) Alpha chats/})).toBeNull();
        });

        // The open chat is listed beyond the limit without the user having expanded anything.
        test('a pinned active chat does not make Show less appear', () => {
            renderList({threadsByBot: {a: manyThreads(THREADS_PAGE_SIZE + 4)}, activeBotId: 'a', activeRootId: `root_${THREADS_PAGE_SIZE + 4}`});
            expect(chatTitles()).toHaveLength(THREADS_PAGE_SIZE + 1);
            expect(screen.queryByRole('button', {name: 'Show less Alpha chats'})).toBeNull();
        });

        test('the active thread beyond the limit stays visible, in order', () => {
            renderList({threadsByBot: {a: manyThreads(25)}, activeBotId: 'a', activeRootId: 'root_18'});
            const titles = chatTitles();
            expect(titles).toHaveLength(THREADS_PAGE_SIZE + 1);
            expect(titles[THREADS_PAGE_SIZE]).toBe('Chat 18');
            expect(screen.getByRole('button', {name: 'Chat 18'}).getAttribute('aria-current')).toBe('true');
        });

        test('collapsing an agent resets its expansion', () => {
            renderList({threadsByBot: {a: manyThreads(25)}});
            fireEvent.click(screen.getByRole('button', {name: 'Show more Alpha chats'}));
            fireEvent.click(screen.getByRole('button', {name: 'Collapse Alpha chats'}));
            fireEvent.click(screen.getByRole('button', {name: 'Expand Alpha chats'}));
            expect(chatTitles()).toHaveLength(THREADS_PAGE_SIZE);
        });
    });

    describe('rename', () => {
        test('Rename chat asks for a title and renames that chat', async () => {
            const h = renderList();
            fireEvent.click(screen.getByRole('button', {name: 'Actions for First chat'}));
            fireEvent.click(await screen.findByText('Rename chat'));

            const input = await screen.findByLabelText('Chat name');
            expect((input as HTMLInputElement).value).toBe('First chat');
            fireEvent.change(input, {target: {value: 'Renamed chat'}});
            fireEvent.click(screen.getByRole('button', {name: 'Save'}));

            await waitFor(() => expect(h.onRenameThread).toHaveBeenCalledWith(expect.objectContaining({id: '1'}), 'Renamed chat'));
            await waitFor(() => expect(screen.queryByLabelText('Chat name')).toBeNull());
        });
    });

    test('clicking a collapsed agent row opens it and expands its chats', () => {
        const h = renderList();
        fireEvent.click(screen.getByRole('button', {name: 'Collapse Alpha chats'}));
        expect(screen.getByRole('button', {name: 'Expand Alpha chats'})).not.toBeNull();

        fireEvent.click(screen.getByRole('button', {name: 'Alpha'}));
        expect(h.onSelectThread).toHaveBeenCalled();
        expect(screen.getByRole('button', {name: 'Collapse Alpha chats'})).not.toBeNull();
    });

    test.each([
        {name: 'a chat whose agent is answering', generating: ['root_1'], activeRootId: '', expected: 1},
        {name: 'the chat that is open too', generating: ['root_1'], activeRootId: 'root_1', expected: 1},
        {name: 'a chat that is idle does not', generating: [], activeRootId: '', expected: 0},
        {name: 'every answering chat', generating: ['root_1', 'root_2'], activeRootId: '', expected: 2},
    ])('generating indicator: $name', ({generating, activeRootId, expected}) => {
        renderList({generatingRoots: new Set(generating), activeBotId: 'a', activeRootId});

        expect(screen.queryAllByTestId('thread-generating')).toHaveLength(expected);
    });

    test.each([
        {name: 'a chat that finished while it was not open', unread: ['root_1'], generating: [], expectedUnread: 1, expectedGenerating: 0},
        {name: 'a chat that is answering shows the wave, not the dot', unread: ['root_1'], generating: ['root_1'], expectedUnread: 0, expectedGenerating: 1},
        {name: 'no unread chats', unread: [], generating: [], expectedUnread: 0, expectedGenerating: 0},
    ])('unread indicator: $name', ({unread, generating, expectedUnread, expectedGenerating}) => {
        renderList({unreadRoots: new Set(unread), generatingRoots: new Set(generating), activeBotId: 'a', activeRootId: ''});

        expect(screen.queryAllByTestId('thread-unread')).toHaveLength(expectedUnread);
        expect(screen.queryAllByTestId('thread-generating')).toHaveLength(expectedGenerating);
    });

    test('an unread chat is announced to screen readers', () => {
        renderList({unreadRoots: new Set(['root_1']), activeBotId: 'a', activeRootId: ''});

        expect(screen.getAllByText('The agent has replied')).toHaveLength(1);
    });
});
