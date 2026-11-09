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
        expect(lines()).toEqual(['Prompt 1Prompt 2Prompt 3Prompt 4+1']);
    });

    test('shows every prompt on one line when they all fit', () => {
        layout.setWidth(1000);
        renderPrompts(jest.fn(), prompts.slice(0, 4));
        expect(lines()).toEqual(['Prompt 1Prompt 2Prompt 3Prompt 4']);
        expect(screen.queryByRole('button', {name: /^More skills/})).toBeNull();
    });

    test('shows at most four prompts when the row has room for more', () => {
        layout.setWidth(1000);
        renderPrompts();
        expect(lines()).toEqual(['Prompt 1Prompt 2Prompt 3Prompt 4+1']);
    });

    test('keeps one row and puts the rest behind the chip', () => {
        layout.setWidth(170);
        renderPrompts();
        expect(lines()).toEqual(['Prompt 1+4']);
    });

    test('never takes more than one row at any width', () => {
        for (const width of [100, 130, 170, 190, 270, 400, 1000]) {
            layout.setWidth(width);
            const {unmount} = renderPrompts();
            expect(lines().length).toBeLessThanOrEqual(1);
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
        expect(lines()).toEqual(['Prompt 1+4']);
    });

    test('the +N menu stays open when a resize changes which prompts are hidden', () => {
        // One short prompt and five that each take 300. The chip stays on the only row.
        const list = ['Aaaa', ...[1, 2, 3, 4, 5].map((n) => `L${n}`.padEnd(30, '-'))].map((name, i) => ({...prompts[0], id: `w${i}`, name}));

        // Room for the short prompt, two long ones, and the chip.
        layout.setWidth(700);
        renderPrompts(jest.fn(), list);
        expect(lines()).toHaveLength(1);
        expect(lines()[0]).toMatch(/\+3$/);
        fireEvent.click(screen.getByRole('button', {name: /^More skills/}));
        expect(within(screen.getByTestId('dropdownmenu')).getAllByRole('button')).toHaveLength(3);

        // Narrower: only the short prompt fits beside the chip, which is rebuilt with a new count.
        layout.setWidth(300);
        layout.resize();
        expect(lines()).toHaveLength(1);
        expect(lines()[0]).toMatch(/\+5$/);
        expect(within(screen.getByTestId('dropdownmenu')).getAllByRole('button')).toHaveLength(5);
    });

    test('closes the menu when everything fits again', () => {
        layout.setWidth(170);
        renderPrompts(jest.fn(), prompts.slice(0, 4));
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
        expect(more.textContent).toBe('+4');
        fireEvent.click(more);

        const menu = screen.getByTestId('dropdownmenu');
        expect(within(menu).getAllByRole('button').map((b) => b.textContent)).toEqual(['Prompt 2', 'Prompt 3', 'Prompt 4', 'Prompt 5']);

        fireEvent.click(within(menu).getByRole('button', {name: 'Prompt 5'}));
        expect(onPick).toHaveBeenCalledWith(expect.objectContaining({id: '5'}));
    });

    test('the hidden measuring row is not exposed to assistive technology', () => {
        layout.setWidth(1000);
        renderPrompts();
        expect(screen.getAllByRole('button')).toHaveLength(5);
    });
});
