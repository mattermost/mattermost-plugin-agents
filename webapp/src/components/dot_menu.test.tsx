// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

import DotMenu from './dot_menu';

jest.mock('@mattermost/shared/components/tooltip', () => ({
    WithTooltip: ({
        children,
        title,
        disabled,
    }: {
        children: React.ReactNode;
        title: string;
        disabled?: boolean;
    }) => (
        <div
            data-testid='dot-menu-tooltip'
            data-title={title}
            data-disabled={disabled ? 'true' : 'false'}
        >
            {children}
        </div>
    ),
}));

describe('DotMenu IconButton tooltip', () => {
    test('uses WithTooltip instead of a native title attribute', () => {
        render(
            <DotMenu
                icon={<span>{'icon'}</span>}
                title='AI Actions'
                testId='ai-actions-menu'
                size='small'
                padding='compact'
            >
                <div>{'item'}</div>
            </DotMenu>,
        );

        const button = screen.getByTestId('ai-actions-menu');
        expect(button.getAttribute('aria-label')).toBe('AI Actions');
        expect(button.getAttribute('title')).toBeNull();
        expect(button.className).toMatch(/icon-button--size-small/);
        expect(button.className).toMatch(/icon-button--padding-compact/);

        const tooltip = screen.getByTestId('dot-menu-tooltip');
        expect(tooltip.getAttribute('data-title')).toBe('AI Actions');
        expect(tooltip.getAttribute('data-disabled')).toBe('false');
    });

    test('disables the tooltip while the menu is open', () => {
        render(
            <DotMenu
                icon={<span>{'icon'}</span>}
                title='AI Actions'
                testId='ai-actions-menu'
            >
                <div>{'item'}</div>
            </DotMenu>,
        );

        fireEvent.click(screen.getByTestId('ai-actions-menu'));
        expect(screen.getByTestId('dot-menu-tooltip').getAttribute('data-disabled')).toBe('true');
    });
});
