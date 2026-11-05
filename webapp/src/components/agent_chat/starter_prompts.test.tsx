// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen, within} from '@testing-library/react';

import StarterPrompts from './starter_prompts';

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

// jsdom has no layout, so give each measured piece a width of 10 per character (a prompt 80, a "+2"
// chip 20, no gap) and the group the width a test sets.
let groupWidth = 0;
const observers: Array<() => void> = [];

class FakeResizeObserver {
    observe = jest.fn();
    disconnect = jest.fn();
    constructor(callback: () => void) {
        observers.push(callback);
    }
}

beforeEach(() => {
    groupWidth = 0;
    observers.length = 0;
    (global as any).ResizeObserver = FakeResizeObserver;
    jest.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockImplementation(function measuredWidth(this: HTMLElement) {
        return (this.textContent?.length ?? 0) * 10;
    });
    jest.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(function groupClientWidth(this: HTMLElement) {
        return this.dataset.testid === 'agent-chat-starter-prompts' ? groupWidth : 0;
    });
});

afterEach(() => {
    jest.restoreAllMocks();
    delete (global as any).ResizeObserver;
});

const renderPrompts = (onPick = jest.fn()) => render(
    <StarterPrompts
        prompts={prompts}
        disabled={false}
        onPick={onPick}
    />,
);

const lines = () => screen.queryAllByTestId('agent-chat-starter-line').map((line) => line.textContent);

describe('StarterPrompts', () => {
    test('shows every prompt on one line when they all fit', () => {
        groupWidth = 1000;
        renderPrompts();
        expect(lines()).toEqual(['Prompt 1Prompt 2Prompt 3Prompt 4Prompt 5']);
        expect(screen.queryByRole('button', {name: /^More prompts/})).toBeNull();
    });

    test('wraps to two lines and puts the rest behind the chip, closing the second line', () => {
        groupWidth = 170;
        renderPrompts();
        expect(lines()).toEqual(['Prompt 1Prompt 2', 'Prompt 3+2']);
    });

    test('never takes more than two lines at any width', () => {
        for (const width of [100, 130, 170, 190, 270, 400, 1000]) {
            groupWidth = width;
            const {unmount} = renderPrompts();
            expect(lines().length).toBeLessThanOrEqual(2);
            unmount();
        }
    });

    test('every prompt button has the 4% resting background', () => {
        groupWidth = 1000;
        renderPrompts();
        const button = screen.getByRole('button', {name: 'Prompt 1'});
        expect(window.getComputedStyle(button).background).toContain('0.04');
    });

    test('measures again when the width changes', () => {
        groupWidth = 1000;
        renderPrompts();
        expect(lines()).toHaveLength(1);

        groupWidth = 170;
        act(() => observers.forEach((callback) => callback()));
        expect(lines()).toEqual(['Prompt 1Prompt 2', 'Prompt 3+2']);
    });

    test('the chip opens a menu with the prompts that did not fit, and picking one runs it', () => {
        groupWidth = 170;
        const onPick = jest.fn();
        renderPrompts(onPick);

        const more = screen.getByRole('button', {name: /^More prompts/});
        expect(more.textContent).toBe('+2');
        fireEvent.click(more);

        const menu = screen.getByTestId('dropdownmenu');
        expect(within(menu).getAllByRole('button').map((b) => b.textContent)).toEqual(['Prompt 4', 'Prompt 5']);

        fireEvent.click(within(menu).getByRole('button', {name: 'Prompt 5'}));
        expect(onPick).toHaveBeenCalledWith(expect.objectContaining({id: '5'}));
    });

    test('the hidden measuring row is not exposed to assistive technology', () => {
        groupWidth = 1000;
        renderPrompts();
        expect(screen.getAllByRole('button')).toHaveLength(5);
    });
});
