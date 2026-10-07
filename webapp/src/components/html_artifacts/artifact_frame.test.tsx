// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen} from '@testing-library/react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';

import {getArtifactToken} from '@/client';

import {resetConsentStore} from './artifact_broker';
import ArtifactConsentPrompt, {CONSENT_ARM_DELAY_MS} from './artifact_consent_prompt';
import ArtifactFrame from './artifact_frame';
import HTMLArtifactFullscreen from './html_artifact_fullscreen';
import {resetArtifactTokenCache} from './artifact_token';

jest.mock('@/client', () => ({
    artifactURL: (fileId: string) => `/plugins/mattermost-ai/artifacts/${fileId}`,
    fileDownloadURL: (fileId: string) => `/api/v4/files/${fileId}?download=1`,
    getArtifactToken: jest.fn(),
}));

const mockGetToken = getArtifactToken as jest.MockedFunction<typeof getArtifactToken>;
const TOKEN = 'bridge-token';

// Lets the token fetch resolve and the frame mount.
const flushToken = () => act(async () => {
    await Promise.resolve();
});

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
    resetArtifactTokenCache();
    mockGetToken.mockReset();
    mockGetToken.mockResolvedValue(TOKEN);
});

afterEach(() => {
    jest.useRealTimers();
});

describe('ArtifactFrame navigation guard', () => {
    const iframe = () => screen.queryByTitle(/Interactive artifact/) as HTMLIFrameElement | null;

    test('unloads a self-navigating artifact without remounting until the viewer reloads', async () => {
        render(withProviders(
            <ArtifactFrame
                fileId='abcdefghijklmnopqrstuvwxyz'
                fileName='nav.html'
                displayMode='inline'
            />,
        ));

        await flushToken();

        // Host load, then the artifact navigates: the frame is unloaded, never remounted.
        const first = iframe()!;
        fireEvent.load(first);
        expect(iframe()).toBe(first);
        fireEvent.load(first);
        expect(iframe()).toBeNull();
        expect(screen.getByTestId('html-artifact-navigation-stopped').textContent).toContain('tried to navigate away');

        // A manual reload mounts a fresh frame, guarded the same way.
        fireEvent.click(screen.getByRole('button', {name: 'Reload'}));
        await flushToken();
        const second = iframe()!;
        expect(second).not.toBeNull();
        expect(second).not.toBe(first);
        fireEvent.load(second);
        fireEvent.load(second);
        expect(iframe()).toBeNull();
    });

    test('stops answering the frame and drops a pending consent prompt on a second load', async () => {
        render(withProviders(
            <ArtifactFrame
                fileId='abcdefghijklmnopqrstuvwxyz'
                fileName='nav.html'
                displayMode='inline'
            />,
        ));
        await flushToken();
        const frame = iframe()!;
        const win = frame.contentWindow!;
        const posted = jest.spyOn(win, 'postMessage');
        const send = (data: unknown) => act(() => {
            window.dispatchEvent(new MessageEvent('message', {data: {...(data as object), token: TOKEN}, origin: 'null', source: win}));
        });

        fireEvent.load(frame);
        send({mmArtifact: 1, type: 'ready'});
        send({mmArtifact: 1, type: 'request', id: 'r1', method: 'getCurrentUser'});
        expect(screen.getByTestId('html-artifact-consent-deny')).toBeTruthy();

        fireEvent.load(frame);
        expect(screen.queryByTestId('html-artifact-consent-deny')).toBeNull();

        // The replacement document gets no answers, even to a fresh ready/request.
        posted.mockClear();
        send({mmArtifact: 1, type: 'ready'});
        send({mmArtifact: 1, type: 'request', id: 'r2', method: 'getCurrentUser'});
        expect(posted).not.toHaveBeenCalled();
    });
});

describe('ArtifactFrame bridge token', () => {
    const iframe = () => screen.queryByTitle(/Interactive artifact/) as HTMLIFrameElement | null;

    function renderFrame() {
        return render(withProviders(
            <ArtifactFrame
                fileId='abcdefghijklmnopqrstuvwxyz'
                fileName='tok.html'
                displayMode='inline'
            />,
        ));
    }

    test('shows loading and no frame until the token arrives', async () => {
        let resolve: (t: string) => void = () => {}; // eslint-disable-line no-empty-function
        mockGetToken.mockReturnValue(new Promise<string>((r) => {
            resolve = r;
        }));
        renderFrame();
        expect(iframe()).toBeNull();
        expect(screen.getByLabelText('Loading artifact')).toBeTruthy();
        await act(async () => {
            resolve(TOKEN);
        });
        expect(iframe()).not.toBeNull();
    });

    test('shows the error state on token failure and retries', async () => {
        mockGetToken.mockRejectedValueOnce(new Error('nope'));
        renderFrame();
        await flushToken();
        expect(iframe()).toBeNull();
        expect(screen.getByRole('alert').textContent).toContain('could not be loaded');
        fireEvent.click(screen.getByRole('button', {name: 'Retry'}));
        await flushToken();
        expect(iframe()).not.toBeNull();
        expect(mockGetToken).toHaveBeenCalledTimes(2);
    });

    test('answers only messages carrying the token', async () => {
        renderFrame();
        await flushToken();
        const frame = iframe()!;
        fireEvent.load(frame);
        const posted = jest.spyOn(frame.contentWindow!, 'postMessage');
        const send = (data: unknown) => act(() => {
            window.dispatchEvent(new MessageEvent('message', {data, origin: 'null', source: frame.contentWindow}));
        });
        send({mmArtifact: 1, type: 'ready'});
        send({mmArtifact: 1, type: 'ready', token: 'guess'});
        expect(posted).not.toHaveBeenCalled();
        send({mmArtifact: 1, type: 'ready', token: TOKEN});
        expect(posted).toHaveBeenCalledTimes(1);
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

    test('restores the previously focused element when it goes away', () => {
        const outside = document.createElement('button');
        document.body.appendChild(outside);
        outside.focus();
        const {unmount} = render(withProviders(
            <ArtifactConsentPrompt
                fileName='dash.html'
                onAllow={jest.fn()}
                onDeny={jest.fn()}
            />,
        ));
        expect(document.activeElement).toBe(screen.getByTestId('html-artifact-consent-deny'));
        unmount();
        expect(document.activeElement).toBe(outside);
        outside.remove();
    });

    test('leaves focus alone when it moved out of the prompt before unmount', () => {
        const before = document.createElement('button');
        const elsewhere = document.createElement('button');
        document.body.append(before, elsewhere);
        before.focus();
        const {unmount} = render(withProviders(
            <ArtifactConsentPrompt
                fileName='dash.html'
                onAllow={jest.fn()}
                onDeny={jest.fn()}
            />,
        ));
        expect(document.activeElement).toBe(screen.getByTestId('html-artifact-consent-deny'));
        elsewhere.focus();
        unmount();
        expect(document.activeElement).toBe(elsewhere);
        before.remove();
        elsewhere.remove();
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

describe('HTMLArtifactFullscreen focus sentinels', () => {
    // jsdom has no layout: report a box for every element.
    let rects: jest.SpyInstance;
    beforeEach(() => {
        rects = jest.spyOn(HTMLElement.prototype, 'getClientRects').mockReturnValue([{}] as unknown as DOMRectList);
    });
    afterEach(() => rects.mockRestore());

    test.each([
        {name: 'end sentinel wraps to the first control', sentinel: 'html-artifact-fullscreen-sentinel-end', ready: true, expected: 'Download'},
        {name: 'start sentinel wraps to the ready iframe', sentinel: 'html-artifact-fullscreen-sentinel-start', ready: true, expected: 'iframe'},
        {name: 'start sentinel skips the hidden loading iframe', sentinel: 'html-artifact-fullscreen-sentinel-start', ready: false, expected: 'Close'},
    ])('$name', async ({sentinel, ready, expected}) => {
        render(withProviders(
            <HTMLArtifactFullscreen
                fileId='abcdefghijklmnopqrstuvwxyz'
                fileName='dash.html'
                onClose={jest.fn()}
            />,
        ));
        await flushToken();
        const frame = screen.getByTitle(/Interactive artifact/) as HTMLIFrameElement;
        if (ready) {
            act(() => {
                window.dispatchEvent(new MessageEvent('message', {data: {mmArtifact: 1, type: 'ready', token: TOKEN}, origin: 'null', source: frame.contentWindow}));
            });
        }
        act(() => {
            screen.getByTestId(sentinel).focus();
        });
        const want = expected === 'iframe' ? frame : screen.getByRole('button', {name: expected});
        expect(document.activeElement).toBe(want);
    });

    test('skips controls without a layout box', async () => {
        render(withProviders(
            <HTMLArtifactFullscreen
                fileId='abcdefghijklmnopqrstuvwxyz'
                fileName='dash.html'
                onClose={jest.fn()}
            />,
        ));
        await flushToken();
        const download = screen.getByRole('button', {name: 'Download'});
        rects.mockImplementation(function rectsFor(this: HTMLElement) {
            return (this === download ? [] : [{}]) as unknown as DOMRectList;
        });
        act(() => {
            screen.getByTestId('html-artifact-fullscreen-sentinel-end').focus();
        });
        expect(document.activeElement).toBe(screen.getByRole('button', {name: 'Reload'}));
    });
});
