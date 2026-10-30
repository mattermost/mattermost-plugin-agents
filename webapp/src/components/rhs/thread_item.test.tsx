// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

import ThreadItem from './thread_item';

jest.mock('@/mm_webapp', () => ({
    Timestamp: () => <span>{'46m ago'}</span>,
}));

jest.mock('react-intl', () => ({
    useIntl: () => ({
        formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: {count: number}) => (
            values ? defaultMessage.replace(/\{count, plural, one \{(.*?)\} other \{(.*?)\}\}/, (_m, one, other) => (values.count === 1 ? one : other).replace('#', String(values.count))) : defaultMessage
        ),
    }),
}));

const renderItem = (props: Partial<React.ComponentProps<typeof ThreadItem>> = {}) => {
    const handlers = {onClick: jest.fn(), startRename: jest.fn()};
    render(
        <ThreadItem
            conversationId='c1'
            chatActions={{startRename: handlers.startRename, renameTarget: null, submitRename: jest.fn(), closeRename: jest.fn()}}
            onClick={handlers.onClick}
            title='Mattermost selling features overview'
            preview='Key selling points: self-hosted and air-gapped deployment'
            agentName='Matty'
            agentAvatarUrl='/users/bot/image'
            lastActivityDate={0}
            replyCount={15}
            {...props}
        />,
    );
    return handlers;
};

describe('ThreadItem', () => {
    test('shows the agent\'s avatar, the chat title and first message, and a meta line with the agent, time and replies', () => {
        renderItem();

        expect(document.querySelector('img[src="/users/bot/image"]')).toBeTruthy();
        expect(screen.getByText('Mattermost selling features overview')).toBeTruthy();
        expect(screen.getByText('Key selling points: self-hosted and air-gapped deployment')).toBeTruthy();
        expect(screen.getByText('Matty')).toBeTruthy();
        expect(screen.getByText('46m ago')).toBeTruthy();

        // The count shows as a number; screen readers get the full phrase.
        expect(screen.getByText('15')).toBeTruthy();
        expect(screen.getByText('15 replies')).toBeTruthy();
    });

    test.each([
        {replyCount: 1, text: '1 reply'},
        {replyCount: 0, text: '0 replies'},
    ])('$text', ({replyCount, text}) => {
        renderItem({replyCount});

        expect(screen.getByText(text)).toBeTruthy();
    });

    test('shows no reply count until it is known', () => {
        // eslint-disable-next-line no-undefined -- the prop is absent until the root post loads
        renderItem({replyCount: undefined});

        expect(screen.queryByText(/repl/)).toBeNull();
    });

    test.each([
        {name: 'a title leads and the message follows', title: 'A title', preview: 'First message', texts: ['A title', 'First message'], absent: []},
        {name: 'the first message heads an untitled chat and is not repeated', title: '  ', preview: 'First message', texts: ['First message'], absent: []},
        {name: 'a generic label heads a chat with neither', title: '', preview: '', texts: ['New chat'], absent: []},
    ])('$name', ({title, preview, texts}) => {
        renderItem({title, preview});

        texts.forEach((text) => expect(screen.getAllByText(text)).toHaveLength(1));
    });

    test('opens the chat on click and from the keyboard', () => {
        const {onClick} = renderItem();

        const item = screen.getByRole('button', {name: 'Mattermost selling features overview, Matty'});
        fireEvent.click(item);
        fireEvent.keyDown(item, {key: 'Enter'});

        expect(onClick).toHaveBeenCalledTimes(2);
    });

    test('the actions button opens the chat\'s actions menu without opening the chat', async () => {
        const {onClick, startRename} = renderItem();

        fireEvent.click(screen.getByRole('button', {name: 'Chat actions'}));
        fireEvent.click(await screen.findByRole('menuitem', {name: 'Rename chat'}));

        expect(startRename).toHaveBeenCalledWith({conversationId: 'c1', title: 'Mattermost selling features overview'});
        expect(onClick).not.toHaveBeenCalled();
    });
});
