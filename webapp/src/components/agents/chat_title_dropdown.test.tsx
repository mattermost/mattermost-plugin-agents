// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';
import styled from 'styled-components';

import ChatTitleDropdown from './chat_title_dropdown';

// jsdom has no layout engine. This is a shrink-path guard (same idea as
// tool_activity_display layout tests): assert the styles that let a flex header
// ellipsize from the start of the title instead of hard-clipping both ends.
// Browser/visual coverage of the ellipsis glyph itself is a follow-up.
describe('ChatTitleDropdown truncation', () => {
    test('keeps a start-aligned shrink path for a long title in a narrow flex header', () => {
        const title = 'A very long chat title that should not push header actions off the bar';
        const onClick = jest.fn();
        render(
            <NarrowHeader>
                <ChatTitleDropdown
                    label={title}
                    ariaLabel={title}
                    expanded={false}
                    onClick={onClick}
                    testId='agent-chat-menu-button'
                />
                <span data-testid='header-action'>{'Action'}</span>
            </NarrowHeader>,
        );

        const button = screen.getByTestId('agent-chat-menu-button');
        expect(button.textContent).toContain(title);
        expect(button.getAttribute('title')).toBeNull();
        expect(screen.getByTestId('header-action')).toBeTruthy();

        const tooltip = screen.getByTestId('with-tooltip');
        expect(tooltip.getAttribute('data-title')).toBe(title);
        expect(tooltip.getAttribute('data-disabled')).toBe('false');

        const trigger = tooltip.parentElement;
        expect(trigger).toBeTruthy();

        const triggerStyle = getComputedStyle(trigger!);
        expect(triggerStyle.display).toBe('flex');
        expect(triggerStyle.justifyContent).toBe('flex-start');
        expect(triggerStyle.overflow).toBe('hidden');
        expect(parseFloat(triggerStyle.minWidth)).toBe(0);

        const tooltipTriggerStyle = getComputedStyle(button.parentElement!);
        expect(tooltipTriggerStyle.display).toBe('flex');
        expect(parseFloat(tooltipTriggerStyle.minWidth)).toBe(0);
        expect(tooltipTriggerStyle.overflow).toBe('hidden');

        const buttonStyle = getComputedStyle(button);
        expect(buttonStyle.overflow).toBe('hidden');
        expect(buttonStyle.justifyContent).toBe('flex-start');
        expect(buttonStyle.textAlign).toBe('start');
        expect(buttonStyle.width).toBe('100%');
        expect(parseFloat(buttonStyle.minWidth)).toBe(0);

        const label = button.querySelector('[class*="dropdown__label"]');
        expect(label).toBeTruthy();
        expect(getComputedStyle(label!).textOverflow).toBe('ellipsis');
        expect(getComputedStyle(label!).overflow).toBe('hidden');
        expect(getComputedStyle(label!).display).toBe('block');

        const truncate = label!.firstElementChild;
        expect(truncate).toBeTruthy();
        expect(getComputedStyle(truncate!).textOverflow).toBe('ellipsis');
        expect(getComputedStyle(truncate!).overflow).toBe('hidden');

        button.click();
        expect(onClick).toHaveBeenCalledTimes(1);
    });

    test('disables the full-name tooltip while the menu is open', () => {
        render(
            <ChatTitleDropdown
                label='Quarterly plan'
                ariaLabel='Quarterly plan'
                expanded={true}
                onClick={jest.fn()}
                testId='agent-chat-menu-button'
            />,
        );

        const tooltip = screen.getByTestId('with-tooltip');
        expect(tooltip.getAttribute('data-title')).toBe('Quarterly plan');
        expect(tooltip.getAttribute('data-disabled')).toBe('true');
    });
});

const NarrowHeader = styled.div`
    display: flex;
    width: 12rem;
    min-width: 0;
    align-items: center;
`;
