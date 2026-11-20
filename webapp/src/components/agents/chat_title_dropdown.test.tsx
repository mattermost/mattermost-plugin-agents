// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';
import styled from 'styled-components';

import ChatTitleDropdown from './chat_title_dropdown';

// jsdom has no layout engine, so assert the shrink/overflow styles that let a flex header
// ellipsize a long Compass Dropdown label (Trigger + button + label).
describe('ChatTitleDropdown truncation', () => {
    test('lets a long title shrink and ellipsize inside a narrow flex header', () => {
        const title = 'A very long chat title that should not push header actions off the bar';
        render(
            <NarrowHeader>
                <ChatTitleDropdown
                    label={title}
                    ariaLabel={title}
                    expanded={false}
                    onClick={jest.fn()}
                    testId='agent-chat-menu-button'
                />
                <span data-testid='header-action'>{'Action'}</span>
            </NarrowHeader>,
        );

        const button = screen.getByTestId('agent-chat-menu-button');
        const trigger = button.parentElement;
        expect(trigger).toBeTruthy();

        const triggerStyle = getComputedStyle(trigger!);
        expect(triggerStyle.display).toBe('flex');
        expect(triggerStyle.overflow).toBe('hidden');
        expect(parseFloat(triggerStyle.minWidth)).toBe(0);

        const buttonStyle = getComputedStyle(button);
        expect(buttonStyle.overflow).toBe('hidden');
        expect(parseFloat(buttonStyle.minWidth)).toBe(0);
        expect(buttonStyle.maxWidth).toBe('100%');

        const label = button.querySelector('[class*="dropdown__label"]');
        expect(label).toBeTruthy();
        expect(getComputedStyle(label!).textOverflow).toBe('ellipsis');
        expect(getComputedStyle(label!).overflow).toBe('hidden');
    });
});

const NarrowHeader = styled.div`
    display: flex;
    width: 12rem;
    min-width: 0;
    align-items: center;
`;
