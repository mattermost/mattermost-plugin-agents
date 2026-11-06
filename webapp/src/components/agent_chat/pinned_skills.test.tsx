// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, within} from '@testing-library/react';

import {usePinnedSkillsLayoutMock} from '../../../tests/pinned_skills_layout_mock';

import PinnedSkills from './pinned_skills';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string>) => (
            defaultMessage.replace(/\{(\w+)\}/g, (_m, k) => values?.[k] ?? '')
        ),
    };
    return {...actual, useIntl: () => intl};
});

const prompts = ['1', '2', '3', '4', '5'].map((n) => ({
    id: n,
    name: `Prompt ${n}`,
    description: '',
    template: '',
    creator_id: 'me',
    is_shared: false,
    created_at: 0,
    updated_at: 0,
    deleted_at: 0,
}));

const renderPrompts = (onPick = jest.fn(), list = prompts, error: string | null = null) => render(
    <PinnedSkills
        prompts={list}
        disabled={false}
        error={error}
        onPick={onPick}
    />,
);

const lines = () => screen.queryAllByTestId('agent-chat-pinned-skill-line').map((line) => line.textContent);

describe('PinnedSkills', () => {
    const layout = usePinnedSkillsLayoutMock();

    test('renders nothing while the group has no width, then the buttons once it does', () => {
        layout.setWidth(0);
        renderPrompts();
        expect(lines()).toEqual([]);
        expect(screen.queryByRole('button')).toBeNull();

        layout.setWidth(1000);
        layout.resize();
        expect(lines()).toEqual(['Prompt 1Prompt 2Prompt 3Prompt 4Prompt 5']);
    });

    test('shows every prompt on one line when they all fit', () => {
        layout.setWidth(1000);
        renderPrompts();
        expect(lines()).toEqual(['Prompt 1Prompt 2Prompt 3Prompt 4Prompt 5']);
        expect(screen.queryByRole('button', {name: /^More skills/})).toBeNull();
    });

    test('wraps to two lines and puts the rest behind the chip, closing the second line', () => {
        layout.setWidth(170);
        renderPrompts();
        expect(lines()).toEqual(['Prompt 1Prompt 2', 'Prompt 3+2']);
    });

    test('never takes more than two lines at any width', () => {
        for (const width of [100, 130, 170, 190, 270, 400, 1000]) {
            layout.setWidth(width);
            const {unmount} = renderPrompts();
            expect(lines().length).toBeLessThanOrEqual(2);
            unmount();
        }
    });

    test.each([
        {name: 'shows why the last run failed, under the buttons', error: 'Could not run it', alert: 'Could not run it'},
        {name: 'shows no alert when nothing failed', error: null, alert: null},
    ])('$name', ({error, alert}) => {
        layout.setWidth(1000);
        renderPrompts(jest.fn(), prompts, error);
        expect(screen.queryByRole('alert')?.textContent ?? null).toBe(alert);
    });

    test('measures again when the width changes', () => {
        layout.setWidth(1000);
        renderPrompts();
        expect(lines()).toHaveLength(1);

        layout.setWidth(170);
        layout.resize();
        expect(lines()).toEqual(['Prompt 1Prompt 2', 'Prompt 3+2']);
    });

    test('the +N menu stays open when a resize moves the chip to another line', () => {
        // One short prompt and five that each take 300; the chip joins whichever line has room.
        const list = ['Aaaa', ...[1, 2, 3, 4, 5].map((n) => `L${n}`.padEnd(30, '-'))].map((name, i) => ({...prompts[0], id: `w${i}`, name}));

        // Room for the first five and the chip on the second line.
        layout.setWidth(700);
        renderPrompts(jest.fn(), list);
        expect(lines()).toHaveLength(2);
        expect(lines()[1]).toMatch(/\+1$/);
        fireEvent.click(screen.getByRole('button', {name: /^More skills/}));
        expect(within(screen.getByTestId('dropdownmenu')).getAllByRole('button')).toHaveLength(1);

        // Narrow: the chip now sits on the first line, so it is rebuilt under a different line.
        layout.setWidth(300);
        layout.resize();
        expect(lines()).toHaveLength(1);
        expect(lines()[0]).toMatch(/\+5$/);
        expect(within(screen.getByTestId('dropdownmenu')).getAllByRole('button')).toHaveLength(5);
    });

    test('closes the menu when everything fits again', () => {
        layout.setWidth(170);
        renderPrompts();
        fireEvent.click(screen.getByRole('button', {name: /^More skills/}));
        expect(screen.queryByTestId('dropdownmenu')).not.toBeNull();

        layout.setWidth(1000);
        layout.resize();
        expect(screen.queryByRole('button', {name: /^More skills/})).toBeNull();
        expect(screen.queryByTestId('dropdownmenu')).toBeNull();
    });

    test('the chip opens a menu with the prompts that did not fit, and picking one runs it', () => {
        layout.setWidth(170);
        const onPick = jest.fn();
        renderPrompts(onPick);

        const more = screen.getByRole('button', {name: /^More skills/});
        expect(more.textContent).toBe('+2');
        fireEvent.click(more);

        const menu = screen.getByTestId('dropdownmenu');
        expect(within(menu).getAllByRole('button').map((b) => b.textContent)).toEqual(['Prompt 4', 'Prompt 5']);

        fireEvent.click(within(menu).getByRole('button', {name: 'Prompt 5'}));
        expect(onPick).toHaveBeenCalledWith(expect.objectContaining({id: '5'}));
    });

    test('the hidden measuring row is not exposed to assistive technology', () => {
        layout.setWidth(1000);
        renderPrompts();
        expect(screen.getAllByRole('button')).toHaveLength(5);
    });
});
