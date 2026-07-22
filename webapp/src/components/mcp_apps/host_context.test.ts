// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {buildHostStyleVariables, resolveAppTheme} from './host_context';

function stubComputedStyle(values: Record<string, string>, fontFamily = 'Test Sans') {
    jest.spyOn(window, 'getComputedStyle').mockImplementation(() => ({
        getPropertyValue: (name: string) => values[name] ?? '',
        fontFamily,
    } as CSSStyleDeclaration));
}

describe('resolveAppTheme', () => {
    afterEach(() => {
        jest.restoreAllMocks();
    });

    test('white background is light', () => {
        stubComputedStyle({'--center-channel-bg': '#ffffff'});
        expect(resolveAppTheme()).toBe('light');
    });

    test('dark hex background is dark', () => {
        stubComputedStyle({'--center-channel-bg': '#1b1d22'});
        expect(resolveAppTheme()).toBe('dark');
    });

    test('dark rgb background is dark', () => {
        stubComputedStyle({'--center-channel-bg': 'rgb(28, 30, 36)'});
        expect(resolveAppTheme()).toBe('dark');
    });

    test('unparseable background defaults to light', () => {
        stubComputedStyle({'--center-channel-bg': 'not-a-color'});
        expect(resolveAppTheme()).toBe('light');
    });
});

describe('buildHostStyleVariables', () => {
    afterEach(() => {
        jest.restoreAllMocks();
    });

    test('maps resolved literals and omits empty sources', () => {
        stubComputedStyle({
            '--center-channel-bg': '#ffffff',
            '--center-channel-color': '#3f4350',
            '--center-channel-color-rgb': '63, 67, 80',
            '--button-bg': '#1c58d9',

            // intentionally omit --button-color
        }, '"Open Sans", sans-serif');

        const vars = buildHostStyleVariables();
        expect(vars['--color-background-primary']).toBe('#ffffff');
        expect(vars['--color-text-primary']).toBe('#3f4350');
        expect(vars['--color-background-secondary']).toBe('rgba(63, 67, 80, 0.04)');
        expect(vars['--color-text-secondary']).toBe('rgba(63, 67, 80, 0.72)');
        expect(vars['--color-border-primary']).toBe('rgba(63, 67, 80, 0.16)');
        expect(vars['--color-background-info']).toBe('#1c58d9');
        expect(vars['--font-sans']).toBe('"Open Sans", sans-serif');
        expect(vars['--color-text-inverse']).toBeUndefined();
    });
});
