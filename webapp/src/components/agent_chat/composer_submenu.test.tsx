// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import {act, fireEvent, render, screen} from '@testing-library/react';

import ComposerSubmenu from './composer_submenu';

const Harness = () => {
    const [state, setState] = useState<{focus: boolean} | null>(null);
    return (
        <div>
            <ComposerSubmenu
                label='Parent'
                icon={<span/>}
                menuLabel='Child menu'
                open={state !== null}
                focusOnOpen={state?.focus ?? false}
                onOpen={(focus) => setState({focus})}
                onClose={() => setState(null)}
            >
                <button type='button'>{'Child item'}</button>
            </ComposerSubmenu>
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
                <ComposerSubmenu
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
                </ComposerSubmenu>
            ))}
        </div>
    );
};

const row = () => screen.getByRole('menuitem', {name: 'Parent'});
const entry = () => row().parentElement as HTMLElement;

// The row's state (the child menu stays mounted through its exit animation).
const childOpen = () => row().getAttribute('aria-expanded') === 'true';

describe('ComposerSubmenu closing', () => {
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

describe('ComposerSubmenu siblings', () => {
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
