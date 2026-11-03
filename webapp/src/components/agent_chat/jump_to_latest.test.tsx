// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

import JumpToLatest from './jump_to_latest';

jest.mock('react-intl', () => ({
    useIntl: () => ({
        formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string>) => (
            defaultMessage.replace(/\{(\w+)\}/g, (_m, k) => values?.[k] ?? '')
        ),
    }),
}));

const renderPill = (props: Partial<React.ComponentProps<typeof JumpToLatest>> = {}) => {
    const onClick = jest.fn();
    render(
        <JumpToLatest
            visible={true}
            responding={true}
            agentName='Matty'
            onClick={onClick}
            {...props}
        />,
    );
    return onClick;
};

describe('JumpToLatest', () => {
    test('while the agent answers it names the agent and scrolls to the latest on click', () => {
        const onClick = renderPill();

        const button = screen.getByRole('button', {name: 'Matty is responding. Scroll to the latest message'});

        // Only the wave shows (no text); the name is for screen readers.
        expect(button.textContent).toBe('');
        fireEvent.click(button);
        expect(onClick).toHaveBeenCalledTimes(1);
    });

    test('once the agent has finished it is a plain scroll-down button', () => {
        renderPill({responding: false});

        const button = screen.getByRole('button', {name: 'Scroll to the latest message'});
        expect(button.getAttribute('data-responding')).toBe('false');
    });

    test.each([
        {visible: true, tabIndex: '0'},
        {visible: false, tabIndex: '-1'},
    ])('visible=$visible: marks its state and keeps the button in or out of the tab order', ({visible, tabIndex}) => {
        renderPill({visible});

        expect(screen.getByTestId('jump-to-latest').getAttribute('data-visible')).toBe(String(visible));
        expect(screen.getByRole('button', {hidden: true}).getAttribute('tabindex')).toBe(tabIndex);
    });
});
