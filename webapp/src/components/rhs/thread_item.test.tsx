// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

import ThreadItem from './thread_item';

jest.mock('@/mm_webapp', () => ({
    Timestamp: () => null,
}));

jest.mock('@mattermost/shared/components/tooltip', () => ({
    WithTooltip: ({children}: {children: React.ReactNode}) => <>{children}</>,
}));

jest.mock('react-intl', () => ({
    useIntl: () => ({
        formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: {count: number}) => (
            values ? defaultMessage.replace(/\{count, plural, one \{(.*?)\} other \{(.*?)\}\}/, (_m, one, other) => (values.count === 1 ? one : other).replace('#', String(values.count))) : defaultMessage
        ),
    }),
}));

const renderItem = (props: Partial<React.ComponentProps<typeof ThreadItem>> = {}) => {
    const onClick = jest.fn();
    render(
        <ThreadItem
            preview='Can you give me some survival tips'
            authorName='Systems Administrator'
            authorAvatarUrl='/users/me/image'
            agentName='Wilson'
            agentAvatarUrl='/users/bot/image'
            lastActivityDate={0}
            replyCount={1}
            onClick={onClick}
            onRename={jest.fn()}
            onArchive={jest.fn()}
            {...props}
        />,
    );
    return onClick;
};

describe('ThreadItem actions', () => {
    test.each([
        {label: 'Rename chat', callback: 'onRename'},
        {label: 'Archive chat', callback: 'onArchive'},
    ] as const)('the hover actions open the same menu as the Agents sidebar; $label runs it without opening the chat', async ({label, callback}) => {
        const handlers = {onRename: jest.fn(), onArchive: jest.fn()};
        const onClick = renderItem(handlers);

        fireEvent.click(screen.getByRole('button', {name: 'Chat actions'}));
        expect(await screen.findByText('Rename chat')).toBeTruthy();
        expect(screen.getByText('Archive chat')).toBeTruthy();
        fireEvent.click(screen.getByText(label));

        expect(handlers[callback]).toHaveBeenCalledTimes(1);
        expect(onClick).not.toHaveBeenCalled();
    });
});

describe('ThreadItem', () => {
    test('shows the author, the agent, the first message and the reply count', () => {
        renderItem();

        expect(screen.getByText('Systems Administrator')).toBeTruthy();
        expect(screen.getByText('Wilson')).toBeTruthy();
        expect(screen.getByText('Can you give me some survival tips')).toBeTruthy();
        expect(screen.getByText('1 reply')).toBeTruthy();
    });

    test.each([
        {replyCount: 0, text: '0 replies'},
        {replyCount: 4, text: '4 replies'},
    ])('pluralizes $replyCount replies', ({replyCount, text}) => {
        renderItem({replyCount});

        expect(screen.getByText(text)).toBeTruthy();
    });

    test('omits the reply count until it is known', () => {
        // eslint-disable-next-line no-undefined -- the prop is absent until the root post loads
        renderItem({replyCount: undefined});

        expect(screen.queryByText(/repl/)).toBeNull();
    });

    test('falls back to a generic label for an empty message', () => {
        renderItem({preview: '  '});

        expect(screen.getByText('Conversation with Agents')).toBeTruthy();
    });

    test.each(['Enter', ' '])('opens the thread from the keyboard with %p', (key) => {
        const onClick = renderItem();

        const item = screen.getByRole('button', {name: /survival tips/});
        item.focus();
        expect(document.activeElement).toBe(item);

        fireEvent.keyDown(item, {key});
        expect(onClick).toHaveBeenCalledTimes(1);
    });
});
