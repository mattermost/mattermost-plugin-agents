// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';

import DotMenu from './dot_menu';
import {popoverTransformOrigin} from './dropdown';

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
        expect(button.getAttribute('aria-describedby')).toBeNull();
        expect(button.className).toMatch(/icon-button--size-small/);
        expect(button.className).toMatch(/icon-button--padding-compact/);
        expect(button.parentElement?.tagName).toBe('SPAN');

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

describe('DotMenu open and close transition', () => {
    test('plays the compass popover transition and unmounts the menu once it has exited', async () => {
        render(
            <DotMenu
                icon={<span>{'icon'}</span>}
                title='AI Actions'
                testId='ai-actions-menu'
            >
                <div>{'item'}</div>
            </DotMenu>,
        );
        expect(screen.queryByTestId('dropdownmenu')).toBeNull();

        fireEvent.click(screen.getByTestId('ai-actions-menu'));
        const menu = screen.getByTestId('dropdownmenu');
        expect(menu.className).toMatch(/popover-menu--transition/);
        await waitFor(() => expect(menu.className).toMatch(/popover-menu--visible/));

        fireEvent.click(screen.getByTestId('ai-actions-menu'));
        await waitFor(() => expect(menu.className).not.toMatch(/popover-menu--visible/));
        expect(screen.queryByTestId('dropdownmenu')).not.toBeNull();

        await waitFor(() => expect(screen.queryByTestId('dropdownmenu')).toBeNull());
    });
});

describe('popover transform origin', () => {
    test.each([
        {placement: 'bottom-start', origin: 'top left'},
        {placement: 'bottom-end', origin: 'top right'},
        {placement: 'bottom', origin: 'top center'},
        {placement: 'top-start', origin: 'bottom left'},
        {placement: 'top-end', origin: 'bottom right'},
        {placement: 'right-start', origin: 'top left'},
        {placement: 'right-end', origin: 'bottom left'},
        {placement: 'left-start', origin: 'top right'},
        {placement: 'left', origin: 'center right'},
    ] as const)('scales a $placement popup from $origin', ({placement, origin}) => {
        expect(popoverTransformOrigin(placement)).toBe(origin);
    });

    test('a menu opened below its trigger scales from the trigger-facing corner', () => {
        render(
            <DotMenu
                icon={<span>{'icon'}</span>}
                title='AI Actions'
                testId='ai-actions-menu'
                placement='bottom-end'
            >
                <div>{'item'}</div>
            </DotMenu>,
        );

        fireEvent.click(screen.getByTestId('ai-actions-menu'));

        const container = screen.getByTestId('dropdownmenu').parentElement as HTMLElement;
        expect(container.style.getPropertyValue('--popover-transform-origin')).toBe('top right');
    });
});
