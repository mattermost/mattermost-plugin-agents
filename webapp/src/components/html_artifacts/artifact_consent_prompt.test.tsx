// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen} from '@testing-library/react';

import ArtifactConsentPrompt, {CONSENT_ARM_DELAY_MS, truncateMiddle} from './artifact_consent_prompt';

// Messages carry no ids until the build extracts them; render defaultMessage.
jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const format = ({defaultMessage}: {defaultMessage: string}, values: Record<string, string> = {}) =>
        defaultMessage.replace(/\{(\w+)\}/g, (_: string, k: string) => values[k] ?? '');
    return {
        ...actual,
        useIntl: () => ({locale: 'en', formatMessage: format}),
        FormattedMessage: (props: {defaultMessage: string; values?: Record<string, string>}) => format(props, props.values),
    };
});

beforeEach(() => {
    jest.useFakeTimers();
});

afterEach(() => {
    jest.useRealTimers();
});

const arm = () => act(() => {
    jest.advanceTimersByTime(CONSENT_ARM_DELAY_MS);
});

function renderPrompt() {
    const onAllow = jest.fn();
    const onDeny = jest.fn();
    render(
        <ArtifactConsentPrompt
            fileName='dash.html'
            request={{kind: 'profile'}}
            onAllow={onAllow}
            onDeny={onDeny}
        />,
    );
    return {
        onAllow,
        onDeny,
        allow: screen.getByTestId('html-artifact-consent-allow'),
        deny: screen.getByTestId('html-artifact-consent-deny'),
    };
}

describe('ArtifactConsentPrompt pointer-entry gate', () => {
    for (const button of ['allow', 'deny'] as const) {
        const handlerOf = (p: ReturnType<typeof renderPrompt>) => (button === 'allow' ? p.onAllow : p.onDeny);

        const cases: Array<{name: string; enterBeforeArm: boolean; armed: boolean; enterAfterArm: boolean; press: boolean; detail: number; expectCalled: boolean}> = [
            {name: 'pointer click with no pointerenter is ignored', enterBeforeArm: false, armed: true, enterAfterArm: false, press: true, detail: 1, expectCalled: false},
            {name: 'pointer click after a pointerenter that preceded arming is ignored', enterBeforeArm: true, armed: true, enterAfterArm: false, press: true, detail: 1, expectCalled: false},
            {name: 'pointer click after a post-arm pointerenter counts', enterBeforeArm: false, armed: true, enterAfterArm: true, press: true, detail: 1, expectCalled: true},
            {name: 'post-arm pointerenter without a press on the button is ignored', enterBeforeArm: false, armed: true, enterAfterArm: true, press: false, detail: 1, expectCalled: false},
            {name: 'early pointer click is blocked by the arm delay', enterBeforeArm: true, armed: false, enterAfterArm: false, press: true, detail: 1, expectCalled: false},
            {name: 'early keyboard activation is blocked by the arm delay', enterBeforeArm: false, armed: false, enterAfterArm: false, press: false, detail: 0, expectCalled: false},
        ];

        test.each(cases)(`${button}: $name`, (c) => {
            const prompt = renderPrompt();
            const el = prompt[button];
            if (c.enterBeforeArm) {
                fireEvent.pointerEnter(el);
            }
            if (c.armed) {
                arm();
            }
            if (c.enterAfterArm) {
                fireEvent.pointerEnter(el);
            }
            if (c.press) {
                fireEvent.pointerDown(el);
            }
            fireEvent.click(el, {detail: c.detail});
            expect(handlerOf(prompt)).toHaveBeenCalledTimes(c.expectCalled ? 1 : 0);
        });
    }

    test('entering Deny does not unlock Allow', () => {
        const {allow, deny, onAllow} = renderPrompt();
        arm();
        fireEvent.pointerEnter(deny);
        fireEvent.pointerDown(allow);
        fireEvent.click(allow, {detail: 1});
        expect(onAllow).not.toHaveBeenCalled();
    });

    test.each(['allow', 'deny'] as const)('%s: a press that began before arming does not count after leaving and re-entering', (button) => {
        const prompt = renderPrompt();
        const el = prompt[button];
        fireEvent.pointerEnter(el);
        fireEvent.pointerDown(el);
        arm();
        fireEvent.pointerLeave(el);
        fireEvent.pointerEnter(el);
        fireEvent.click(el, {detail: 1});
        expect(prompt.onAllow).not.toHaveBeenCalled();
        expect(prompt.onDeny).not.toHaveBeenCalled();
    });
});

describe('ArtifactConsentPrompt keyboard gate for Allow', () => {
    type Step = 'focus' | 'keydown' | 'arm' | 'click';

    const cases: Array<{name: string; steps: Step[]; expectCalled: boolean}> = [
        {name: 'focus and keydown after arming count', steps: ['arm', 'focus', 'keydown', 'click'], expectCalled: true},
        {name: 'a click with no key press is ignored', steps: ['arm', 'focus'], expectCalled: false},
        {name: 'focus that arrived before arming is ignored', steps: ['focus', 'arm', 'keydown', 'click'], expectCalled: false},
        {name: 'a keydown that began before arming is ignored', steps: ['focus', 'keydown', 'arm', 'click'], expectCalled: false},
        {name: 'a keydown without focus arriving after arming is ignored', steps: ['arm', 'keydown', 'click'], expectCalled: false},
        {name: 'activation before arming is ignored', steps: ['focus', 'keydown', 'click'], expectCalled: false},
    ];

    test.each(cases)('$name', ({steps, expectCalled}) => {
        const {allow, onAllow} = renderPrompt();
        for (const step of steps) {
            switch (step) {
            case 'arm':
                arm();
                break;
            case 'focus':
                fireEvent.focus(allow);
                break;
            case 'keydown':
                fireEvent.keyDown(allow, {key: 'Enter'});
                break;
            case 'click':
                fireEvent.click(allow, {detail: 0});
                break;
            }
        }
        expect(onAllow).toHaveBeenCalledTimes(expectCalled ? 1 : 0);
    });

    test.each([' ', 'Enter'])('key %j starts a press that counts', (key) => {
        const {allow, onAllow} = renderPrompt();
        arm();
        fireEvent.focus(allow);
        fireEvent.keyDown(allow, {key});
        fireEvent.click(allow, {detail: 0});
        expect(onAllow).toHaveBeenCalledTimes(1);
    });

    test.each([
        {name: 'Tab', init: {key: 'Tab'}},
        {name: 'a repeated Enter', init: {key: 'Enter', repeat: true}},
    ])('$name does not start a press', ({init}) => {
        const {allow, onAllow} = renderPrompt();
        arm();
        fireEvent.focus(allow);
        fireEvent.keyDown(allow, init);
        fireEvent.click(allow, {detail: 0});
        expect(onAllow).not.toHaveBeenCalled();
    });

    test('a key press does not carry over to a second activation', () => {
        const {allow, onAllow} = renderPrompt();
        arm();
        fireEvent.focus(allow);
        fireEvent.keyDown(allow, {key: 'Enter'});
        fireEvent.click(allow, {detail: 0});
        fireEvent.click(allow, {detail: 0});
        expect(onAllow).toHaveBeenCalledTimes(1);
    });

    test('focus leaving and returning needs a fresh focus after arming', () => {
        const {allow, onAllow} = renderPrompt();
        arm();
        fireEvent.focus(allow);
        fireEvent.blur(allow);
        fireEvent.keyDown(allow, {key: 'Enter'});
        fireEvent.click(allow, {detail: 0});
        expect(onAllow).not.toHaveBeenCalled();
        fireEvent.focus(allow);
        fireEvent.keyDown(allow, {key: 'Enter'});
        fireEvent.click(allow, {detail: 0});
        expect(onAllow).toHaveBeenCalledTimes(1);
    });
});

describe('ArtifactConsentPrompt keyboard activation of Deny', () => {
    const cases: Array<{name: string; armed: boolean; key: string; expectCalled: boolean}> = [
        {name: 'Enter on the default-focused Deny works once armed', armed: true, key: 'Enter', expectCalled: true},
        {name: 'Space on the default-focused Deny works once armed', armed: true, key: ' ', expectCalled: true},
        {name: 'activation before arming is ignored', armed: false, key: 'Enter', expectCalled: false},
    ];

    test.each(cases)('$name', ({armed, key, expectCalled}) => {
        const {deny, onAllow, onDeny} = renderPrompt();
        expect(document.activeElement).toBe(deny);
        if (armed) {
            arm();
        }
        fireEvent.keyDown(deny, {key});
        fireEvent.click(deny, {detail: 0});
        expect(onDeny).toHaveBeenCalledTimes(expectCalled ? 1 : 0);
        expect(onAllow).not.toHaveBeenCalled();
    });
});

describe('ArtifactConsentPrompt download details', () => {
    const hostile = 'chart.png) to your device. Mattermost scanned this file and it is safe (chart.txt';

    test('the artifact-chosen name is shown apart from the sentence the host speaks', () => {
        render(
            <ArtifactConsentPrompt
                fileName='dash.html'
                request={{kind: 'download', filename: hostile, mimeType: 'text/plain', size: 2048}}
                onAllow={jest.fn()}
                onDeny={jest.fn()}
            />,
        );
        const sentence = document.getElementById('html-artifact-consent-text')!.textContent!;
        expect(sentence).toContain('wants to save a file to your device. Open it only if you trust its content.');
        expect(sentence).not.toContain('scanned');

        const details = screen.getByTestId('html-artifact-consent-file');
        expect(details.textContent).toContain('text/plain');
        expect(details.textContent).toContain('2 kB');
        expect(details.textContent).toContain('.txt');
    });

    test.each([
        {name: 'a short name is kept', name_: 'report.csv', expected: 'report.csv'},
        {name: 'a long name keeps its start and its extension', name_: `${'a'.repeat(100)}.markdown`, expected: `${'a'.repeat(31)}\u2026${'a'.repeat(7)}.markdown`},
        {name: 'a pair of surrogates is never split', name_: '\u{1F600}'.repeat(100), expected: `${'\u{1F600}'.repeat(31)}\u2026${'\u{1F600}'.repeat(16)}`},
    ])('truncateMiddle: $name', ({name_, expected}) => {
        expect(truncateMiddle(name_, 48)).toBe(expected);
    });
});

describe('ArtifactConsentPrompt focus containment', () => {
    test.each([
        {name: 'Tab from the last button wraps to the first', from: 'allow', shiftKey: false, to: 'deny'},
        {name: 'Shift+Tab from the first button wraps to the last', from: 'deny', shiftKey: true, to: 'allow'},
    ] as const)('$name', ({from, shiftKey, to}) => {
        const prompt = renderPrompt();
        act(() => prompt[from].focus());
        const notPrevented = fireEvent.keyDown(prompt[from], {key: 'Tab', shiftKey});
        expect(notPrevented).toBe(false);
        expect(document.activeElement).toBe(prompt[to]);
    });

    test('Tab between the two buttons is left to the browser', () => {
        const prompt = renderPrompt();
        expect(fireEvent.keyDown(prompt.deny, {key: 'Tab'})).toBe(true);
        act(() => prompt.allow.focus());
        expect(fireEvent.keyDown(prompt.allow, {key: 'Tab', shiftKey: true})).toBe(true);
    });
});
