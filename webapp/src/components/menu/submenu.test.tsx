// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import {act, fireEvent, render, screen} from '@testing-library/react';

import Submenu from './submenu';

const Harness = () => {
    const [state, setState] = useState<{focus: boolean} | null>(null);
    return (
        <div>
            <Submenu
                label='Parent'
                icon={<span/>}
                menuLabel='Child menu'
                open={state !== null}
                focusOnOpen={state?.focus ?? false}
                onOpen={(focus) => setState({focus})}
                onClose={() => setState(null)}
            >
                <button type='button'>{'Child item'}</button>
            </Submenu>
            <button type='button'>{'Sibling'}</button>
        </div>
    );
};

// Two rows sharing one open-submenu state, as the real parent menus have.
const SiblingsHarness = () => {
    const [openId, setOpenId] = useState<string | null>(null);
    return (
        <div>
            {['First', 'Second'].map((name) => (
                <Submenu
                    key={name}
                    label={name}
                    icon={<span/>}
                    menuLabel={`${name} menu`}
                    open={openId === name}
                    focusOnOpen={false}
                    onOpen={() => setOpenId(name)}
                    onClose={() => setOpenId(null)}
                >
                    <button type='button'>{`${name} item`}</button>
                </Submenu>
            ))}
        </div>
    );
};

const row = () => screen.getByRole('menuitem', {name: 'Parent'});
const entry = () => row().parentElement as HTMLElement;

// The row's state (the child menu stays mounted through its exit animation).
const childOpen = () => row().getAttribute('aria-expanded') === 'true';

describe('Submenu closing', () => {
    beforeEach(() => {
        jest.useFakeTimers();
    });

    afterEach(() => {
        jest.useRealTimers();
    });

    test('a menu that hover opened closes after the pointer leaves the row and the child menu', () => {
        render(<Harness/>);

        fireEvent.mouseEnter(row());
        expect(childOpen()).toBe(true);

        fireEvent.mouseLeave(entry());
        act(() => {
            jest.advanceTimersByTime(200);
        });
        expect(childOpen()).toBe(false);
    });

    test('coming back before the delay ends keeps it open', () => {
        render(<Harness/>);
        fireEvent.mouseEnter(row());

        fireEvent.mouseLeave(entry());
        act(() => {
            jest.advanceTimersByTime(50);
        });
        fireEvent.mouseEnter(entry());
        act(() => {
            jest.advanceTimersByTime(500);
        });

        expect(childOpen()).toBe(true);
    });

    test('a menu opened by click stays open when the pointer leaves', () => {
        render(<Harness/>);
        fireEvent.click(row());
        expect(childOpen()).toBe(true);

        fireEvent.mouseLeave(entry());
        act(() => {
            jest.advanceTimersByTime(500);
        });

        expect(childOpen()).toBe(true);
    });
});

describe('Submenu siblings', () => {
    beforeEach(() => {
        jest.useFakeTimers();
    });

    afterEach(() => {
        jest.useRealTimers();
    });

    test('moving from one row to the next within the close delay leaves the next one open', () => {
        render(<SiblingsHarness/>);
        const first = screen.getByRole('menuitem', {name: 'First'});
        const second = screen.getByRole('menuitem', {name: 'Second'});

        fireEvent.mouseEnter(first);
        fireEvent.mouseLeave(first.parentElement as HTMLElement);
        fireEvent.mouseEnter(second);
        act(() => {
            jest.advanceTimersByTime(500);
        });

        expect(second.getAttribute('aria-expanded')).toBe('true');
        expect(first.getAttribute('aria-expanded')).toBe('false');
    });
});

describe('Submenu placement', () => {
    afterEach(() => {
        jest.restoreAllMocks();
    });

    test.each([
        {name: 'opens to the right, growing down, when it fits', rect: {top: 100, bottom: 300, right: 500}, x: 'right', y: 'down'},
        {name: 'grows up when growing down would pass the bottom of the window', rect: {top: 600, bottom: 900, right: 500}, x: 'right', y: 'up'},
        {name: 'opens to the left when the right side would pass the edge of the window', rect: {top: 100, bottom: 300, right: 1200}, x: 'left', y: 'down'},
    ])('$name', ({rect, x, y}) => {
        Object.defineProperty(window, 'innerWidth', {configurable: true, value: 1024});
        Object.defineProperty(window, 'innerHeight', {configurable: true, value: 768});
        jest.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({...rect, left: 0, width: 0, height: 0, x: 0, y: 0, toJSON: () => null} as DOMRect);
        render(<Harness/>);

        fireEvent.click(row());

        const anchor = screen.getByRole('menu', {name: 'Child menu'}).parentElement as HTMLElement;
        expect(anchor.getAttribute('data-x')).toBe(x);
        expect(anchor.getAttribute('data-y')).toBe(y);
    });
});
