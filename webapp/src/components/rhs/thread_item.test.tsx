// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

import ThreadItem from './thread_item';

jest.mock('@/mm_webapp', () => ({
    Timestamp: () => <span>{'timestamp'}</span>,
}));

describe('ThreadItem', () => {
    test('scopes a history entry by conversation ID', () => {
        const onClick = jest.fn();

        render(
            <ThreadItem
                conversationId='conversation-id'
                postTitle='Deployment plan'
                turnCount={2}
                lastActivityDate={1}
                label='Mock Bot'
                onClick={onClick}
            />,
        );

        const entry = screen.getByTestId('rhs-thread-conversation-id');
        expect(entry.textContent).toContain('Deployment plan');
        expect(entry.textContent).toContain('Mock Bot');
        expect(entry.textContent).toContain('2 messages');

        fireEvent.click(entry);
        expect(onClick).toHaveBeenCalledTimes(1);
    });
});
