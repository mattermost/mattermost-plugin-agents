// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';

import {DictationError, DictationTransport, TranscriptUpdate} from '@/dictation/types';

import DictationButton from './dictation_button';
import {EditorDraft, MAX_SESSION_MS} from './use_dictation';

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
    partial: (u: TranscriptUpdate) => void = () => { /* set by hook */ };
    final: (u: TranscriptUpdate) => void = () => { /* set by hook */ };
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

    onPartial(h: (u: TranscriptUpdate) => void) {
        this.partial = h;
    }

    onFinal(h: (u: TranscriptUpdate) => void) {
        this.final = h;
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
function renderButton(initial: EditorDraft, caret?: number) {
    const transports: FakeTransport[] = [];
    const controls: HarnessControls = {setDraft: () => { /* set on render */ }};
    const updateText = jest.fn();

    const Harness = () => {
        const [draft, setDraft] = useState(initial);
        controls.setDraft = setDraft;
        return (
            <>
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
                />
                <div data-testid='draft'>{draft.message}</div>
            </>
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

    test('types partials at the caret and replaces them with final text on stop', async () => {
        const {transports, draft} = renderButton({message: 'Hello world', channelId: 'c1'}, 5);
        await startListening(() => transports[0]);

        act(() => transports[0].partial({text: 'big', finalText: ''}));
        await waitFor(() => expect(draft()).toBe('Hello big world'));

        act(() => transports[0].final({text: 'big brave', finalText: 'big'}));
        await waitFor(() => expect(draft()).toBe('Hello big brave world'));

        fireEvent.click(button());
        await waitFor(() => expect(draft()).toBe('Hello big world'));
    });

    test('coalesces updates into one write per frame', async () => {
        const {transports, updateText, draft} = renderButton({message: '', channelId: 'c1'});
        await startListening(() => transports[0]);

        act(() => {
            transports[0].partial({text: 'a', finalText: ''});
            transports[0].partial({text: 'a b', finalText: ''});
            transports[0].partial({text: 'a b c', finalText: ''});
        });
        await waitFor(() => expect(draft()).toBe('a b c'));
        expect(updateText).toHaveBeenCalledTimes(1);
    });

    test('a user edit stops dictation and keeps the edit', async () => {
        const {transports, controls, draft} = renderButton({message: '', channelId: 'c1'});
        await startListening(() => transports[0]);
        act(() => transports[0].partial({text: 'hello', finalText: ''}));
        await waitFor(() => expect(draft()).toBe('hello'));

        act(() => controls.setDraft((d) => ({...d, message: 'hello!'})));
        await waitFor(() => expect(button().getAttribute('data-status')).toBe('idle'));
        expect(transports[0].stop).toHaveBeenCalled();

        act(() => transports[0].partial({text: 'hello there', finalText: ''}));
        await new Promise((r) => setTimeout(r, 50));
        expect(draft()).toBe('hello!');
    });

    test.each([
        {name: 'the message is sent', change: (d: EditorDraft) => ({...d, message: ''})},
        {name: 'the channel changes', change: (d: EditorDraft) => ({...d, channelId: 'c2'})},
        {name: 'the thread changes', change: (d: EditorDraft) => ({...d, rootId: 'r2'})},
    ])('stops without writing again when $name', async ({change}) => {
        const {transports, controls, updateText} = renderButton({message: 'draft', channelId: 'c1', rootId: ''});
        await startListening(() => transports[0]);

        act(() => controls.setDraft(change));
        await waitFor(() => expect(transports[0].stop).toHaveBeenCalled());
        await waitFor(() => expect(button().getAttribute('data-status')).toBe('idle'));
        expect(updateText).not.toHaveBeenCalled();
    });

    test('Esc stops dictation', async () => {
        const {transports} = renderButton({message: '', channelId: 'c1'});
        await startListening(() => transports[0]);

        fireEvent.keyDown(document, {key: 'Escape'});
        await waitFor(() => expect(transports[0].stop).toHaveBeenCalled());
    });

    test('unmounting stops dictation', async () => {
        const {transports, unmount} = renderButton({message: '', channelId: 'c1'});
        await startListening(() => transports[0]);
        expect(transports[0].stop).not.toHaveBeenCalled();

        unmount();
        expect(transports[0].stop).toHaveBeenCalled();
    });

    test('the transport reporting silence stops dictation', async () => {
        const {transports} = renderButton({message: '', channelId: 'c1'});
        await startListening(() => transports[0]);

        await act(async () => {
            transports[0].idle();
        });
        await waitFor(() => expect(button().getAttribute('data-status')).toBe('idle'));
        expect(transports[0].stop).toHaveBeenCalled();
    });

    test('stops at the session length cap', async () => {
        jest.useFakeTimers();
        try {
            const {transports} = renderButton({message: '', channelId: 'c1'});
            await startListening(() => transports[0]);

            await act(async () => {
                jest.advanceTimersByTime(MAX_SESSION_MS);
            });
            expect(transports[0].stop).toHaveBeenCalled();
        } finally {
            jest.useRealTimers();
        }
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

    test('a provider error while listening keeps finalized text and shows the error', async () => {
        const {transports, draft} = renderButton({message: '', channelId: 'c1'});
        await startListening(() => transports[0]);
        act(() => transports[0].final({text: 'kept partial', finalText: 'kept'}));
        await waitFor(() => expect(draft()).toBe('kept partial'));

        await act(async () => {
            transports[0].error(new DictationError('provider_error'));
        });
        await waitFor(() => expect(button().getAttribute('data-status')).toBe('error'));
        expect(draft()).toBe('kept');
    });
});
