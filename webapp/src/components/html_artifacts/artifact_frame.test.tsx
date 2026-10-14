// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen} from '@testing-library/react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';

import {getArtifactToken} from '@/client';

import {getConsentDecision, isConsentPromptPending, resetConsentStore} from './artifact_broker';
import ArtifactConsentPrompt, {CONSENT_ARM_DELAY_MS} from './artifact_consent_prompt';
import ArtifactFrame, {HEARTBEAT_HIDE_MS, HEARTBEAT_STOP_MS} from './artifact_frame';
import {NUDGE_MS} from './artifact_interact_overlay';
import HTMLArtifactCard, {resetArtifactRunDecisions} from './html_artifact_card';
import HTMLArtifactFullscreen from './html_artifact_fullscreen';

jest.mock('@/client', () => ({
    artifactPath: (fileId: string) => `/plugins/mattermost-ai/artifacts/${fileId}`,
    artifactURL: (fileId: string, nonce: string) => `/plugins/mattermost-ai/artifacts/${fileId}?n=${nonce}`,
    fileDownloadURL: (fileId: string) => `/api/v4/files/${fileId}?download=1`,
    getArtifactToken: jest.fn(),
}));

const mockGetToken = getArtifactToken as jest.MockedFunction<typeof getArtifactToken>;
const TOKEN = 'bridge-token';
const FILE_ID = 'abcdefghijklmnopqrstuvwxyz';

// Lets the token fetch resolve, the wrapper mount and load (jsdom loads it
// as an empty document), and the artifact's frame get created inside it.
const flushLoad = () => act(async () => {
    for (let i = 0; i < 5; i++) {
        await Promise.resolve(); // eslint-disable-line no-await-in-loop
    }
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
    resetArtifactRunDecisions();
    mockGetToken.mockReset();
    mockGetToken.mockResolvedValue({token: TOKEN, tooLarge: false});
});

afterEach(() => {
    jest.useRealTimers();
});

const advance = (ms: number) => act(() => {
    jest.advanceTimersByTime(ms);
});

// jsdom marks every event dispatched from script as untrusted. Dispatching
// through its internal implementation stands in for a real user's click.
type JsdomImpl = {isTrusted: boolean; _dispatch: (event: JsdomImpl) => boolean};
function jsdomImpl(wrapper: object): JsdomImpl {
    const symbol = Object.getOwnPropertySymbols(wrapper).find((s) => s.description === 'impl');
    return (wrapper as Record<symbol, JsdomImpl>)[symbol!];
}
function trustedClick(el: Element) {
    const event = jsdomImpl(new MouseEvent('click', {bubbles: true, cancelable: true}));
    event.isTrusted = true;
    act(() => {
        jsdomImpl(el)._dispatch(event); // eslint-disable-line no-underscore-dangle
    });
}

// A host element outside every artifact, standing in for the message box.
function addComposer(): HTMLTextAreaElement {
    const composer = document.createElement('textarea');
    document.body.appendChild(composer);
    return composer;
}

// The host's frame, holding the wrapper document.
const wrapperIframe = () => screen.queryByTestId('html-artifact-wrapper') as HTMLIFrameElement | null;

// The sandboxed frame inside the wrapper that runs the artifact.
const innerIframe = (wrapper = wrapperIframe()) => (wrapper?.contentDocument?.querySelector('iframe') ?? null) as HTMLIFrameElement | null;

// Focus going into the artifact (the artifact focusing itself, or the
// viewer clicking it): on this page the wrapper becomes the active element.
const focusArtifact = (wrapper: HTMLIFrameElement) => act(() => {
    wrapper.focus();
    innerIframe(wrapper)!.focus();
});

function postFrom(source: Window | null, data: Record<string, unknown>) {
    act(() => {
        window.dispatchEvent(new MessageEvent('message', {data, origin: 'null', source}));
    });
}

// Renders an inline frame and walks it through the host load and the bridge's ready.
async function renderReadyFrame(props: Partial<React.ComponentProps<typeof ArtifactFrame>> = {}) {
    const result = render(withProviders(
        <ArtifactFrame
            fileId={FILE_ID}
            fileName='guard.html'
            displayMode='inline'
            {...props}
        />,
    ));
    await flushLoad();
    const frame = wrapperIframe()!;
    const inner = innerIframe(frame)!;
    const win = inner.contentWindow!;
    const send = (data: Record<string, unknown>) => postFrom(win, {...data, token: TOKEN});
    fireEvent.load(inner);
    send({mmArtifact: 1, type: 'ready'});
    return {...result, frame, inner, win, send};
}

function renderFrame(fileName = 'frame.html') {
    return render(withProviders(
        <ArtifactFrame
            fileId={FILE_ID}
            fileName={fileName}
            displayMode='inline'
        />,
    ));
}

const stopped = () => screen.queryByTestId('html-artifact-navigation-stopped');

describe('ArtifactFrame wrapper', () => {
    const wrapperDoc = (wrapper: HTMLIFrameElement) => new DOMParser().parseFromString(wrapper.getAttribute('srcdoc') ?? '', 'text/html');

    test('the host frame is an unsandboxed, script-free wrapper that only lets its child load the artifact', async () => {
        const fileName = '"><script>alert(1)</script><iframe src="https://evil.example/">.html';
        renderFrame(fileName);
        await flushLoad();
        const wrapper = wrapperIframe()!;

        expect(wrapper.hasAttribute('sandbox')).toBe(false);
        expect(wrapper.hasAttribute('src')).toBe(false);
        const srcdoc = wrapper.getAttribute('srcdoc')!;
        expect(srcdoc).not.toContain('alert(1)');
        expect(srcdoc).not.toContain('evil.example');

        const doc = wrapperDoc(wrapper);
        const csp = doc.querySelectorAll('meta[http-equiv="Content-Security-Policy"]');
        expect(csp).toHaveLength(1);
        expect(csp[0].getAttribute('content')).toBe(
            `default-src 'none'; style-src 'unsafe-inline'; frame-src http://localhost/plugins/mattermost-ai/artifacts/${FILE_ID}`,
        );
        expect(doc.querySelectorAll('script, iframe, object, embed, link, base')).toHaveLength(0);
    });

    test('the artifact runs in a sandboxed frame inside the wrapper, loaded with the nonce its token was issued for', async () => {
        renderFrame('inner.html');
        await flushLoad();
        const inner = innerIframe()!;

        expect(mockGetToken).toHaveBeenCalledTimes(1);
        const [fileId, nonce] = mockGetToken.mock.calls[0];
        expect(fileId).toBe(FILE_ID);
        expect(nonce).toMatch(/^[A-Za-z0-9_-]{22}$/);
        expect(inner.getAttribute('sandbox')).toBe('allow-scripts');
        expect(inner.getAttribute('referrerpolicy')).toBe('no-referrer');
        expect(inner.getAttribute('src')).toBe(`/plugins/mattermost-ai/artifacts/${FILE_ID}?n=${nonce}`);
        expect(inner.getAttribute('title')).toBe('Interactive artifact: inner.html');
    });

    test.each([
        {name: 'the artifact frame inside the wrapper', source: 'inner', answered: true},
        {name: 'the wrapper document', source: 'wrapper', answered: false},
        {name: 'the host page itself', source: 'host', answered: false},
    ])('a message from $name is answered: $answered', async ({source, answered}) => {
        renderFrame();
        await flushLoad();
        const wrapper = wrapperIframe()!;
        const inner = innerIframe(wrapper)!;
        fireEvent.load(inner);
        const posted = jest.spyOn(inner.contentWindow!, 'postMessage');
        const from = {inner: inner.contentWindow, wrapper: wrapper.contentWindow, host: window}[source]!;
        postFrom(from, {mmArtifact: 1, type: 'ready', token: TOKEN});
        expect(posted).toHaveBeenCalledTimes(answered ? 1 : 0);
        if (answered) {
            expect(posted).toHaveBeenCalledWith(expect.objectContaining({type: 'context'}), '*');
        }
    });

    test('each host load uses a new nonce and only that load\'s token', async () => {
        mockGetToken.
            mockResolvedValueOnce({token: 'first-token', tooLarge: false}).
            mockResolvedValueOnce({token: 'second-token', tooLarge: false});
        renderFrame();
        await flushLoad();
        const firstInner = innerIframe()!;
        fireEvent.load(firstInner);
        fireEvent.load(firstInner);
        expect(stopped()).not.toBeNull();

        fireEvent.click(screen.getByRole('button', {name: 'Reload'}));
        await flushLoad();
        expect(mockGetToken).toHaveBeenCalledTimes(2);
        const [firstNonce, secondNonce] = mockGetToken.mock.calls.map(([, nonce]) => nonce);
        expect(secondNonce).not.toBe(firstNonce);

        const inner = innerIframe()!;
        expect(inner).not.toBe(firstInner);
        expect(inner.getAttribute('src')).toBe(`/plugins/mattermost-ai/artifacts/${FILE_ID}?n=${secondNonce}`);
        fireEvent.load(inner);
        const posted = jest.spyOn(inner.contentWindow!, 'postMessage');
        postFrom(inner.contentWindow, {mmArtifact: 1, type: 'ready', token: 'first-token'});
        expect(posted).not.toHaveBeenCalled();
        postFrom(inner.contentWindow, {mmArtifact: 1, type: 'ready', token: 'second-token'});
        expect(posted).toHaveBeenCalledTimes(1);
    });

    test('Escape in the wrapper document closes the fullscreen viewer', async () => {
        const onClose = jest.fn();
        render(withProviders(
            <HTMLArtifactFullscreen
                fileId={FILE_ID}
                fileName='esc.html'
                onClose={onClose}
            />,
        ));
        await flushLoad();
        const doc = wrapperIframe()!.contentDocument!;
        act(() => {
            doc.dispatchEvent(new KeyboardEvent('keydown', {key: 'a', bubbles: true}));
        });
        expect(onClose).not.toHaveBeenCalled();
        act(() => {
            doc.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape', bubbles: true}));
        });
        expect(onClose).toHaveBeenCalledTimes(1);
    });
});

describe('ArtifactFrame navigation guard', () => {
    test('unloads a self-navigating artifact without remounting until the viewer reloads', async () => {
        renderFrame('nav.html');
        await flushLoad();

        // Host load, then the artifact navigates: the frame is unloaded, never remounted.
        const first = wrapperIframe()!;
        fireEvent.load(innerIframe(first)!);
        expect(wrapperIframe()).toBe(first);
        fireEvent.load(innerIframe(first)!);
        expect(wrapperIframe()).toBeNull();
        expect(screen.getByTestId('html-artifact-navigation-stopped').textContent).toContain('tried to navigate away');

        // A manual reload mounts a fresh frame, guarded the same way.
        fireEvent.click(screen.getByRole('button', {name: 'Reload'}));
        await flushLoad();
        const second = wrapperIframe()!;
        expect(second).not.toBeNull();
        expect(second).not.toBe(first);
        fireEvent.load(innerIframe(second)!);
        fireEvent.load(innerIframe(second)!);
        expect(wrapperIframe()).toBeNull();
    });

    test.each([
        {name: 'the artifact frame loads a second document', reload: 'inner'},
        {name: 'the artifact frame reports an error after loading (Firefox, blocked navigation)', reload: 'inner-error'},
        {name: 'the wrapper document is replaced', reload: 'wrapper'},
    ])('stops answering the frame and drops a pending consent prompt when $name', async ({reload}) => {
        const {frame, inner, win, send} = await renderReadyFrame({startedByClick: true});
        const posted = jest.spyOn(win, 'postMessage');
        send({mmArtifact: 1, type: 'request', id: 'r1', method: 'getCurrentUser'});
        expect(screen.getByTestId('html-artifact-consent-deny')).toBeTruthy();

        if (reload === 'inner-error') {
            fireEvent.error(inner);
        } else {
            fireEvent.load(reload === 'inner' ? inner : frame);
        }
        expect(screen.queryByTestId('html-artifact-consent-deny')).toBeNull();
        expect(stopped()?.getAttribute('data-stop-reason')).toBe('navigation');

        // The replacement document gets no answers, even to a fresh ready/request.
        posted.mockClear();
        send({mmArtifact: 1, type: 'ready'});
        send({mmArtifact: 1, type: 'request', id: 'r2', method: 'getCurrentUser'});
        expect(posted).not.toHaveBeenCalled();
    });
});

describe('ArtifactFrame heartbeat watchdog', () => {
    const unresponsive = () => screen.queryByTestId('html-artifact-unresponsive');
    const heartbeat = {mmArtifact: 1, type: 'heartbeat'};

    let visibility: DocumentVisibilityState = 'visible';
    beforeEach(() => {
        visibility = 'visible';
        Object.defineProperty(document, 'visibilityState', {configurable: true, get: () => visibility});
    });
    afterEach(() => {
        delete (document as {visibilityState?: unknown}).visibilityState;
    });
    const setVisibility = (state: DocumentVisibilityState) => act(() => {
        visibility = state;
        document.dispatchEvent(new Event('visibilitychange'));
    });

    test('hides a frame whose heartbeat lapses, and shows it again when the heartbeat resumes', async () => {
        const {frame, send} = await renderReadyFrame();
        send(heartbeat);
        advance(HEARTBEAT_HIDE_MS - 250);
        expect(unresponsive()).toBeNull();
        expect(window.getComputedStyle(frame).visibility).toBe('visible');

        advance(500);
        expect(unresponsive()?.textContent).toContain('stopped responding');
        expect(window.getComputedStyle(frame).visibility).toBe('hidden');
        expect(wrapperIframe()).toBe(frame);

        send(heartbeat);
        expect(unresponsive()).toBeNull();
        expect(window.getComputedStyle(frame).visibility).toBe('visible');
        expect(stopped()).toBeNull();
    });

    test('unloads a frame whose heartbeat stays gone, dropping its prompt and answering nothing more', async () => {
        const {frame, win, send} = await renderReadyFrame({startedByClick: true});
        send(heartbeat);
        send({mmArtifact: 1, type: 'request', id: 'r1', method: 'getCurrentUser'});
        expect(screen.getByTestId('html-artifact-consent')).toBeTruthy();

        const posted = jest.spyOn(win, 'postMessage');
        advance(HEARTBEAT_STOP_MS + 500);
        expect(wrapperIframe()).toBeNull();
        expect(stopped()?.getAttribute('data-stop-reason')).toBe('unresponsive');
        expect(stopped()?.textContent).toContain('stopped responding and was unloaded');
        expect(screen.queryByTestId('html-artifact-consent')).toBeNull();

        posted.mockClear();
        send({mmArtifact: 1, type: 'ready'});
        send({mmArtifact: 1, type: 'request', id: 'r2', method: 'getCurrentUser'});
        expect(posted).not.toHaveBeenCalled();

        fireEvent.click(screen.getByRole('button', {name: 'Reload'}));
        await flushLoad();
        expect(wrapperIframe()).not.toBeNull();
        expect(wrapperIframe()).not.toBe(frame);
        expect(stopped()).toBeNull();
    });

    test('does not watch a frame before its first heartbeat', async () => {
        await renderReadyFrame();
        advance(HEARTBEAT_STOP_MS * 3);
        expect(unresponsive()).toBeNull();
        expect(stopped()).toBeNull();
        expect(wrapperIframe()).not.toBeNull();
    });

    // The bridge's first heartbeat goes out before a parser-blocking
    // location.replace() swaps the document: that frame never gets ready,
    // so the ready timeout unloads it and offers a retry.
    test('unloads a frame that never got ready when the ready timeout fires, and retry remounts it', async () => {
        renderFrame('replaced.html');
        await flushLoad();
        const frame = wrapperIframe()!;
        const inner = innerIframe(frame)!;
        postFrom(inner.contentWindow, {...heartbeat, token: TOKEN});
        fireEvent.load(inner);
        advance(HEARTBEAT_STOP_MS + 500);
        expect(stopped()).toBeNull();
        expect(wrapperIframe()).toBe(frame);
        expect(window.getComputedStyle(frame).visibility).toBe('hidden');
        advance(10000);
        expect(wrapperIframe()).toBeNull();
        expect(screen.getByRole('alert').textContent).toContain('could not be loaded');
        expect(stopped()).toBeNull();

        fireEvent.click(screen.getByRole('button', {name: 'Retry'}));
        await flushLoad();
        expect(wrapperIframe()).not.toBeNull();
        expect(wrapperIframe()).not.toBe(frame);
    });

    test('a late check with a stale heartbeat hides the frame but does not unload it', async () => {
        let t = 1000;
        const now = jest.spyOn(performance, 'now').mockImplementation(() => t);
        try {
            const {frame, send} = await renderReadyFrame();
            send(heartbeat);

            t += HEARTBEAT_STOP_MS + 1000;
            advance(250);
            expect(unresponsive()).not.toBeNull();
            expect(window.getComputedStyle(frame).visibility).toBe('hidden');
            expect(stopped()).toBeNull();
            expect(wrapperIframe()).toBe(frame);

            t += HEARTBEAT_STOP_MS / 2;
            advance(250);
            expect(stopped()).toBeNull();

            send(heartbeat);
            expect(unresponsive()).toBeNull();
            expect(window.getComputedStyle(frame).visibility).toBe('visible');

            for (let elapsed = 0; elapsed <= HEARTBEAT_STOP_MS + 500; elapsed += 250) {
                t += 250;
                advance(250);
            }
            expect(stopped()?.getAttribute('data-stop-reason')).toBe('unresponsive');
        } finally {
            now.mockRestore();
        }
    });

    test('pauses while the page is hidden and grants a fresh grace period once visible', async () => {
        const {send} = await renderReadyFrame();
        send(heartbeat);
        setVisibility('hidden');
        advance(HEARTBEAT_STOP_MS * 10);
        expect(stopped()).toBeNull();
        expect(unresponsive()).toBeNull();

        setVisibility('visible');
        advance(HEARTBEAT_HIDE_MS - 250);
        expect(unresponsive()).toBeNull();
        advance(HEARTBEAT_STOP_MS);
        expect(stopped()?.getAttribute('data-stop-reason')).toBe('unresponsive');
    });

    test('a frame stopped by its heartbeat stays down without the viewer reloading', async () => {
        const {send} = await renderReadyFrame();
        send(heartbeat);
        advance(HEARTBEAT_STOP_MS + 500);
        expect(wrapperIframe()).toBeNull();
        send(heartbeat);
        advance(HEARTBEAT_STOP_MS);
        expect(wrapperIframe()).toBeNull();
        expect(mockGetToken).toHaveBeenCalledTimes(1);
    });
});

describe('ArtifactFrame focus guard', () => {
    const interact = () => screen.queryByTestId('html-artifact-interact');
    let composer: HTMLTextAreaElement;
    beforeEach(() => {
        composer = addComposer();
    });
    afterEach(() => {
        composer.remove();
    });

    test('covers an artifact the viewer has not engaged; only a trusted click engages it and focuses the artifact', async () => {
        const {frame, inner} = await renderReadyFrame();
        expect(interact()).not.toBeNull();
        expect(interact()!.tagName).toBe('BUTTON');
        expect(interact()!.getAttribute('aria-label')).toBe('Interact with this artifact');
        expect(frame.getAttribute('tabindex')).toBe('-1');
        expect(inner.getAttribute('tabindex')).toBe('-1');
        expect(frame.hasAttribute('inert')).toBe(false);

        fireEvent.click(interact()!);
        expect(interact()).not.toBeNull();

        trustedClick(interact()!);
        expect(interact()).toBeNull();
        expect(frame.hasAttribute('tabindex')).toBe(false);
        expect(inner.hasAttribute('tabindex')).toBe(false);
        expect(document.activeElement).toBe(frame);
        expect(frame.contentDocument!.activeElement).toBe(inner);
        advance(1000);
        expect(stopped()).toBeNull();
    });

    test.each([
        {name: 'focused before the artifact mounted', focusBeforeMount: true},
        {name: 'focused after the artifact mounted', focusBeforeMount: false},
    ])('stops an artifact that takes focus unasked and returns it to the page element $name', async ({focusBeforeMount}) => {
        if (focusBeforeMount) {
            composer.focus();
        }
        const {frame} = await renderReadyFrame();
        if (!focusBeforeMount) {
            composer.focus();
        }

        focusArtifact(frame);
        advance(300);
        expect(wrapperIframe()).toBeNull();
        expect(stopped()?.getAttribute('data-stop-reason')).toBe('focus');
        expect(stopped()?.textContent).toContain('tried to take keyboard focus');
        expect(document.activeElement).toBe(composer);
    });

    test('an engaged artifact may hold focus until the viewer focuses the page again', async () => {
        const {frame, inner} = await renderReadyFrame({startedByClick: true});
        expect(interact()).toBeNull();
        expect(inner.hasAttribute('tabindex')).toBe(false);
        focusArtifact(frame);
        advance(1000);
        expect(stopped()).toBeNull();

        act(() => composer.focus());
        expect(interact()).not.toBeNull();
        expect(frame.getAttribute('tabindex')).toBe('-1');
        expect(inner.getAttribute('tabindex')).toBe('-1');

        focusArtifact(frame);
        advance(300);
        expect(stopped()?.getAttribute('data-stop-reason')).toBe('focus');
        expect(document.activeElement).toBe(composer);
    });

    test.each([
        {name: 'a trusted click on Reload after a stop', trigger: 'stop', button: 'Reload', trusted: true},
        {name: 'a scripted click on Reload after a stop', trigger: 'stop', button: 'Reload', trusted: false},
        {name: 'a trusted click on Retry after a failed load', trigger: 'error', button: 'Retry', trusted: true},
        {name: 'a scripted click on Retry after a failed load', trigger: 'error', button: 'Retry', trusted: false},
    ])('$name starts the new load engaged only when trusted', async ({trigger, button, trusted}) => {
        renderFrame('reload.html');
        await flushLoad();
        const first = innerIframe()!;
        if (trigger === 'stop') {
            fireEvent.load(first);
            fireEvent.load(first);
            expect(stopped()).not.toBeNull();
        } else {
            fireEvent.error(first);
            expect(screen.getByRole('alert').textContent).toContain('could not be loaded');
        }

        const control = screen.getByRole('button', {name: button});
        if (trusted) {
            trustedClick(control);
        } else {
            fireEvent.click(control);
        }
        await flushLoad();
        const frame = wrapperIframe()!;
        const inner = innerIframe(frame)!;
        fireEvent.load(inner);
        postFrom(inner.contentWindow, {mmArtifact: 1, type: 'ready', token: TOKEN});

        expect(screen.getByTestId('html-artifact-frame').getAttribute('data-engaged')).toBe(String(trusted));
        expect(interact() === null).toBe(trusted);
        expect(document.activeElement === frame).toBe(trusted);
        if (trusted) {
            expect(frame.contentDocument!.activeElement).toBe(inner);
        }
        advance(1000);
        expect(stopped()).toBeNull();
    });

    test('a click on page content that takes no focus ends engagement without stopping the artifact', async () => {
        const {frame} = await renderReadyFrame({startedByClick: true});
        focusArtifact(frame);
        expect(document.activeElement).toBe(frame);

        act(() => {
            composer.dispatchEvent(new MouseEvent('pointerdown', {bubbles: true}));
        });
        expect(document.activeElement).not.toBe(frame);
        expect(interact()).not.toBeNull();
        advance(1000);
        expect(stopped()).toBeNull();

        focusArtifact(frame);
        advance(300);
        expect(stopped()?.getAttribute('data-stop-reason')).toBe('focus');
    });

    test('a pointerdown inside the frame keeps it engaged', async () => {
        const {frame} = await renderReadyFrame({startedByClick: true});
        act(() => {
            frame.dispatchEvent(new MouseEvent('pointerdown', {bubbles: true}));
        });
        expect(interact()).toBeNull();
    });

    test('an artifact the viewer has not engaged cannot open the consent prompt', async () => {
        const {win, send} = await renderReadyFrame();
        const posted = jest.spyOn(win, 'postMessage');
        send({mmArtifact: 1, type: 'request', id: 'r1', method: 'getCurrentUser'});
        expect(screen.queryByTestId('html-artifact-consent')).toBeNull();
        expect(posted).toHaveBeenCalledWith(expect.objectContaining({id: 'r1', error: expect.objectContaining({code: 'busy'})}), '*');

        trustedClick(interact()!);
        send({mmArtifact: 1, type: 'request', id: 'r2', method: 'getCurrentUser'});
        expect(screen.getByTestId('html-artifact-consent')).toBeTruthy();
    });

    const answerPrompt = () => {
        act(() => {
            jest.advanceTimersByTime(CONSENT_ARM_DELAY_MS);
        });
        const allow = screen.getByTestId('html-artifact-consent-allow');
        act(() => allow.focus());
        fireEvent.keyDown(allow, {key: 'Enter'});
        fireEvent.click(allow);
        expect(screen.queryByTestId('html-artifact-consent')).toBeNull();
    };

    test('answering the consent prompt hands focus back to the engaged artifact without tripping the guard', async () => {
        const {frame, inner, send} = await renderReadyFrame({startedByClick: true});
        focusArtifact(frame);
        send({mmArtifact: 1, type: 'request', id: 'r1', method: 'getCurrentUser'});
        expect(document.activeElement).toBe(screen.getByTestId('html-artifact-consent-deny'));

        // The viewer goes back to the message box before answering.
        act(() => composer.focus());

        // Browsers leave keystrokes in the wrapper document when only the
        // wrapper gets focus back, so the artifact's frame must be focused again.
        const innerFocus = jest.spyOn(inner, 'focus');
        answerPrompt();
        expect(document.activeElement).toBe(frame);
        expect(frame.contentDocument!.activeElement).toBe(inner);
        expect(innerFocus).toHaveBeenCalled();
        advance(1000);
        expect(stopped()).toBeNull();
        expect(wrapperIframe()).toBe(frame);
    });

    test('a heartbeat lapse drops the open consent prompt without recording a decision', async () => {
        const {frame, win, send} = await renderReadyFrame({startedByClick: true});
        act(() => frame.focus());
        send({mmArtifact: 1, type: 'heartbeat'});
        send({mmArtifact: 1, type: 'request', id: 'r1', method: 'getCurrentUser'});
        expect(screen.getByTestId('html-artifact-consent')).toBeTruthy();

        const posted = jest.spyOn(win, 'postMessage');
        advance(HEARTBEAT_HIDE_MS + 500);
        await act(async () => {
            await Promise.resolve();
        });
        expect(screen.getByTestId('html-artifact-unresponsive')).toBeTruthy();
        expect(screen.queryByTestId('html-artifact-consent')).toBeNull();
        expect(getConsentDecision('u1', 'abcdefghijklmnopqrstuvwxyz')).toBeUndefined();
        expect(isConsentPromptPending()).toBe(false);
        expect(posted).not.toHaveBeenCalledWith(expect.objectContaining({result: expect.anything()}), '*');

        // Once the bridge is back and the viewer engages again, it must ask again.
        send({mmArtifact: 1, type: 'heartbeat'});
        trustedClick(interact()!);
        send({mmArtifact: 1, type: 'request', id: 'r2', method: 'getCurrentUser'});
        expect(screen.getByTestId('html-artifact-consent')).toBeTruthy();
        expect(posted).not.toHaveBeenCalledWith(expect.objectContaining({result: expect.anything()}), '*');
    });

    test('an artifact that takes focus and opens a prompt at once gets no prompt and is stopped', async () => {
        const {frame, inner} = await renderReadyFrame();
        act(() => composer.focus());
        act(() => {
            frame.focus();
            inner.focus();
            window.dispatchEvent(new MessageEvent('message', {
                data: {mmArtifact: 1, type: 'request', id: 'r1', method: 'getCurrentUser', token: TOKEN},
                origin: 'null',
                source: inner.contentWindow,
            }));
        });
        expect(screen.queryByTestId('html-artifact-consent')).toBeNull();
        advance(300);
        expect(stopped()?.getAttribute('data-stop-reason')).toBe('focus');
        expect(document.activeElement).toBe(composer);
    });

    test('a frame that lost its heartbeat gives keyboard focus back to the page', async () => {
        const {frame, send} = await renderReadyFrame();
        act(() => composer.focus());
        trustedClick(interact()!);
        expect(document.activeElement).toBe(frame);
        send({mmArtifact: 1, type: 'heartbeat'});
        advance(HEARTBEAT_HIDE_MS + 500);
        expect(screen.getByTestId('html-artifact-unresponsive')).toBeTruthy();
        expect(document.activeElement).toBe(composer);
        expect(stopped()).toBeNull();
    });
});

describe('ArtifactFrame interact hint', () => {
    const interact = () => screen.queryByTestId('html-artifact-interact');
    const hint = () => screen.queryByTestId('html-artifact-interact-hint');
    let composer: HTMLTextAreaElement;
    beforeEach(() => {
        composer = addComposer();
    });
    afterEach(() => {
        composer.remove();
    });

    const expectHintShown = () => {
        expect(interact()).not.toBeNull();
        const style = window.getComputedStyle(hint()!);
        expect(style.opacity).not.toBe('0');
        expect(style.visibility).not.toBe('hidden');
        expect(style.display).not.toBe('none');
        expect(hint()!.textContent).toBe('Click to interact');
    };

    test.each([
        {name: 'on a frame no click started', startedByClick: false},
        {name: 'after focus leaves an engaged frame for the page', startedByClick: true},
    ])('the hint shows without hover or focus whenever the overlay does, $name', async ({startedByClick}) => {
        await renderReadyFrame({startedByClick});
        if (startedByClick) {
            expect(interact()).toBeNull();
            act(() => composer.focus());
        }
        expectHintShown();

        trustedClick(interact()!);
        expect(interact()).toBeNull();
        act(() => composer.focus());
        expectHintShown();
    });

    test.each([
        {name: 'a mouse wheel', scroll: (el: HTMLElement) => fireEvent.wheel(el, {deltaY: 120})},
        {name: 'a touch drag', scroll: (el: HTMLElement) => fireEvent.touchMove(el)},
    ])('scrolling with $name over the overlay nudges the hint for a moment without stopping the page scroll', async ({scroll}) => {
        await renderReadyFrame();
        expect(interact()!.getAttribute('data-nudge')).toBe('false');

        expect(scroll(interact()!)).toBe(true);
        expect(interact()!.getAttribute('data-nudge')).toBe('true');
        expect(hint()!.textContent).toBe('Click to scroll and interact');

        // Each scroll keeps the nudge going.
        advance(NUDGE_MS - 100);
        expect(scroll(interact()!)).toBe(true);
        advance(NUDGE_MS - 100);
        expect(interact()!.getAttribute('data-nudge')).toBe('true');

        advance(200);
        expect(interact()!.getAttribute('data-nudge')).toBe('false');
        expect(hint()!.textContent).toBe('Click to interact');
        expect(interact()!.getAttribute('aria-label')).toBe('Interact with this artifact');
    });

    test('a wheel over the overlay keeps its default action', async () => {
        await renderReadyFrame();
        const wheel = new WheelEvent('wheel', {bubbles: true, cancelable: true, deltaY: 120});
        act(() => {
            interact()!.dispatchEvent(wheel);
        });
        expect(wheel.defaultPrevented).toBe(false);
        expect(interact()!.getAttribute('data-nudge')).toBe('true');
    });
});

describe('ArtifactFrame bridge token', () => {
    test('shows loading and no frame until the token arrives', async () => {
        let resolve: (t: {token: string; tooLarge: boolean}) => void = () => {}; // eslint-disable-line no-empty-function
        mockGetToken.mockReturnValue(new Promise((r) => {
            resolve = r;
        }));
        renderFrame('tok.html');
        expect(wrapperIframe()).toBeNull();
        expect(screen.getByLabelText('Loading artifact')).toBeTruthy();
        await act(async () => {
            resolve({token: TOKEN, tooLarge: false});
        });
        await flushLoad();
        expect(wrapperIframe()).not.toBeNull();
        expect(innerIframe()).not.toBeNull();
    });

    // Firefox: a navigation the wrapper blocked before the artifact's first
    // load event only fires an error on its frame.
    test('unloads a frame that errors before it loaded, and retry remounts it', async () => {
        renderFrame('replaced.html');
        await flushLoad();
        fireEvent.error(innerIframe(wrapperIframe()!)!);
        expect(wrapperIframe()).toBeNull();
        expect(screen.getByRole('alert').textContent).toContain('could not be loaded');
        expect(stopped()).toBeNull();

        fireEvent.click(screen.getByRole('button', {name: 'Retry'}));
        await flushLoad();
        expect(innerIframe()).not.toBeNull();
    });

    test('shows a download prompt instead of loading a too-large artifact', async () => {
        mockGetToken.mockResolvedValueOnce({token: TOKEN, tooLarge: true});
        renderFrame('tok.html');
        await flushLoad();
        expect(wrapperIframe()).toBeNull();
        expect(screen.queryByLabelText('Loading artifact')).toBeNull();
        expect(screen.getByRole('alert').textContent).toContain('too large to preview');
        expect(screen.getByRole('button', {name: 'Download'})).toBeTruthy();
    });

    test('shows the error state on token failure and retries with a new nonce', async () => {
        mockGetToken.mockRejectedValueOnce(new Error('nope'));
        renderFrame('tok.html');
        await flushLoad();
        expect(wrapperIframe()).toBeNull();
        expect(screen.getByRole('alert').textContent).toContain('could not be loaded');
        fireEvent.click(screen.getByRole('button', {name: 'Retry'}));
        await flushLoad();
        expect(wrapperIframe()).not.toBeNull();
        expect(mockGetToken).toHaveBeenCalledTimes(2);
        expect(mockGetToken.mock.calls[1][1]).not.toBe(mockGetToken.mock.calls[0][1]);
    });

    test('answers only messages carrying the token', async () => {
        renderFrame('tok.html');
        await flushLoad();
        const inner = innerIframe()!;
        fireEvent.load(inner);
        const posted = jest.spyOn(inner.contentWindow!, 'postMessage');
        postFrom(inner.contentWindow, {mmArtifact: 1, type: 'ready'});
        postFrom(inner.contentWindow, {mmArtifact: 1, type: 'ready', token: 'guess'});
        expect(posted).not.toHaveBeenCalled();
        postFrom(inner.contentWindow, {mmArtifact: 1, type: 'ready', token: TOKEN});
        expect(posted).toHaveBeenCalledTimes(1);
    });
});

describe('ArtifactConsentPrompt focus handling', () => {
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
        render(withProviders(
            <ArtifactConsentPrompt
                fileName='dash.html'
                onAllow={jest.fn()}
                onDeny={jest.fn()}
            />,
        ));
        expect(screen.getByRole('alertdialog', {name: 'Allow access to your profile?'})).toBeTruthy();
        expect(document.activeElement).toBe(screen.getByTestId('html-artifact-consent-deny'));
    });

    test('buttons read as disabled until the delay passes', () => {
        render(withProviders(
            <ArtifactConsentPrompt
                fileName='dash.html'
                onAllow={jest.fn()}
                onDeny={jest.fn()}
            />,
        ));
        const allow = screen.getByTestId('html-artifact-consent-allow');
        const deny = screen.getByTestId('html-artifact-consent-deny');
        expect(allow.getAttribute('aria-disabled')).toBe('true');
        expect(deny.getAttribute('aria-disabled')).toBe('true');
        act(() => {
            jest.advanceTimersByTime(CONSENT_ARM_DELAY_MS);
        });
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

    // The artifact starts covered (Close takes focus on open), so once it is
    // ready the interact overlay stands in for the iframe as the tab stop.
    test.each([
        {name: 'end sentinel wraps to the first control', sentinel: 'html-artifact-fullscreen-sentinel-end', ready: true, expected: 'Download'},
        {name: 'start sentinel wraps to the interact overlay', sentinel: 'html-artifact-fullscreen-sentinel-start', ready: true, expected: 'Interact with this artifact'},
        {name: 'start sentinel skips the hidden loading iframe', sentinel: 'html-artifact-fullscreen-sentinel-start', ready: false, expected: 'Close'},
    ])('$name', async ({sentinel, ready, expected}) => {
        render(withProviders(
            <HTMLArtifactFullscreen
                fileId={FILE_ID}
                fileName='dash.html'
                onClose={jest.fn()}
            />,
        ));
        await flushLoad();
        const inner = innerIframe()!;
        expect(document.activeElement).toBe(screen.getByRole('button', {name: 'Close'}));
        if (ready) {
            postFrom(inner.contentWindow, {mmArtifact: 1, type: 'ready', token: TOKEN});
        }
        act(() => {
            screen.getByTestId(sentinel).focus();
        });
        expect(document.activeElement).toBe(screen.getByRole('button', {name: expected}));
        advance(1000);
        expect(stopped()).toBeNull();
    });

    test('an artifact that takes focus from the viewer\'s header controls is stopped', async () => {
        render(withProviders(
            <HTMLArtifactFullscreen
                fileId={FILE_ID}
                fileName='dash.html'
                onClose={jest.fn()}
            />,
        ));
        await flushLoad();
        const close = screen.getByRole('button', {name: 'Close'});
        expect(document.activeElement).toBe(close);
        focusArtifact(wrapperIframe()!);
        advance(300);
        expect(stopped()?.getAttribute('data-stop-reason')).toBe('focus');
        expect(document.activeElement).toBe(close);
    });

    test('skips controls without a layout box', async () => {
        render(withProviders(
            <HTMLArtifactFullscreen
                fileId={FILE_ID}
                fileName='dash.html'
                onClose={jest.fn()}
            />,
        ));
        await flushLoad();
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

describe('artifact safety notice', () => {
    test.each([
        {name: 'inline card', fileName: 'card.html', view: 'card', area: 'html-artifact-frame'},
        {name: 'inline card before Run', fileName: 'card.html', view: 'placeholder', area: 'html-artifact-run-placeholder'},
        {name: 'fullscreen viewer', fileName: 'full.html', view: 'fullscreen', area: 'html-artifact-frame'},
    ])('$name always shows the notice outside the frame area', async ({fileName, view, area}) => {
        render(withProviders(view === 'fullscreen' ? (
            <HTMLArtifactFullscreen
                fileId={FILE_ID}
                fileName={fileName}
                onClose={jest.fn()}
            />
        ) : (
            <HTMLArtifactCard
                fileId={FILE_ID}
                fileName={fileName}
            />
        )));
        if (view === 'card') {
            trustedClick(screen.getByTestId('html-artifact-run'));
        }
        await flushLoad();
        const notice = screen.getByRole('note');
        expect(notice.getAttribute('aria-label')).toMatch(/AI-generated.*Never enter passwords/);
        expect(notice.textContent).toContain('Don\'t enter passwords');
        expect(screen.getByTestId(area).contains(notice)).toBe(false);
    });
});

describe('HTMLArtifactCard run gate', () => {
    const placeholder = () => screen.queryByTestId('html-artifact-run-placeholder');

    function renderCard(fileId = FILE_ID) {
        return render(withProviders(
            <HTMLArtifactCard
                fileId={fileId}
                fileName='run.html'
            />,
        ));
    }

    test('shows Run and no frame until a trusted click on Run, then loads the artifact engaged', async () => {
        renderCard();
        await flushLoad();
        expect(placeholder()?.textContent).toContain('runs code only when you choose');
        expect(wrapperIframe()).toBeNull();
        expect(screen.queryByTestId('html-artifact-frame')).toBeNull();
        expect(screen.queryByTestId('html-artifact-reload')).toBeNull();
        expect(mockGetToken).not.toHaveBeenCalled();

        fireEvent.click(screen.getByTestId('html-artifact-run'));
        await flushLoad();
        expect(wrapperIframe()).toBeNull();

        trustedClick(screen.getByTestId('html-artifact-run'));
        await flushLoad();
        expect(placeholder()).toBeNull();
        expect(wrapperIframe()).not.toBeNull();
        expect(screen.getByTestId('html-artifact-frame').getAttribute('data-engaged')).toBe('true');
        expect(screen.getByTestId('html-artifact-reload')).toBeTruthy();
    });

    // Renders the card, runs it and unmounts it: the next card for the same
    // file runs without a click, as when it is rendered again in the RHS.
    async function renderRememberedCard() {
        const first = renderCard();
        trustedClick(screen.getByTestId('html-artifact-run'));
        await flushLoad();
        first.unmount();
        const again = renderCard();
        await flushLoad();
        return again;
    }

    const viewerFrame = () => {
        const viewer = screen.queryByTestId('html-artifact-fullscreen');
        return screen.getAllByTestId('html-artifact-frame').find((f) => viewer?.contains(f)) ?? null;
    };
    const inlineFrame = () => screen.getAllByTestId('html-artifact-frame').find((f) => f !== viewerFrame())!;
    const wrapperIn = (frame: HTMLElement) => frame.querySelector('iframe') as HTMLIFrameElement;

    function sendReadyIn(frame: HTMLElement) {
        const inner = innerIframe(wrapperIn(frame))!;
        fireEvent.load(inner);
        postFrom(inner.contentWindow, {mmArtifact: 1, type: 'ready', token: TOKEN});
    }

    const expectFocused = (frame: HTMLElement) => {
        const wrapper = wrapperIn(frame);
        expect(document.activeElement).toBe(wrapper);
        expect(wrapper.contentDocument!.activeElement).toBe(innerIframe(wrapper));
    };

    test.each([
        {name: 'Run', remembered: false, click: 'html-artifact-run', clicked: 'inline'},
        {name: 'Open fullscreen from the placeholder', remembered: false, click: 'html-artifact-open-fullscreen', clicked: 'fullscreen'},
        {name: 'Open fullscreen on a remembered run', remembered: true, click: 'html-artifact-open-fullscreen', clicked: 'fullscreen'},
        {name: 'Reload on a remembered run', remembered: true, click: 'html-artifact-reload', clicked: 'inline'},
    ])('a trusted click on $name engages the frame it starts and focuses it once ready', async ({remembered, click, clicked}) => {
        if (remembered) {
            await renderRememberedCard();
        } else {
            renderCard();
        }
        trustedClick(screen.getByTestId(click));
        await flushLoad();
        const target = clicked === 'fullscreen' ? viewerFrame()! : inlineFrame();
        expect(target.getAttribute('data-engaged')).toBe('true');

        screen.getAllByTestId('html-artifact-frame').forEach(sendReadyIn);
        expect(target.querySelector('[data-testid="html-artifact-interact"]')).toBeNull();
        expectFocused(target);

        // The inline frame under the fullscreen viewer is not what the viewer clicked.
        if (clicked === 'fullscreen') {
            expect(inlineFrame().getAttribute('data-engaged')).toBe('false');
            expect(inlineFrame().querySelector('[data-testid="html-artifact-interact"]')).not.toBeNull();
        }
        advance(1000);
        expect(stopped()).toBeNull();
        expectFocused(target);
    });

    test('a run remembered for the session starts covered: nothing is focused and an artifact taking focus is stopped', async () => {
        await renderRememberedCard();
        expect(placeholder()).toBeNull();
        const frame = wrapperIframe()!;
        sendReadyIn(screen.getByTestId('html-artifact-frame'));
        expect(screen.getByTestId('html-artifact-frame').getAttribute('data-engaged')).toBe('false');
        expect(screen.getByTestId('html-artifact-interact')).toBeTruthy();
        expect(document.activeElement).not.toBe(frame);

        focusArtifact(frame);
        advance(300);
        expect(stopped()?.getAttribute('data-stop-reason')).toBe('focus');
    });

    test('Run is remembered per viewer and file for the page session', async () => {
        const again = await renderRememberedCard();
        expect(placeholder()).toBeNull();
        expect(wrapperIframe()).not.toBeNull();
        again.unmount();

        renderCard('zyxwvutsrqponmlkjihgfedcba');
        await flushLoad();
        expect(placeholder()).not.toBeNull();
    });

    describe('when the viewer moves on before the artifact is ready', () => {
        let composer: HTMLTextAreaElement;
        beforeEach(() => {
            composer = addComposer();
        });
        afterEach(() => {
            composer.remove();
        });

        test.each([
            {name: 'focus moves to the message box', moveOn: 'focus'},
            {name: 'a click on page content that takes no focus', moveOn: 'pointerdown'},
        ])('$name: the overlay shows once ready and nothing is focused', async ({moveOn}) => {
            renderCard();
            trustedClick(screen.getByTestId('html-artifact-run'));
            await flushLoad();
            act(() => {
                if (moveOn === 'focus') {
                    composer.focus();
                } else {
                    composer.dispatchEvent(new MouseEvent('pointerdown', {bubbles: true}));
                }
            });
            const frame = screen.getByTestId('html-artifact-frame');
            expect(frame.getAttribute('data-engaged')).toBe('false');

            sendReadyIn(frame);
            expect(screen.getByTestId('html-artifact-interact')).toBeTruthy();
            expect(document.activeElement).not.toBe(wrapperIn(frame));
            advance(1000);
            expect(stopped()).toBeNull();
        });
    });

    test('the inline card and the fullscreen viewer each load with their own nonce', async () => {
        renderCard();
        trustedClick(screen.getByTestId('html-artifact-run'));
        await flushLoad();
        trustedClick(screen.getByTestId('html-artifact-open-fullscreen'));
        await flushLoad();
        expect(mockGetToken).toHaveBeenCalledTimes(2);
        const [cardNonce, fullscreenNonce] = mockGetToken.mock.calls.map(([, nonce]) => nonce);
        expect(fullscreenNonce).not.toBe(cardNonce);
        const srcs = screen.getAllByTestId('html-artifact-wrapper').map((w) => innerIframe(w as HTMLIFrameElement)!.getAttribute('src'));
        expect(srcs).toEqual([
            `/plugins/mattermost-ai/artifacts/${FILE_ID}?n=${cardNonce}`,
            `/plugins/mattermost-ai/artifacts/${FILE_ID}?n=${fullscreenNonce}`,
        ]);
    });
});
