// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, within} from '@testing-library/react';

import type {LLMBot} from '@/bots';
import type {AIThread} from '@/types/ai_thread';

import AgentThreadsList from './agent_threads_list';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const real = actual.createIntl({locale: 'en', defaultLocale: 'en', onError: () => null});
    const intl = {
        formatMessage: (descriptor: {id?: string; defaultMessage: string}, values?: Record<string, string | number>) =>
            real.formatMessage({id: descriptor.id ?? descriptor.defaultMessage, ...descriptor}, values),
    };
    return {...actual, useIntl: () => intl};
});

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

const renderList = (props: Partial<React.ComponentProps<typeof AgentThreadsList>> = {}) => {
    const handlers = {onSelectThread: jest.fn(), onNewChat: jest.fn(), onRetry: jest.fn()};
    render(
        <AgentThreadsList
            bots={bots}
            threadsByBot={threadsByBot}
            loading={false}
            error={null}
            {...handlers}
            {...props}
        />,
    );
    return handlers;
};

describe('AgentThreadsList', () => {
    test('groups threads under their agent and shows a placeholder for blank titles', () => {
        renderList();
        const list = screen.getByRole('list', {name: 'Alpha chats'});
        const items = within(list).getAllByRole('button');
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

    test('agent without threads has a disabled chevron', () => {
        renderList();
        expect((screen.getByRole('button', {name: 'Collapse Beta chats'}) as HTMLButtonElement).disabled).toBe(true);
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
        expect(screen.getByRole('button', {name: 'First chat'}).getAttribute('aria-current')).toBe('page');
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
});
