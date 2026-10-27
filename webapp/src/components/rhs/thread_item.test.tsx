// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

import ThreadItem from './thread_item';

jest.mock('@/mm_webapp', () => ({
    Timestamp: () => null,
}));

jest.mock('react-intl', () => ({
    useIntl: () => ({
        formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: {count: number}) => (
            values ? defaultMessage.replace(/\{count, plural, one \{(.*?)\} other \{(.*?)\}\}/, (_m, one, other) => (values.count === 1 ? one : other).replace('#', String(values.count))) : defaultMessage
        ),
    }),
}));

const renderItem = (props: Partial<React.ComponentProps<typeof ThreadItem>> = {}) => {
    const handlers = {onClick: jest.fn(), onMenuClick: jest.fn()};
    render(
        <ThreadItem
            title='Survival tips'
            preview='Can you give me some survival tips'
            agentName='Wilson'
            agentAvatarUrl='/users/bot/image'
            lastActivityDate={0}
            replyCount={1}
            {...handlers}
            {...props}
        />,
    );
    return handlers;
};

describe('ThreadItem', () => {
    test('is the compass Thread List Item with the agent as its name: avatar, no tag, no participants', () => {
        renderItem();

        // The agent's name leads the item (its avatar is decorative); the title, message and reply count follow.
        expect(screen.getByText('Wilson')).toBeTruthy();
        expect(screen.getByText('Survival tips')).toBeTruthy();
        expect(screen.getByText('Can you give me some survival tips')).toBeTruthy();
        expect(screen.getByText('1 reply')).toBeTruthy();
        expect(document.querySelector('img[src="/users/bot/image"]')).toBeTruthy();

        // No participants row (so no second avatar and no group label).
        expect(screen.queryByRole('group')).toBeNull();
        expect(screen.queryByText(/participant/i)).toBeNull();
    });

    test.each([
        {replyCount: 0, text: '0 replies'},
        {replyCount: 4, text: '4 replies'},
    ])('pluralizes $replyCount replies', ({replyCount, text}) => {
        renderItem({replyCount});

        expect(screen.getByText(text)).toBeTruthy();
    });

    test('shows no reply count until it is known', () => {
        // eslint-disable-next-line no-undefined -- the prop is absent until the root post loads
        renderItem({replyCount: undefined});

        expect(screen.queryByText(/repl/)).toBeNull();
    });

    test.each([
        {name: 'a title is shown above the message', title: 'Survival tips', preview: 'First message', expected: ['Survival tips', 'First message']},
        {name: 'a blank title is not shown', title: '  ', preview: 'First message', expected: ['First message']},
    ])('$name', ({title, preview, expected}) => {
        renderItem({title, preview});

        expected.forEach((text) => expect(screen.getByText(text)).toBeTruthy());
        expect(screen.queryByText('Conversation with Agents')).toBeNull();
    });

    test('falls back to a generic label with neither a title nor a message', () => {
        renderItem({title: '', preview: ''});

        expect(screen.getByText('Conversation with Agents')).toBeTruthy();
    });

    test('opens the chat on click and from the keyboard', () => {
        const {onClick} = renderItem();

        const item = screen.getByRole('button', {name: /Survival tips/});
        fireEvent.click(item);
        fireEvent.keyDown(item, {key: 'Enter'});

        expect(onClick).toHaveBeenCalledTimes(2);
    });

    test('the hover actions button asks the list to open its menu next to it, without opening the chat', () => {
        const {onClick, onMenuClick} = renderItem();

        const button = screen.getByRole('button', {name: 'Chat actions'});
        fireEvent.click(button);

        expect(onMenuClick).toHaveBeenCalledWith(button);
        expect(onClick).not.toHaveBeenCalled();
    });
});
