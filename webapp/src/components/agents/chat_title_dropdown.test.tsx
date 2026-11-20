// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';
import styled from 'styled-components';

import ChatTitleDropdown from './chat_title_dropdown';

// jsdom has no layout engine, and compass CSS modules are identity-obj-proxy in Jest, so
// getComputedStyle only sees our styled-components host shrink path. Compass's own Dropdown
// tests cover the ellipsis rules; browser/visual coverage of the glyph is a follow-up.
describe('ChatTitleDropdown truncation', () => {
    test('constrains the compass trigger in a narrow flex header and keeps the native title', () => {
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
        expect(button.getAttribute('title')).toBe(title);
        expect(screen.getByTestId('header-action')).toBeTruthy();

        const trigger = button.parentElement;
        expect(trigger).toBeTruthy();

        const triggerStyle = getComputedStyle(trigger!);
        expect(triggerStyle.display).toBe('flex');
        expect(triggerStyle.justifyContent).toBe('flex-start');
        expect(triggerStyle.overflow).toBe('hidden');
        expect(parseFloat(triggerStyle.minWidth)).toBe(0);

        // Cap on both: Trigger for flex intrinsic sizing, button so the dropdown itself is capped.
        expect(triggerStyle.maxWidth).toBe('min(100%, 40rem)');
        expect(getComputedStyle(button).width).toBe('100%');
        expect(getComputedStyle(button).maxWidth).toBe('min(100%, 40rem)');

        const label = button.querySelector('[class*="dropdown__label"]');
        expect(label).toBeTruthy();
        expect(label!.textContent).toBe(title);

        button.click();
        expect(onClick).toHaveBeenCalledTimes(1);
    });
});

const NarrowHeader = styled.div`
    display: flex;
    width: 12rem;
    min-width: 0;
    align-items: center;
`;
