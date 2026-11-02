// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

import ThreadItem from './thread_item';

jest.mock('@/mm_webapp', () => ({
    Timestamp: () => null,
}));

describe('ThreadItem', () => {
    test.each(['Enter', ' '])('opens the thread from the keyboard with %p', (key) => {
        const onClick = jest.fn();
        render(
            <ThreadItem
                postTitle='Quarterly planning'
                turnCount={3}
                lastActivityDate={0}
                label='Agents'
                onClick={onClick}
            />,
        );

        const item = screen.getByRole('button', {name: /Quarterly planning/});
        item.focus();
        expect(document.activeElement).toBe(item);

        fireEvent.keyDown(item, {key});
        expect(onClick).toHaveBeenCalledTimes(1);
    });
});
