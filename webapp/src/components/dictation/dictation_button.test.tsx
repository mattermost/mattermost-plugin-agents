// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useLayoutEffect, useState} from 'react';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';

import {DictationError, DictationTransport, TranscriptUpdate} from '@/dictation/types';

import DictationButton from './dictation_button';
import {EditorDraft} from './use_dictation';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
        }),
    };
});

jest.mock('@/license', () => ({
    useIsLicensedFor: jest.fn(() => true),
}));

jest.mock('@/dictation/availability', () => ({
    useDictationAvailable: jest.fn(() => true),
}));

jest.mock('@/dictation/webrtc_transcriber', () => ({
    WebRTCTranscriber: jest.fn(),
    isDictationSupported: jest.fn(() => true),
}));

const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};
const {useDictationAvailable} = jest.requireMock('@/dictation/availability') as {useDictationAvailable: jest.Mock};
const {isDictationSupported} = jest.requireMock('@/dictation/webrtc_transcriber') as {isDictationSupported: jest.Mock};

class FakeTransport implements DictationTransport {
    transcript: (u: TranscriptUpdate) => void = () => { /* set by hook */ };
    error: (e: DictationError) => void = () => { /* set by hook */ };
    idle: () => void = () => { /* set by hook */ };
    connecting: () => void = () => { /* set by hook */ };
    resolveStart: () => void = () => { /* set in start */ };
    rejectStart: (e: unknown) => void = () => { /* set in start */ };
    stop = jest.fn(async () => { /* stopped */ });

    start() {
        return new Promise<void>((resolve, reject) => {
            this.resolveStart = resolve;
            this.rejectStart = reject;
        });
    }

    onTranscript(h: (u: TranscriptUpdate) => void) {
        this.transcript = h;
    }

    onError(h: (e: DictationError) => void) {
        this.error = h;
    }

    onIdle(h: () => void) {
        this.idle = h;
    }

    onConnecting(h: () => void) {
        this.connecting = h;
    }
}

type HarnessControls = {
    setDraft: (update: (d: EditorDraft) => EditorDraft) => void;
};

// Harness behaves like the host editor: updateText writes the draft through
// React state, so the button sees its own writes on the next render.
// Frames queued with requestAnimationFrame, run on demand.
let queuedFrames: FrameRequestCallback[] = [];
const runQueuedFrames = () => {
    const frames = queuedFrames;
    queuedFrames = [];
    frames.forEach((frame) => frame(0));
};

// Runs queued frames in the layout phase after a channel switch, the way a
// browser frame can land between React's layout and passive effects.
const FrameBetweenEffects = ({channelId}: {channelId: string}) => {
    useLayoutEffect(() => {
        runQueuedFrames();
    }, [channelId]);
    return null;
};

type RenderOptions = {
    caret?: number;
    frameBetweenEffects?: boolean;
    onEditorKeyDown?: (e: React.KeyboardEvent) => void;
    maxSessionMs?: number;
};

function renderButton(initial: EditorDraft, {caret, onEditorKeyDown, maxSessionMs, frameBetweenEffects}: RenderOptions = {}) {
    const transports: FakeTransport[] = [];
    const controls: HarnessControls = {setDraft: () => { /* set on render */ }};
    const updateText = jest.fn();

    const Harness = () => {
        const [draft, setDraft] = useState(initial);
        controls.setDraft = setDraft;
        return (
            // eslint-disable-next-line agents/no-static-element-interactions -- stands in for the host editor's key handling
            <div onKeyDown={onEditorKeyDown}>
                <textarea
                    aria-label='editor'
                    readOnly={true}
                    value={draft.message}
                />
                <DictationButton
                    draft={draft}
                    getSelectedText={() => ({start: caret, end: caret})}
                    updateText={(message) => {
                        updateText(message);
                        setDraft((d) => ({...d, message}));
                    }}
                    createTransport={() => {
                        const t = new FakeTransport();
                        transports.push(t);
                        return t;
                    }}
                    maxSessionMs={maxSessionMs}
                />
                {frameBetweenEffects && <FrameBetweenEffects channelId={draft.channelId}/>}
                <div data-testid='draft'>{draft.message}</div>
            </div>
        );
    };
    const {unmount} = render(<Harness/>);
    return {transports, controls, updateText, unmount, draft: () => screen.getByTestId('draft').textContent};
}

const button = () => screen.getByTestId('dictation-button');

async function startListening(transport: () => FakeTransport) {
    fireEvent.click(button());
    await act(async () => {
        transport().connecting();
        transport().resolveStart();
    });
}

describe('DictationButton', () => {
    beforeEach(() => {
        useIsLicensedFor.mockReturnValue(true);
        useDictationAvailable.mockReturnValue(true);
        isDictationSupported.mockReturnValue(true);
    });

    test.each([
        {name: 'available and licensed', available: true, licensed: true, shown: true},
        {name: 'below Enterprise', available: true, licensed: false, shown: false},
        {name: 'not available', available: false, licensed: true, shown: false},
    ])('$name: shown=$shown', ({available, licensed, shown}) => {
        useDictationAvailable.mockReturnValue(available);
        useIsLicensedFor.mockReturnValue(licensed);
        renderButton({message: '', channelId: 'c1'});
        expect(screen.queryByTestId('dictation-button') !== null).toBe(shown);
        expect(useIsLicensedFor).toHaveBeenCalledWith('dictation');
    });

    test('is disabled with an explanation when the browser lacks WebRTC or microphone access', () => {
        isDictationSupported.mockReturnValue(false);
        renderButton({message: '', channelId: 'c1'});
        expect((button() as HTMLButtonElement).disabled).toBe(true);
        expect(screen.getByTestId('with-tooltip').getAttribute('data-title')).toBe('Dictation isn\'t supported in this browser');
    });

    test('moves through its states with matching labels', async () => {
        const {transports} = renderButton({message: '', channelId: 'c1'});
        expect(button().getAttribute('aria-label')).toBe('Start dictation');
        expect(button().getAttribute('aria-pressed')).toBe('false');

        fireEvent.click(button());
        expect(button().getAttribute('data-status')).toBe('requesting_permission');
        expect(button().getAttribute('aria-label')).toBe('Stop dictation');
        expect(button().getAttribute('aria-pressed')).toBe('true');

        act(() => transports[0].connecting());
        expect(button().getAttribute('data-status')).toBe('connecting');

        await act(async () => transports[0].resolveStart());
        expect(button().getAttribute('data-status')).toBe('listening');

        fireEvent.click(button());
        await waitFor(() => expect(button().getAttribute('data-status')).toBe('idle'));
        expect(transports[0].stop).toHaveBeenCalled();
        expect(button().getAttribute('aria-label')).toBe('Start dictation');
    });

    test('types the transcript at the caret as it grows', async () => {
        const {transports, draft} = renderButton({message: 'Hello world', channelId: 'c1'}, {caret: 5});
        await startListening(() => transports[0]);

        act(() => transports[0].transcript({text: 'big'}));
        await waitFor(() => expect(draft()).toBe('Hello big world'));

        act(() => transports[0].transcript({text: 'big brave'}));
        await waitFor(() => expect(draft()).toBe('Hello big brave world'));
    });

    test('a frame queued before a channel switch never writes into the new editor', async () => {
        const raf = jest.spyOn(window, 'requestAnimationFrame').mockImplementation((frame) => {
            queuedFrames.push(frame);
            return queuedFrames.length;
        });
        try {
            const {transports, controls, updateText, draft} = renderButton({message: 'old channel', channelId: 'c1'}, {frameBetweenEffects: true});
            await startListening(() => transports[0]);

            act(() => {
                transports[0].transcript({text: 'spoken'});
                controls.setDraft(() => ({message: 'new channel draft', channelId: 'c2'}));
            });

            await waitFor(() => expect(button().getAttribute('data-status')).toBe('idle'));
            expect(updateText).not.toHaveBeenCalled();
            expect(draft()).toBe('new channel draft');
        } finally {
            raf.mockRestore();
            queuedFrames = [];
        }
    });

    test('coalesces updates into one write per frame', async () => {
        const {transports, updateText, draft} = renderButton({message: '', channelId: 'c1'});
        await startListening(() => transports[0]);

        act(() => {
            transports[0].transcript({text: 'a'});
            transports[0].transcript({text: 'a b'});
            transports[0].transcript({text: 'a b c'});
        });
        await waitFor(() => expect(draft()).toBe('a b c'));
        expect(updateText).toHaveBeenCalledTimes(1);
    });

    test('Esc stops dictation without reaching the editor, which would cancel a post edit', async () => {
        const editorKeyDown = jest.fn();
        const {transports} = renderButton({message: '', channelId: 'c1'}, {onEditorKeyDown: editorKeyDown});
        await startListening(() => transports[0]);

        fireEvent.keyDown(screen.getByLabelText('editor'), {key: 'Escape'});
        await waitFor(() => expect(transports[0].stop).toHaveBeenCalled());
        expect(editorKeyDown).not.toHaveBeenCalled();

        await waitFor(() => expect(button().getAttribute('data-status')).toBe('idle'));
        fireEvent.keyDown(screen.getByLabelText('editor'), {key: 'Escape'});
        expect(editorKeyDown).toHaveBeenCalledTimes(1);
    });

    // Every way dictation ends goes through one stop. Streamed text differs
    // from the final text stop delivers, so a final write is visible.
    const PARTIAL = 'Hello big braid world';
    const FINAL = 'Hello big brave world';

    type Ctx = {transport: FakeTransport; controls: HarnessControls; unmount: () => void};
    type Row = {
        name: string;
        trigger: (ctx: Ctx) => unknown;
        duringStop?: (ctx: Ctx) => unknown;
        maxSessionMs?: number;

        // The transport already tore down (a provider error), so stop has no final to deliver.
        stopDeliversFinal?: boolean;
        unmounts?: boolean;
        draft?: string;
        status?: 'idle' | 'error';
    };

    const editor = () => screen.getByLabelText('editor');
    const setMessage = (ctx: Ctx, message: string) => ctx.controls.setDraft((d) => ({...d, message}));

    const rows: Row[] = [
        {name: 'the button', trigger: () => fireEvent.click(button()), draft: FINAL, status: 'idle'},
        {name: 'the button, with no final before the grace period ends', trigger: () => fireEvent.click(button()), stopDeliversFinal: false, draft: PARTIAL, status: 'idle'},
        {name: 'Esc', trigger: () => fireEvent.keyDown(editor(), {key: 'Escape'}), draft: FINAL, status: 'idle'},
        {name: 'silence',
            trigger: ({transport}) => {
                transport.idle();
            },
            draft: FINAL,
            status: 'idle'},
        {name: 'the session cap', maxSessionMs: 500, trigger: ({transport}) => waitFor(() => expect(transport.stop).toHaveBeenCalled(), {timeout: 2000}), draft: FINAL, status: 'idle'},
        {name: 'a provider error',
            trigger: ({transport}) => {
                transport.error(new DictationError('provider_error'));
            },
            stopDeliversFinal: false,
            draft: PARTIAL,
            status: 'error'},
        {name: 'typing', trigger: (ctx) => setMessage(ctx, PARTIAL + '!'), draft: PARTIAL + '!', status: 'idle'},
        {name: 'sending', trigger: (ctx) => setMessage(ctx, ''), draft: '', status: 'idle'},
        {name: 'a channel switch', trigger: ({controls}) => controls.setDraft((d) => ({...d, channelId: 'c2'})), draft: PARTIAL, status: 'idle'},
        {name: 'a thread switch', trigger: ({controls}) => controls.setDraft((d) => ({...d, rootId: 'r2'})), draft: PARTIAL, status: 'idle'},
        {name: 'unmounting', trigger: ({unmount}) => unmount(), unmounts: true},
        {name: 'the button, then typing while stopping', trigger: () => fireEvent.click(button()), duringStop: (ctx) => setMessage(ctx, 'mine'), draft: 'mine', status: 'idle'},
        {name: 'the button, then unmounting while stopping', trigger: () => fireEvent.click(button()), duringStop: ({unmount}) => unmount(), unmounts: true},
        {name: 'the button, then a provider error while stopping',
            trigger: () => fireEvent.click(button()),
            duringStop: ({transport}) => {
                transport.error(new DictationError('provider_error'));
            },
            draft: FINAL,
            status: 'idle'},
    ];

    test.each(rows)('stopping by $name', async (row) => {
        const {transports, controls, unmount, updateText, draft} = renderButton({message: 'Hello world', channelId: 'c1', rootId: ''}, {caret: 5, maxSessionMs: row.maxSessionMs});
        await startListening(() => transports[0]);
        const transport = transports[0];
        const ctx = {transport, controls, unmount};

        act(() => transport.transcript({text: 'big braid'}));
        await waitFor(() => expect(draft()).toBe(PARTIAL));

        let finishStop: () => void = () => { /* set when stop is called */ };
        transport.stop.mockImplementation(() => new Promise<void>((resolve) => {
            finishStop = () => {
                if (row.stopDeliversFinal !== false) {
                    transport.transcript({text: 'big brave'});
                }
                resolve();
            };
        }));

        await act(async () => {
            await row.trigger(ctx);
        });
        await waitFor(() => expect(transport.stop).toHaveBeenCalledTimes(1));

        if (!row.unmounts || row.duringStop) {
            expect(button().getAttribute('data-status')).toBe('stopping');
            expect((button() as HTMLButtonElement).disabled).toBe(true);
            fireEvent.click(button());
            expect(transports).toHaveLength(1);
        }

        act(() => {
            row.duringStop?.(ctx);
        });
        const writesBeforeStop = updateText.mock.calls.length;
        await act(async () => finishStop());

        if (row.unmounts) {
            expect(updateText).toHaveBeenCalledTimes(writesBeforeStop);
            return;
        }
        await waitFor(() => expect(button().getAttribute('data-status')).toBe(row.status));
        expect(draft()).toBe(row.draft);
        expect(transport.stop).toHaveBeenCalledTimes(1);
    });

    test.each([
        {reason: 'mic_blocked', message: 'Microphone access is blocked. Allow it in your browser settings and try again.'},
        {reason: 'rate_limited', message: 'Dictation was started too often. Wait a minute and try again.'},
        {reason: 'provider_unreachable', message: 'The speech service could not be reached.'},
    ] as const)('a start failure ($reason) shows an error state', async ({reason, message}) => {
        const {transports} = renderButton({message: '', channelId: 'c1'});
        fireEvent.click(button());
        await act(async () => transports[0].rejectStart(new DictationError(reason)));

        expect(button().getAttribute('data-status')).toBe('error');
        expect(screen.getByTestId('with-tooltip').getAttribute('data-title')).toBe(message);
        expect(screen.getByRole('status').textContent).toBe(message);

        fireEvent.click(button());
        expect(transports).toHaveLength(2);
    });
});
