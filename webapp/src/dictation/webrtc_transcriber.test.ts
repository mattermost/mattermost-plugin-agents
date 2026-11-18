// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {DictationError, DictationSession} from './types';
import {TranscriberDeps, WebRTCTranscriber} from './webrtc_transcriber';

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
    close = jest.fn();

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
    clientSecret: 'ek_SENTINEL',
    expiresAt: Date.now() + 60000,
    callsURL: 'https://api.openai.test/v1/realtime/calls',
    provider: 'openai',
    model: 'gpt-live-transcribe',
    turnDetection: 'none',
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

    const deps: TranscriberDeps = {
        getUserMedia: jest.fn(async () => stream),
        createSession: jest.fn(async () => session()),
        createPeerConnection: () => pc as unknown as RTCPeerConnection,
        fetch: jest.fn(async () => ({ok: true, text: async () => 'ANSWER_SDP'})) as unknown as typeof fetch,
        createLevelMeter: () => ({level: () => level, close: meterClose}),
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

    return {deps, track, pc, advance, meterClose};
}

const commits = (pc: FakePeerConnection) => pc.channel.sent.filter((e) => e.type === 'input_audio_buffer.commit').length;

describe('WebRTCTranscriber', () => {
    test('requests the mic before minting, then posts the SDP offer with the ephemeral key', async () => {
        const {deps, pc} = setup();
        const order: string[] = [];
        (deps.getUserMedia as jest.Mock).mockImplementation(async () => {
            order.push('mic');
            return {getAudioTracks: () => [new FakeTrack()], getTracks: () => []};
        });
        (deps.createSession as jest.Mock).mockImplementation(async () => {
            order.push('mint');
            return session();
        });

        await new WebRTCTranscriber(deps).start();

        expect(order).toEqual(['mic', 'mint']);
        expect(deps.getUserMedia).toHaveBeenCalledWith({audio: {echoCancellation: true, noiseSuppression: true}});
        expect(deps.fetch).toHaveBeenCalledWith('https://api.openai.test/v1/realtime/calls', expect.objectContaining({
            method: 'POST',
            body: 'OFFER_SDP',
            headers: {Authorization: 'Bearer ek_SENTINEL', 'Content-Type': 'application/sdp'},
        }));
        expect(pc.remoteDescription).toEqual({type: 'answer', sdp: 'ANSWER_SDP'});
    });

    test('a blocked microphone fails without minting a session', async () => {
        const {deps} = setup({getUserMedia: jest.fn(async () => {
            throw Object.assign(new Error('denied'), {name: 'NotAllowedError'});
        })});

        await expect(new WebRTCTranscriber(deps).start()).rejects.toMatchObject({reason: 'mic_blocked'});
        expect(deps.createSession).not.toHaveBeenCalled();
    });

    test.each([
        {status: 403, reason: 'forbidden'},
        {status: 404, reason: 'unavailable'},
        {status: 429, reason: 'rate_limited'},
        {status: 502, reason: 'provider_unreachable'},
        {status: 503, reason: 'unavailable'},
    ])('a $status from the mint route fails with $reason and releases the mic', async ({status, reason}) => {
        const {deps, track} = setup({createSession: jest.fn(async () => {
            throw Object.assign(new Error(''), {status_code: status});
        })});

        await expect(new WebRTCTranscriber(deps).start()).rejects.toMatchObject({reason});
        expect(track.stop).toHaveBeenCalled();
    });

    test('a rejected SDP offer fails and releases the mic and connection', async () => {
        const {deps, track, pc} = setup({fetch: jest.fn(async () => ({ok: false, text: async () => 'upstream body'})) as unknown as typeof fetch});

        await expect(new WebRTCTranscriber(deps).start()).rejects.toMatchObject({reason: 'provider_unreachable'});
        expect(track.stop).toHaveBeenCalled();
        expect(pc.close).toHaveBeenCalled();
    });

    test('emits partial and final transcripts in speaking order', async () => {
        const {deps, pc} = setup();
        const transcriber = new WebRTCTranscriber(deps);
        const partial = jest.fn();
        const final = jest.fn();
        transcriber.onPartial(partial);
        transcriber.onFinal(final);
        await transcriber.start();

        pc.channel.receive({type: 'conversation.item.input_audio_transcription.delta', item_id: 'a', delta: 'Hello'});
        expect(partial).toHaveBeenLastCalledWith({text: 'Hello', finalText: ''});

        pc.channel.receive({type: 'input_audio_buffer.committed', item_id: 'a', previous_item_id: null});
        pc.channel.receive({type: 'input_audio_buffer.committed', item_id: 'b', previous_item_id: 'a'});
        pc.channel.receive({type: 'conversation.item.input_audio_transcription.completed', item_id: 'b', transcript: 'world.'});
        pc.channel.receive({type: 'conversation.item.input_audio_transcription.completed', item_id: 'a', transcript: 'Hello'});
        expect(final).toHaveBeenLastCalledWith({text: 'Hello world.', finalText: 'Hello world.'});
    });

    test('commits segments on silence and on stop when the provider does no turn detection', async () => {
        const {deps, pc, advance} = setup();
        const transcriber = new WebRTCTranscriber(deps);
        await transcriber.start();

        advance(0.3, 1000);
        advance(0, 800);
        expect(commits(pc)).toBe(1);

        advance(0.3, 500);
        await transcriber.stop();
        expect(commits(pc)).toBe(2);
    });

    test('never commits when the provider detects turns', async () => {
        const {deps, pc, advance} = setup({createSession: jest.fn(async () => session({model: 'gpt-4o-transcribe', turnDetection: 'server_vad'}))});
        const transcriber = new WebRTCTranscriber(deps);
        await transcriber.start();

        advance(0.3, 1000);
        advance(0, 800);
        advance(0.3, 500);
        await transcriber.stop();
        expect(commits(pc)).toBe(0);
    });

    test('stop waits for the last segment, then closes the connection and stops every mic track', async () => {
        const {deps, pc, track, meterClose} = setup();
        const transcriber = new WebRTCTranscriber(deps);
        const final = jest.fn();
        transcriber.onFinal(final);
        await transcriber.start();
        pc.channel.receive({type: 'conversation.item.input_audio_transcription.delta', item_id: 'a', delta: 'last'});

        const stopped = transcriber.stop();
        await Promise.resolve();
        expect(pc.close).not.toHaveBeenCalled();

        pc.channel.receive({type: 'conversation.item.input_audio_transcription.completed', item_id: 'a', transcript: 'last words'});
        await stopped;

        expect(final).toHaveBeenLastCalledWith({text: 'last words', finalText: 'last words'});
        expect(pc.channel.close).toHaveBeenCalled();
        expect(pc.close).toHaveBeenCalled();
        expect(track.stop).toHaveBeenCalled();
        expect(meterClose).toHaveBeenCalled();
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

    test('stop during connecting releases the mic and makes start fail', async () => {
        let resolveMint: (s: DictationSession) => void = () => { /* set below */ };
        const {deps, track} = setup({createSession: jest.fn(() => new Promise<DictationSession>((resolve) => {
            resolveMint = resolve;
        }))});
        const transcriber = new WebRTCTranscriber(deps);
        const starting = transcriber.start();
        await Promise.resolve();
        await Promise.resolve();

        await transcriber.stop();
        resolveMint(session());

        await expect(starting).rejects.toBeInstanceOf(DictationError);
        expect(track.stop).toHaveBeenCalled();
        expect(deps.fetch).not.toHaveBeenCalled();
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

        expect(onError).toHaveBeenCalledWith(expect.objectContaining({reason}));
        expect(pc.close).toHaveBeenCalled();
        expect(track.stop).toHaveBeenCalled();
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
