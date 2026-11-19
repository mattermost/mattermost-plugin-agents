// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {waitFor} from '@testing-library/react';

import {DictationError, DictationSession} from './types';

import {notifyDictationSessionEnded} from './session_events';
import {TranscriberDeps, WebRTCTranscriber, createAnalyserLevelMeter} from './webrtc_transcriber';

jest.mock('@/client', () => ({createDictationSession: jest.fn()}));

class FakeTarget {
    private listeners = new Map<string, Array<(e: any) => void>>();

    addEventListener(type: string, fn: (e: any) => void) {
        this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn]);
    }

    dispatch(type: string, event: any = {}) {
        (this.listeners.get(type) ?? []).forEach((fn) => fn(event));
    }
}

class FakeTrack extends FakeTarget {
    stop = jest.fn();
}

class FakeChannel extends FakeTarget {
    readyState = 'connecting';
    sent: any[] = [];
    close = jest.fn(() => {
        this.readyState = 'closed';
    });

    send(data: string) {
        this.sent.push(JSON.parse(data));
    }

    open() {
        this.readyState = 'open';
        this.dispatch('open');
    }

    receive(event: Record<string, unknown>) {
        this.dispatch('message', {data: JSON.stringify(event)});
    }
}

class FakePeerConnection extends FakeTarget {
    connectionState = 'new';
    channel = new FakeChannel();
    close = jest.fn();
    addTrack = jest.fn();
    remoteDescription: unknown = null;

    createDataChannel(label: string) {
        expect(label).toBe('oai-events');
        return this.channel;
    }

    async createOffer() {
        return {type: 'offer', sdp: 'OFFER_SDP'};
    }

    async setLocalDescription() { /* accepted */ }

    async setRemoteDescription(desc: unknown) {
        this.remoteDescription = desc;

        // The provider opens the data channel once negotiation completes.
        setTimeout(() => this.channel.open(), 0);
    }
}

const session = (overrides: Partial<DictationSession> = {}): DictationSession => ({
    sdp: 'ANSWER_SDP',
    sessionId: 'session-1',
    provider: 'openai',
    model: 'gpt-live-transcribe',
    turnDetection: 'none',
    maxDurationSeconds: 600,
    ...overrides,
});

function setup(overrides: Partial<TranscriberDeps> = {}) {
    const track = new FakeTrack();
    const stream = {
        getAudioTracks: () => [track],
        getTracks: () => [track],
    } as unknown as MediaStream;
    const pc = new FakePeerConnection();
    let level = 0;
    let now = 0;
    let tick: (() => void) | null = null;
    const meterClose = jest.fn();
    const meterAttach = jest.fn(async () => true);

    const deps: TranscriberDeps = {
        getUserMedia: jest.fn(async () => stream),
        createSession: jest.fn(async () => session()),
        endSession: jest.fn(async () => { /* ended */ }),
        createPeerConnection: () => pc as unknown as RTCPeerConnection,
        createLevelMeter: jest.fn(() => ({attach: meterAttach, level: () => level, close: meterClose})),
        now: () => now,
        setInterval: ((fn: () => void) => {
            tick = fn;
            return 1;
        }) as unknown as TranscriberDeps['setInterval'],
        clearInterval: jest.fn(),
        ...overrides,
    };

    // advance samples the microphone every 50 ms at the given level.
    const advance = (toLevel: number, ms: number) => {
        level = toLevel;
        for (let t = 0; t < ms; t += 50) {
            now += 50;
            tick?.();
        }
    };

    return {deps, track, stream, pc, advance, meterClose, meterAttach};
}

const commits = (pc: FakePeerConnection) => pc.channel.sent.filter((e) => e.type === 'input_audio_buffer.commit').length;

describe('WebRTCTranscriber', () => {
    test('requests the mic first, then sends the offer through the plugin and applies its answer', async () => {
        const {deps, pc} = setup();
        const order: string[] = [];
        (deps.getUserMedia as jest.Mock).mockImplementation(async () => {
            order.push('mic');
            return {getAudioTracks: () => [new FakeTrack()], getTracks: () => []};
        });
        (deps.createSession as jest.Mock).mockImplementation(async () => {
            order.push('session');
            return session();
        });

        await new WebRTCTranscriber(deps).start();

        expect(order).toEqual(['mic', 'session']);
        expect(deps.getUserMedia).toHaveBeenCalledWith({audio: {echoCancellation: true, noiseSuppression: true}});
        expect(deps.createSession).toHaveBeenCalledWith('OFFER_SDP', expect.any(AbortSignal));
        expect(pc.remoteDescription).toEqual({type: 'answer', sdp: 'ANSWER_SDP'});
    });

    test('never sends session.update, only commits', async () => {
        const {deps, pc, advance} = setup();
        const transcriber = new WebRTCTranscriber(deps);
        await transcriber.start();
        advance(0.3, 1000);
        advance(0, 2100);
        advance(0.3, 500);
        await transcriber.stop();

        expect(pc.channel.sent.length).toBeGreaterThan(0);
        expect(pc.channel.sent.every((e) => e.type === 'input_audio_buffer.commit')).toBe(true);
    });

    test('a blocked microphone fails without minting a session', async () => {
        const {deps} = setup({getUserMedia: jest.fn(async () => {
            throw Object.assign(new Error('denied'), {name: 'NotAllowedError'});
        })});

        await expect(new WebRTCTranscriber(deps).start()).rejects.toMatchObject({reason: 'mic_blocked'});
        expect(deps.createSession).not.toHaveBeenCalled();
    });

    test.each([
        {status: 0, reason: 'provider_unreachable'},
        {status: 400, reason: 'unavailable'},
        {status: 403, reason: 'forbidden'},
        {status: 404, reason: 'unavailable'},
        {status: 413, reason: 'offer_too_large'},
        {status: 429, reason: 'rate_limited'},
        {status: 502, reason: 'provider_unreachable'},
        {status: 503, reason: 'unavailable'},
    ])('a $status from the session route fails with $reason and releases the mic and connection', async ({status, reason}) => {
        const {deps, track} = setup({createSession: jest.fn(async () => {
            throw Object.assign(new Error(''), {status_code: status});
        })});

        await expect(new WebRTCTranscriber(deps).start()).rejects.toMatchObject({reason});
        expect(track.stop).toHaveBeenCalled();
    });

    test.each([
        {name: 'no answer', answer: {sdp: ''}},
        {name: 'a non-string answer', answer: {sdp: 42}},
        {name: 'no session id', answer: {sessionId: ''}},
    ])('a session response with $name fails and releases the mic and connection', async ({answer}) => {
        const {deps, track, pc} = setup({createSession: jest.fn(async () => ({...session(), ...answer}) as unknown as DictationSession)});

        await expect(new WebRTCTranscriber(deps).start()).rejects.toMatchObject({reason: 'provider_unreachable'});
        expect(track.stop).toHaveBeenCalled();
        expect(pc.close).toHaveBeenCalled();
    });

    test.each([
        {reason: 'duration_limit', want: 'session_time_limit'},
        {reason: 'policy', want: 'session_policy'},
        {reason: 'shutdown', want: 'session_shutdown'},
    ])('the server ending the session ($reason) reports $want and tears down', async ({reason, want}) => {
        const {deps, pc, track} = setup();
        const transcriber = new WebRTCTranscriber(deps);
        const onError = jest.fn();
        transcriber.onError(onError);
        await transcriber.start();

        // The server hangs up, which closes the data channel, and tells the user why.
        pc.channel.dispatch('close');
        notifyDictationSessionEnded('session-1', reason);

        expect(onError).toHaveBeenCalledTimes(1);
        expect(onError).toHaveBeenCalledWith(expect.objectContaining({reason: want}));
        expect(pc.close).toHaveBeenCalled();
        expect(track.stop).toHaveBeenCalled();
        expect(deps.endSession).not.toHaveBeenCalled();
    });

    test.each([
        {name: 'another session', sessionId: 'session-2', reason: 'policy'},
        {name: 'an unknown reason', sessionId: 'session-1', reason: 'whatever'},
        {name: 'a non-string session id', sessionId: 1, reason: 'policy'},
    ])('ignores a session end for $name', async ({sessionId, reason}) => {
        const {deps, pc} = setup();
        const transcriber = new WebRTCTranscriber(deps);
        const onError = jest.fn();
        transcriber.onError(onError);
        await transcriber.start();

        notifyDictationSessionEnded(sessionId, reason);
        expect(onError).not.toHaveBeenCalled();
        expect(pc.close).not.toHaveBeenCalled();
    });

    test('a session end after stop is ignored', async () => {
        const {deps} = setup();
        const transcriber = new WebRTCTranscriber(deps);
        const onError = jest.fn();
        transcriber.onError(onError);
        await transcriber.start();
        await transcriber.stop();

        notifyDictationSessionEnded('session-1', 'policy');
        expect(onError).not.toHaveBeenCalled();
    });

    test('emits partial and final transcripts in speaking order', async () => {
        const {deps, pc} = setup();
        const transcriber = new WebRTCTranscriber(deps);
        const onTranscript = jest.fn();
        transcriber.onTranscript(onTranscript);
        await transcriber.start();

        pc.channel.receive({type: 'conversation.item.input_audio_transcription.delta', item_id: 'a', delta: 'Hello'});
        expect(onTranscript).toHaveBeenLastCalledWith({text: 'Hello'});

        pc.channel.receive({type: 'input_audio_buffer.committed', item_id: 'a', previous_item_id: null});
        pc.channel.receive({type: 'input_audio_buffer.committed', item_id: 'b', previous_item_id: 'a'});
        pc.channel.receive({type: 'conversation.item.input_audio_transcription.completed', item_id: 'b', transcript: 'world.'});
        pc.channel.receive({type: 'conversation.item.input_audio_transcription.completed', item_id: 'a', transcript: 'Hello'});
        expect(onTranscript).toHaveBeenLastCalledWith({text: 'Hello world.'});
    });

    test('commits segments on silence and on stop when the provider does no turn detection', async () => {
        const {deps, pc, advance} = setup();
        const transcriber = new WebRTCTranscriber(deps);
        await transcriber.start();

        advance(0.3, 1000);
        advance(0, 2100);
        expect(commits(pc)).toBe(1);

        advance(0.3, 500);
        await transcriber.stop();
        expect(commits(pc)).toBe(2);
    });

    test('leaves commits to the provider that detects turns, except speech still in the buffer at stop', async () => {
        const {deps, pc, advance} = setup({createSession: jest.fn(async () => session({model: 'gpt-4o-transcribe', turnDetection: 'server_vad'}))});
        const transcriber = new WebRTCTranscriber(deps);
        const onTranscript = jest.fn();
        transcriber.onTranscript(onTranscript);
        await transcriber.start();

        advance(0.3, 1000);
        advance(0, 2100);
        advance(0.3, 500);
        expect(commits(pc)).toBe(0);

        const stopped = transcriber.stop();
        expect(commits(pc)).toBe(1);
        await Promise.resolve();
        expect(pc.close).not.toHaveBeenCalled();

        pc.channel.receive({type: 'input_audio_buffer.committed', item_id: 'last', previous_item_id: null});
        pc.channel.receive({type: 'conversation.item.input_audio_transcription.completed', item_id: 'last', transcript: 'still talking'});
        await stopped;
        expect(onTranscript).toHaveBeenLastCalledWith({text: 'still talking'});
        expect(pc.close).toHaveBeenCalled();
    });

    test('stop waits for a commit the provider has not acknowledged yet', async () => {
        const {deps, pc, advance} = setup();
        const transcriber = new WebRTCTranscriber(deps);
        const onTranscript = jest.fn();
        transcriber.onTranscript(onTranscript);
        await transcriber.start();

        // A silence commit is sent; the user stops before any event for it arrives.
        advance(0.3, 1000);
        advance(0, 2100);
        expect(commits(pc)).toBe(1);

        const stopped = transcriber.stop();
        await Promise.resolve();
        expect(pc.close).not.toHaveBeenCalled();

        pc.channel.receive({type: 'input_audio_buffer.committed', item_id: 'a', previous_item_id: null});
        await Promise.resolve();
        expect(pc.close).not.toHaveBeenCalled();

        pc.channel.receive({type: 'conversation.item.input_audio_transcription.completed', item_id: 'a', transcript: 'before the pause'});
        await stopped;
        expect(onTranscript).toHaveBeenLastCalledWith({text: 'before the pause'});
        expect(pc.close).toHaveBeenCalled();
    });

    test('an empty-buffer error acknowledges the commit sent at stop', async () => {
        const {deps, pc, advance} = setup();
        const transcriber = new WebRTCTranscriber(deps);
        await transcriber.start();
        advance(0.3, 500);

        const stopped = transcriber.stop();
        expect(commits(pc)).toBe(1);
        pc.channel.receive({type: 'error', error: {type: 'invalid_request_error', code: 'input_audio_buffer_commit_empty'}});
        await stopped;
        expect(pc.close).toHaveBeenCalled();
    });

    test('creates the level meter before any await, then attaches the mic to it', async () => {
        let resolveMic: (s: MediaStream) => void = () => { /* set below */ };
        const {deps, stream, meterAttach} = setup();
        (deps.getUserMedia as jest.Mock).mockImplementation(() => new Promise<MediaStream>((resolve) => {
            resolveMic = resolve;
        }));
        const starting = new WebRTCTranscriber(deps).start();
        expect(deps.createLevelMeter).toHaveBeenCalledTimes(1);

        resolveMic(stream);
        await starting;
        expect(meterAttach).toHaveBeenCalledWith(stream);
    });

    test.each([
        {turnDetection: 'none' as const, fails: true},
        {turnDetection: 'server_vad' as const, fails: false},
    ])('a suspended audio context with turn detection $turnDetection fails start: $fails', async ({turnDetection, fails}) => {
        const {deps, meterAttach, track} = setup({createSession: jest.fn(async () => session({turnDetection}))});
        meterAttach.mockResolvedValue(false);
        const starting = new WebRTCTranscriber(deps).start();
        if (fails) {
            await expect(starting).rejects.toMatchObject({reason: 'audio_blocked'});
            expect(track.stop).toHaveBeenCalled();
        } else {
            await expect(starting).resolves.toBeUndefined();
        }
    });

    test('the browser level meter resumes its audio context inside the click, before any await', async () => {
        const calls: string[] = [];
        const analyser = {fftSize: 0, getFloatTimeDomainData: jest.fn(), connect: jest.fn()};
        const mute = {gain: {value: 1}, connect: jest.fn()};
        class FakeAudioContext {
            destination = {name: 'destination'};
            state = 'suspended';
            resume = jest.fn(async () => {
                calls.push('resume');
                this.state = 'running';
            });
            close = jest.fn(async () => { /* closed */ });
            createAnalyser() {
                return analyser;
            }
            createGain() {
                return mute;
            }
            createMediaStreamSource() {
                return {connect: jest.fn()};
            }
        }
        const original = window.AudioContext;
        (window as any).AudioContext = FakeAudioContext;
        try {
            let resolveMic: (s: MediaStream) => void = () => { /* set below */ };
            const {deps, stream} = setup({createLevelMeter: createAnalyserLevelMeter});
            (deps.getUserMedia as jest.Mock).mockImplementation(() => {
                calls.push('getUserMedia');
                return new Promise<MediaStream>((resolve) => {
                    resolveMic = resolve;
                });
            });

            const starting = new WebRTCTranscriber(deps).start();
            expect(calls).toEqual(['resume', 'getUserMedia']);

            resolveMic(stream);
            await expect(starting).resolves.toBeUndefined();

            // WebKit leaves an analyser that doesn't reach the destination silent.
            expect(analyser.connect).toHaveBeenCalledWith(mute);
            expect(mute.connect).toHaveBeenCalledWith(new FakeAudioContext().destination);
            expect(mute.gain.value).toBe(0);
        } finally {
            (window as any).AudioContext = original;
        }
    });

    test('a data channel the provider closes reports a lost connection', async () => {
        const {deps, pc, track} = setup();
        const transcriber = new WebRTCTranscriber(deps);
        const onError = jest.fn();
        transcriber.onError(onError);
        await transcriber.start();

        // Without a reason from the server, the loss is reported after a short wait.
        pc.channel.dispatch('close');
        expect(onError).not.toHaveBeenCalled();

        await waitFor(() => expect(onError).toHaveBeenCalledWith(expect.objectContaining({reason: 'connection_lost'})), {timeout: 2000});
        expect(pc.close).toHaveBeenCalled();
        expect(track.stop).toHaveBeenCalled();
    });

    test('a data channel that closes before opening fails start without waiting for the timeout', async () => {
        const {deps, pc} = setup();
        pc.setRemoteDescription = jest.fn(async () => {
            setTimeout(() => pc.channel.dispatch('close'), 0);
        });

        await expect(new WebRTCTranscriber(deps).start()).rejects.toMatchObject({reason: 'provider_unreachable'});
        expect(pc.close).toHaveBeenCalled();
    });

    test('stop waits for the last segment, then closes the connection and stops every mic track', async () => {
        const {deps, pc, track, meterClose} = setup();
        const transcriber = new WebRTCTranscriber(deps);
        const final = jest.fn();
        transcriber.onTranscript(final);
        await transcriber.start();
        pc.channel.receive({type: 'conversation.item.input_audio_transcription.delta', item_id: 'a', delta: 'last'});

        const stopped = transcriber.stop();
        await Promise.resolve();
        expect(pc.close).not.toHaveBeenCalled();

        pc.channel.receive({type: 'conversation.item.input_audio_transcription.completed', item_id: 'a', transcript: 'last words'});
        await stopped;

        expect(final).toHaveBeenLastCalledWith({text: 'last words'});
        expect(pc.channel.close).toHaveBeenCalled();
        expect(pc.close).toHaveBeenCalled();
        expect(track.stop).toHaveBeenCalled();
        expect(meterClose).toHaveBeenCalled();
    });

    test.each([
        {name: 'a track that reports ended when stopped', before: (track: FakeTrack) => track.stop.mockImplementation(() => track.dispatch('ended'))},
        {name: 'a track that ends on its own while stopping', after: (track: FakeTrack) => track.dispatch('ended')},
    ])('$name still keeps a late final transcript', async ({before, after}) => {
        const {deps, pc, track} = setup();
        const transcriber = new WebRTCTranscriber(deps);
        const onTranscript = jest.fn();
        const onError = jest.fn();
        transcriber.onTranscript(onTranscript);
        transcriber.onError(onError);
        await transcriber.start();
        before?.(track);
        pc.channel.receive({type: 'conversation.item.input_audio_transcription.delta', item_id: 'a', delta: 'last'});

        const stopped = transcriber.stop();
        after?.(track);
        await new Promise((r) => setTimeout(r, 100));
        expect(pc.channel.close).not.toHaveBeenCalled();

        pc.channel.receive({type: 'conversation.item.input_audio_transcription.completed', item_id: 'a', transcript: 'last words'});
        await stopped;

        expect(onTranscript).toHaveBeenLastCalledWith({text: 'last words'});
        expect(onError).not.toHaveBeenCalled();
    });

    test.each([
        {name: 'the data channel closes', lose: (pc: FakePeerConnection) => pc.channel.dispatch('close')},
        {
            name: 'the peer connection fails',
            lose: (pc: FakePeerConnection) => {
                pc.connectionState = 'failed';
                pc.dispatch('connectionstatechange');
            },
        },
    ])('when $name while stopping, stop ends at once without an error', async ({lose}) => {
        const {deps, pc} = setup();
        const transcriber = new WebRTCTranscriber(deps);
        const onError = jest.fn();
        transcriber.onError(onError);
        await transcriber.start();
        pc.channel.receive({type: 'conversation.item.input_audio_transcription.delta', item_id: 'a', delta: 'last'});

        const stopped = transcriber.stop();
        lose(pc);
        await stopped;
        await new Promise((r) => setTimeout(r, 1200));

        expect(pc.close).toHaveBeenCalled();
        expect(onError).not.toHaveBeenCalled();
    });

    test('a provider error while stopping still delivers the last segment', async () => {
        const {deps, pc} = setup();
        const transcriber = new WebRTCTranscriber(deps);
        const onTranscript = jest.fn();
        const onError = jest.fn();
        transcriber.onTranscript(onTranscript);
        transcriber.onError(onError);
        await transcriber.start();
        pc.channel.receive({type: 'conversation.item.input_audio_transcription.delta', item_id: 'a', delta: 'last'});

        const stopped = transcriber.stop();
        pc.channel.receive({type: 'error', error: {type: 'invalid_request_error', code: 'input_audio_buffer_commit_empty'}});
        pc.channel.receive({type: 'error', error: {type: 'server_error', code: 'other'}});
        expect(pc.close).not.toHaveBeenCalled();

        pc.channel.receive({type: 'conversation.item.input_audio_transcription.completed', item_id: 'a', transcript: 'last words'});
        await stopped;

        expect(onTranscript).toHaveBeenLastCalledWith({text: 'last words'});
        expect(onError).not.toHaveBeenCalled();
        expect(pc.close).toHaveBeenCalled();
    });

    test('an empty-buffer commit error while listening is not fatal', async () => {
        const {deps, pc, track} = setup();
        const transcriber = new WebRTCTranscriber(deps);
        const onError = jest.fn();
        transcriber.onError(onError);
        await transcriber.start();

        pc.channel.receive({type: 'error', error: {type: 'invalid_request_error', code: 'input_audio_buffer_commit_empty'}});

        expect(onError).not.toHaveBeenCalled();
        expect(pc.close).not.toHaveBeenCalled();
        expect(track.stop).not.toHaveBeenCalled();
    });

    test('stop turns the microphone off before waiting for the last segment', async () => {
        const {deps, pc, track} = setup();
        const transcriber = new WebRTCTranscriber(deps);
        await transcriber.start();
        pc.channel.receive({type: 'conversation.item.input_audio_transcription.delta', item_id: 'a', delta: 'last'});

        const stopped = transcriber.stop();
        expect(track.stop).toHaveBeenCalled();
        expect(pc.close).not.toHaveBeenCalled();

        pc.channel.receive({type: 'conversation.item.input_audio_transcription.completed', item_id: 'a', transcript: 'last words'});
        await stopped;
        expect(pc.close).toHaveBeenCalled();
    });

    test('stop without waiting for the final closes at once and commits nothing', async () => {
        const {deps, pc, track, advance} = setup();
        const transcriber = new WebRTCTranscriber(deps);
        await transcriber.start();
        advance(0.3, 500);
        pc.channel.receive({type: 'conversation.item.input_audio_transcription.delta', item_id: 'a', delta: 'discarded'});

        await transcriber.stop({waitForFinal: false});
        expect(commits(pc)).toBe(0);
        expect(track.stop).toHaveBeenCalled();
        expect(pc.close).toHaveBeenCalled();
    });

    test.each([
        {name: 'a numeric delta', event: {type: 'conversation.item.input_audio_transcription.delta', item_id: 'a', delta: 42}},
        {name: 'an object item id', event: {type: 'conversation.item.input_audio_transcription.delta', item_id: {}, delta: 'x'}},
        {name: 'a numeric transcript', event: {type: 'conversation.item.input_audio_transcription.completed', item_id: 'a', transcript: 7}},
        {name: 'a numeric previous item id', event: {type: 'input_audio_buffer.committed', item_id: 'b', previous_item_id: 3}},
        {name: 'a null error', event: {type: 'error', error: null}},
        {name: 'null', event: null},
        {name: 'an array', event: ['x']},
        {name: 'a string', event: 'x'},
    ])('ignores malformed provider fields: $name', async ({event}) => {
        const {deps, pc} = setup();
        const transcriber = new WebRTCTranscriber(deps);
        const onTranscript = jest.fn();
        transcriber.onTranscript(onTranscript);
        transcriber.onError(jest.fn());
        await transcriber.start();
        pc.channel.receive({type: 'conversation.item.input_audio_transcription.delta', item_id: 'a', delta: 'ok'});

        expect(() => pc.channel.dispatch('message', {data: JSON.stringify(event)})).not.toThrow();
        pc.channel.receive({type: 'conversation.item.input_audio_transcription.delta', item_id: 'a', delta: ' still'});
        expect(onTranscript).toHaveBeenLastCalledWith({text: expect.stringMatching(/^ok/)});
    });

    test('stop gives up waiting for a final transcript after the grace period', async () => {
        jest.useFakeTimers();
        try {
            const {deps, pc, track} = setup();
            const transcriber = new WebRTCTranscriber(deps);
            const starting = transcriber.start();
            await jest.advanceTimersByTimeAsync(0);
            await starting;
            pc.channel.receive({type: 'conversation.item.input_audio_transcription.delta', item_id: 'a', delta: 'never finished'});

            const stopped = transcriber.stop();
            await jest.advanceTimersByTimeAsync(1500);
            await stopped;
            expect(pc.close).toHaveBeenCalled();
            expect(track.stop).toHaveBeenCalled();
        } finally {
            jest.useRealTimers();
        }
    });

    test('stop during connecting aborts the session request and releases the mic and connection', async () => {
        let signal: AbortSignal | null = null;
        const {deps, track, pc} = setup({createSession: jest.fn((_sdp: string, abortSignal: AbortSignal) => {
            signal = abortSignal;
            return new Promise<DictationSession>((_resolve, reject) => {
                abortSignal.addEventListener('abort', () => reject(new Error('aborted')));
            });
        })});
        const transcriber = new WebRTCTranscriber(deps);
        const starting = transcriber.start();
        await waitFor(() => expect(deps.createSession).toHaveBeenCalled());

        await transcriber.stop();

        expect(signal!.aborted).toBe(true);
        await expect(starting).rejects.toBeInstanceOf(DictationError);
        expect(track.stop).toHaveBeenCalled();
        expect(pc.close).toHaveBeenCalled();
        expect(pc.remoteDescription).toBeNull();
        expect(deps.endSession).not.toHaveBeenCalled();
    });

    test('a session that arrives after stop is ended and never connected', async () => {
        let resolveSession: (s: DictationSession) => void = () => { /* set below */ };
        const {deps, pc} = setup({createSession: jest.fn(() => new Promise<DictationSession>((resolve) => {
            resolveSession = resolve;
        }))});
        const transcriber = new WebRTCTranscriber(deps);
        const starting = transcriber.start();
        await waitFor(() => expect(deps.createSession).toHaveBeenCalled());

        await transcriber.stop();
        resolveSession(session());

        await expect(starting).rejects.toBeInstanceOf(DictationError);
        expect(deps.endSession).toHaveBeenCalledTimes(1);
        expect(deps.endSession).toHaveBeenCalledWith('session-1');
        expect(pc.remoteDescription).toBeNull();
    });

    test.each([
        {name: 'a normal stop', end: (t: WebRTCTranscriber) => t.stop()},
        {name: 'a stop that discards the final text', end: (t: WebRTCTranscriber) => t.stop({waitForFinal: false})},
        {
            name: 'a lost connection',
            end: async (_t: WebRTCTranscriber, pc: FakePeerConnection) => {
                pc.connectionState = 'failed';
                pc.dispatch('connectionstatechange');
            },
        },
    ])('$name ends the plugin session once', async ({end}) => {
        const {deps, pc} = setup();
        const transcriber = new WebRTCTranscriber(deps);
        await transcriber.start();
        jest.useFakeTimers();
        try {
            const ending = end(transcriber, pc);
            await jest.runAllTimersAsync();
            await ending;
            await transcriber.stop();

            expect(deps.endSession).toHaveBeenCalledTimes(1);
            expect(deps.endSession).toHaveBeenCalledWith('session-1');
        } finally {
            jest.useRealTimers();
        }
    });

    test('a failing end request does not surface an error', async () => {
        const {deps} = setup({endSession: jest.fn(async () => {
            throw new Error('offline');
        })});
        const transcriber = new WebRTCTranscriber(deps);
        const onError = jest.fn();
        transcriber.onError(onError);
        await transcriber.start();

        await expect(transcriber.stop()).resolves.toBeUndefined();
        expect(onError).not.toHaveBeenCalled();
    });

    test('a session response with an id but no answer is ended', async () => {
        const {deps} = setup({createSession: jest.fn(async () => ({...session(), sdp: ''}))});
        const transcriber = new WebRTCTranscriber(deps);

        await expect(transcriber.start()).rejects.toMatchObject({reason: 'provider_unreachable'});
        expect(deps.endSession).toHaveBeenCalledWith('session-1');
    });

    test.each([
        {name: 'a provider error event', trigger: (pc: FakePeerConnection) => pc.channel.receive({type: 'error', error: {message: 'bad'}}), reason: 'provider_error'},
        {
            name: 'a failed peer connection',
            trigger: (pc: FakePeerConnection) => {
                pc.connectionState = 'failed';
                pc.dispatch('connectionstatechange');
            },
            reason: 'connection_lost',
        },
    ])('$name reports an error and tears down', async ({trigger, reason}) => {
        const {deps, pc, track} = setup();
        const transcriber = new WebRTCTranscriber(deps);
        const onError = jest.fn();
        transcriber.onError(onError);
        await transcriber.start();

        trigger(pc);

        await waitFor(() => expect(onError).toHaveBeenCalledWith(expect.objectContaining({reason})), {timeout: 2000});
        expect(pc.close).toHaveBeenCalled();
        expect(track.stop).toHaveBeenCalled();
    });

    test('reports the microphone level on every sample', async () => {
        const {deps, advance} = setup();
        const transcriber = new WebRTCTranscriber(deps);
        const onLevel = jest.fn();
        transcriber.onLevel(onLevel);
        await transcriber.start();

        advance(0.25, 100);
        expect(onLevel).toHaveBeenCalledTimes(2);
        expect(onLevel).toHaveBeenLastCalledWith(0.25);
    });

    test('reports idle after 30 s of silence', async () => {
        const {deps, advance} = setup();
        const transcriber = new WebRTCTranscriber(deps);
        const onIdle = jest.fn();
        transcriber.onIdle(onIdle);
        await transcriber.start();

        advance(0, 30050);
        expect(onIdle).toHaveBeenCalledTimes(1);
    });
});
