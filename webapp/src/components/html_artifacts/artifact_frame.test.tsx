// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen} from '@testing-library/react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';

import {resetConsentStore} from './artifact_broker';
import ArtifactConsentPrompt, {CONSENT_ARM_DELAY_MS} from './artifact_consent_prompt';
import ArtifactFrame from './artifact_frame';

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

const store = createStore(() => ({
    entities: {
        preferences: {myPreferences: {}},
        users: {currentUserId: 'u1', profiles: {u1: {id: 'u1', username: 'alice'}}},
    },
}));

function withProviders(children: React.ReactNode) {
    return (
        <Provider store={store}>
            {children}
        </Provider>
    );
}

beforeEach(() => {
    jest.useFakeTimers();
    resetConsentStore();
});

afterEach(() => {
    jest.useRealTimers();
});

describe('ArtifactFrame navigation guard', () => {
    const iframe = () => screen.queryByTitle(/Interactive artifact/) as HTMLIFrameElement | null;

    test('resets the frame once, then stops a self-navigating artifact until the viewer reloads', () => {
        render(withProviders(
            <ArtifactFrame
                fileId='abcdefghijklmnopqrstuvwxyz'
                fileName='loop.html'
                displayMode='inline'
            />,
        ));

        // Host load, then the artifact navigates: one automatic reset (new iframe element).
        const first = iframe()!;
        fireEvent.load(first);
        fireEvent.load(first);
        const second = iframe()!;
        expect(second).not.toBe(first);

        // It navigates again after the reset: the frame is unloaded, not reset again.
        fireEvent.load(second);
        fireEvent.load(second);
        expect(iframe()).toBeNull();
        expect(screen.getByTestId('html-artifact-navigation-stopped').textContent).toContain('kept trying to navigate away');

        // A manual reload brings the artifact back and re-arms the single reset.
        fireEvent.click(screen.getByRole('button', {name: 'Reload'}));
        const third = iframe()!;
        expect(third).not.toBeNull();
        fireEvent.load(third);
        fireEvent.load(third);
        const fourth = iframe()!;
        expect(fourth).not.toBe(third);
        fireEvent.load(fourth);
        fireEvent.load(fourth);
        expect(iframe()).toBeNull();
    });
});

describe('ArtifactConsentPrompt activation guard', () => {
    function renderPrompt() {
        const onAllow = jest.fn();
        const onDeny = jest.fn();
        render(withProviders(
            <ArtifactConsentPrompt
                fileName='dash.html'
                onAllow={onAllow}
                onDeny={onDeny}
            />,
        ));
        return {
            onAllow,
            onDeny,
            allow: screen.getByTestId('html-artifact-consent-allow'),
            deny: screen.getByTestId('html-artifact-consent-deny'),
        };
    }

    const arm = () => act(() => {
        jest.advanceTimersByTime(CONSENT_ARM_DELAY_MS);
    });

    test('shows a heading and focuses Deny', () => {
        const {deny} = renderPrompt();
        expect(screen.getByRole('alertdialog', {name: 'Allow access to your profile?'})).toBeTruthy();
        expect(document.activeElement).toBe(deny);
    });

    const cases: Array<{name: string; armed: boolean; pointerDownAfterMount: boolean; detail: number; expectCalled: boolean}> = [
        {name: 'pointer click right after the prompt appears is ignored', armed: false, pointerDownAfterMount: true, detail: 1, expectCalled: false},
        {name: 'keyboard activation right after the prompt appears is ignored', armed: false, pointerDownAfterMount: false, detail: 0, expectCalled: false},
        {name: 'pointer click whose press started before the prompt is ignored', armed: true, pointerDownAfterMount: false, detail: 1, expectCalled: false},
        {name: 'pointer click after the delay counts', armed: true, pointerDownAfterMount: true, detail: 1, expectCalled: true},
        {name: 'keyboard activation after the delay counts', armed: true, pointerDownAfterMount: false, detail: 0, expectCalled: true},
    ];

    for (const button of ['allow', 'deny'] as const) {
        for (const c of cases) {
            test(`${button}: ${c.name}`, () => {
                const prompt = renderPrompt();
                const el = prompt[button];
                if (c.armed) {
                    arm();
                }
                if (c.pointerDownAfterMount) {
                    fireEvent.pointerDown(el);
                }
                fireEvent.click(el, {detail: c.detail});
                const handler = button === 'allow' ? prompt.onAllow : prompt.onDeny;
                expect(handler).toHaveBeenCalledTimes(c.expectCalled ? 1 : 0);
            });
        }
    }

    test('buttons read as disabled until the delay passes', () => {
        const {allow, deny} = renderPrompt();
        expect(allow.getAttribute('aria-disabled')).toBe('true');
        expect(deny.getAttribute('aria-disabled')).toBe('true');
        arm();
        expect(allow.getAttribute('aria-disabled')).toBe('false');
        expect(deny.getAttribute('aria-disabled')).toBe('false');
    });
});
