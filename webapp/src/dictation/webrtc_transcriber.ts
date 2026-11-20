// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {createDictationSession, endDictationSession} from '@/client';

import {Segmenter, rmsLevel} from './segmenter';
import {onDictationSessionEnded} from './session_events';
import {Transcript} from './transcript';
import {
    DictationError,
    DictationSession,
    DictationTransport,
    TranscriptUpdate,
    errorReasonForSessionEnd,
    errorReasonForStatus,
} from './types';

const CHANNEL_OPEN_TIMEOUT_MS = 15000;

// When the server ends a session, the data channel closes about when its
// websocket event arrives; a lost connection waits this long for the reason.
const SESSION_END_GRACE_MS = 1000;
const FINAL_TRANSCRIPT_WAIT_MS = 1500;

// How recent speech must be for stop to expect the provider's own (server
// VAD) commit of it, which can still be in flight.
const PROVIDER_COMMIT_WINDOW_MS = 3000;
const LEVEL_SAMPLE_INTERVAL_MS = 50;

// LevelMeter reports the microphone's current RMS level (0 to 1). It is
// created before any await in start, inside the click that started
// dictation: browsers start an AudioContext made later suspended.
export type LevelMeter = {

    // Resolves whether audio processing runs; a suspended context reads silence.
    attach(stream: MediaStream): Promise<boolean>;
    level(): number;
    close(): void;
};

export type TranscriberDeps = {
    getUserMedia: (constraints: MediaStreamConstraints) => Promise<MediaStream>;
    createSession: (offerSDP: string, channelId: string, signal: AbortSignal) => Promise<DictationSession>;
    endSession: (sessionId: string) => Promise<void>;
    createPeerConnection: () => RTCPeerConnection;
    createLevelMeter: () => LevelMeter;
    now: () => number;
    setInterval: (fn: () => void, ms: number) => ReturnType<typeof setInterval>;
    clearInterval: (id: ReturnType<typeof setInterval>) => void;
};

export type TranscriberOptions = {
    channelId?: string;
};

export function createAnalyserLevelMeter(): LevelMeter {
    const AudioContextClass = window.AudioContext || (window as any).webkitAudioContext;
    const context: AudioContext = new AudioContextClass();

    // Resumed here, inside the click: Safari refuses a resume once the
    // gesture is gone.
    const resumed = context.resume().then(() => true, () => false);
    const analyser = context.createAnalyser();
    analyser.fftSize = 1024;
    const samples = new Float32Array(analyser.fftSize);

    // WebKit only processes nodes that reach the destination; a muted gain
    // keeps the analyser live without playing the microphone back.
    const mute = context.createGain();
    mute.gain.value = 0;
    analyser.connect(mute);
    mute.connect(context.destination);
    return {
        attach: async (stream) => {
            context.createMediaStreamSource(stream).connect(analyser);
            return await resumed && context.state === 'running';
        },
        level: () => {
            analyser.getFloatTimeDomainData(samples);
            return rmsLevel(samples);
        },
        close: () => {
            context.close().catch(() => { /* already closed */ });
        },
    };
}

export const browserTranscriberDeps = (): TranscriberDeps => ({
    getUserMedia: (constraints) => navigator.mediaDevices.getUserMedia(constraints),
    createSession: createDictationSession,
    endSession: endDictationSession,
    createPeerConnection: () => new RTCPeerConnection(),
    createLevelMeter: createAnalyserLevelMeter,
    now: () => Date.now(),
    setInterval: (fn, ms) => setInterval(fn, ms),
    clearInterval: (id) => clearInterval(id),
});

export function isDictationSupported(): boolean {
    return typeof window !== 'undefined' &&
        typeof window.RTCPeerConnection === 'function' &&
        typeof navigator !== 'undefined' &&
        typeof navigator.mediaDevices?.getUserMedia === 'function';
}

function micError(err: unknown): DictationError {
    const name = (err as {name?: string})?.name;
    if (name === 'NotFoundError' || name === 'OverconstrainedError') {
        return new DictationError('no_mic');
    }
    return new DictationError('mic_blocked');
}

// Provider realtime events read from the "oai-events" data channel.
type ProviderEvent = {
    type?: unknown;
    item_id?: unknown;
    previous_item_id?: unknown;
    delta?: unknown;
    transcript?: unknown;
    error?: {code?: unknown} | null;
};

// Sent when a commit holds under 100 ms of audio, e.g. a stop right after a
// forced commit. Nothing is lost.
const COMMIT_EMPTY_ERROR = 'input_audio_buffer_commit_empty';

// WebRTCTranscriber streams the microphone to OpenAI's realtime transcription
// over WebRTC. The plugin server exchanges the connection offer with OpenAI
// and keeps a control channel to the session, so the browser never holds a
// provider credential. The client only commits audio; it never sends
// session.update.
export class WebRTCTranscriber implements DictationTransport {
    private readonly deps: TranscriberDeps;
    private readonly transcript = new Transcript();
    private transcriptHandler: (update: TranscriptUpdate) => void = () => { /* unset */ };
    private errorHandler: (error: DictationError) => void = () => { /* unset */ };
    private idleHandler: () => void = () => { /* unset */ };
    private levelHandler: (level: number) => void = () => { /* unset */ };
    private connectingHandler: () => void = () => { /* unset */ };

    private stream: MediaStream | null = null;
    private pc: RTCPeerConnection | null = null;
    private channel: RTCDataChannel | null = null;
    private meter: LevelMeter | null = null;
    private sampler: ReturnType<typeof setInterval> | null = null;
    private segmenter: Segmenter | null = null;
    private clientCommits = false;
    private started = false;
    private stopped = false;
    private stopping: Promise<void> | null = null;
    private stopRequested = false;
    private finalWaiter: (() => void) | null = null;

    // Commits sent and not yet acknowledged. Their segments aren't in the
    // transcript yet, so stop waits for them too.
    private outstandingCommits = 0;

    // Set on a stop that expects the provider's own commit of recent speech;
    // cleared by the next commit, which can only cover audio sent before stop.
    private awaitingProviderCommit = false;

    private unsubscribeSessionEnded: (() => void) | null = null;
    private lostTimer: ReturnType<typeof setTimeout> | null = null;

    // Aborts a session request still in flight when dictation stops.
    private readonly connectAbort = new AbortController();

    // Ended on release so the slot frees before the provider sees the peer close.
    private sessionId: string | null = null;

    // The channel being dictated into, sent with the offer.
    private readonly channelId: string;

    constructor(deps: TranscriberDeps = browserTranscriberDeps(), {channelId = ''}: TranscriberOptions = {}) {
        this.deps = deps;
        this.channelId = channelId;
    }

    onConnecting(handler: () => void) {
        this.connectingHandler = handler;
    }

    onTranscript(handler: (update: TranscriptUpdate) => void) {
        this.transcriptHandler = handler;
    }

    onError(handler: (error: DictationError) => void) {
        this.errorHandler = handler;
    }

    onLevel(handler: (level: number) => void) {
        this.levelHandler = handler;
    }

    onIdle(handler: () => void) {
        this.idleHandler = handler;
    }

    async start(): Promise<void> {
        try {
            await this.connect();
        } catch (err) {
            this.release();
            if (err instanceof DictationError) {
                throw err;
            }
            throw new DictationError('provider_unreachable');
        }
    }

    private async connect() {
        this.meter = this.deps.createLevelMeter();

        // The microphone is requested before the session so a permission
        // prompt the user dismisses doesn't start one.
        try {
            this.stream = await this.deps.getUserMedia({audio: {echoCancellation: true, noiseSuppression: true}});
        } catch (err) {
            throw micError(err);
        }
        this.throwIfStopped();
        this.connectingHandler();
        const meterRunning = this.meter.attach(this.stream);
        meterRunning.catch(() => { /* awaited below */ });

        const pc = this.deps.createPeerConnection();
        this.pc = pc;
        for (const track of this.stream.getAudioTracks()) {
            pc.addTrack(track, this.stream);
            track.addEventListener('ended', () => {
                // The mic is turned off on stop, while the final transcript is still awaited.
                if (!this.stopRequested) {
                    this.fail(new DictationError('connection_lost'));
                }
            });
        }
        const channel = pc.createDataChannel('oai-events');
        this.channel = channel;
        channel.addEventListener('message', (e: MessageEvent) => this.handleEvent(e.data));
        channel.addEventListener('close', () => this.connectionLost());
        pc.addEventListener('connectionstatechange', () => {
            if (pc.connectionState === 'failed') {
                this.connectionLost();
            }
        });

        let openTimer: ReturnType<typeof setTimeout> | undefined;
        const opened = new Promise<void>((resolve, reject) => {
            openTimer = setTimeout(() => reject(new DictationError('provider_unreachable')), CHANNEL_OPEN_TIMEOUT_MS);
            channel.addEventListener('open', () => resolve());
            channel.addEventListener('close', () => reject(new DictationError('provider_unreachable')));
        });

        // A failure before the channel opens is thrown below; this keeps the
        // pending timeout from surfacing as an unhandled rejection.
        opened.catch(() => { /* handled by the await below */ });
        try {
            await this.negotiate(pc);
            this.throwIfStopped();
            await opened;
        } finally {
            clearTimeout(openTimer);
        }
        this.throwIfStopped();

        // Without levels no segment would ever be committed, and the idle
        // stop would end dictation while the user speaks, whatever the model.
        if (!await meterRunning) {
            throw new DictationError('audio_blocked');
        }
        this.throwIfStopped();
        this.startSampling();
        this.started = true;
    }

    // negotiate sends the offer through the plugin and applies the answer.
    private async negotiate(pc: RTCPeerConnection) {
        const offer = await pc.createOffer();
        await pc.setLocalDescription(offer);
        this.throwIfStopped();

        let session: DictationSession;
        try {
            session = await this.deps.createSession(offer.sdp ?? '', this.channelId, this.connectAbort.signal);
        } catch (err) {
            this.throwIfStopped();
            throw new DictationError(errorReasonForStatus((err as {status_code?: number})?.status_code));
        }
        if (typeof session?.sessionId === 'string' && session.sessionId) {
            this.sessionId = session.sessionId;

            // A stop that raced the response released before this id was known.
            if (this.stopped) {
                this.endSession();
            }
        }
        this.throwIfStopped();
        if (typeof session?.sdp !== 'string' || !session.sdp || !this.sessionId) {
            throw new DictationError('provider_unreachable');
        }
        this.clientCommits = session.turnDetection === 'none';
        this.unsubscribeSessionEnded = onDictationSessionEnded(this.sessionId, (reason) => {
            this.sessionId = null;
            this.fail(new DictationError(errorReasonForSessionEnd(reason)));
        });
        await pc.setRemoteDescription({type: 'answer', sdp: session.sdp});
    }

    // connectionLost reports a lost connection after a short wait, in case
    // the server ended the session and its reason is on the way.
    private connectionLost() {
        if (this.stopped || this.lostTimer !== null) {
            return;
        }

        // A stop under way has nothing left to wait for, and isn't an error.
        if (this.stopRequested) {
            this.finalWaiter?.();
            return;
        }
        if (!this.started) {
            this.fail(new DictationError('connection_lost'));
            return;
        }
        this.lostTimer = setTimeout(() => this.fail(new DictationError('connection_lost')), SESSION_END_GRACE_MS);
    }

    private throwIfStopped() {
        if (this.stopped) {
            throw new DictationError('unavailable', 'stopped');
        }
    }

    private startSampling() {
        const meter = this.meter;
        if (!meter) {
            return;
        }
        this.segmenter = new Segmenter({
            onCommit: () => {
                if (this.clientCommits) {
                    this.commit();
                }
            },
            onIdle: () => this.idleHandler(),
        }, this.deps.now());
        this.sampler = this.deps.setInterval(() => {
            const level = meter.level();
            this.segmenter?.sample(level, this.deps.now());
            this.levelHandler(level);
        }, LEVEL_SAMPLE_INTERVAL_MS);
    }

    private commit() {
        if (this.channel?.readyState === 'open') {
            this.channel.send(JSON.stringify({type: 'input_audio_buffer.commit'}));
            this.outstandingCommits++;
        }
    }

    private acknowledgeCommit() {
        this.outstandingCommits = Math.max(0, this.outstandingCommits - 1);
        this.settleIfDone();
    }

    private handleEvent(data: unknown) {
        if (typeof data !== 'string') {
            return;
        }
        let event: ProviderEvent;
        try {
            event = JSON.parse(data);
        } catch {
            return;
        }
        if (event === null || typeof event !== 'object') {
            return;
        }

        // Provider events are untrusted input: anything not a string is ignored.
        const itemId = typeof event.item_id === 'string' && event.item_id ? event.item_id : null;
        const text = (value: unknown) => (typeof value === 'string' ? value : '');
        switch (event.type) {
        case 'input_audio_buffer.committed':
            this.awaitingProviderCommit = false;
            if (itemId) {
                this.transcript.committed(itemId, typeof event.previous_item_id === 'string' ? event.previous_item_id : null);
            }
            this.acknowledgeCommit();
            return;
        case 'conversation.item.input_audio_transcription.delta':
            if (itemId && text(event.delta)) {
                this.transcript.delta(itemId, text(event.delta));
                this.transcriptHandler(this.transcript.snapshot());
            }
            return;
        case 'conversation.item.input_audio_transcription.completed':
            if (itemId) {
                if (typeof event.transcript === 'string') {
                    this.transcript.completed(itemId, event.transcript);
                } else {
                    // Without a usable final, keep what streamed.
                    this.transcript.failed(itemId);
                }
                this.emitFinal();
            }
            return;
        case 'conversation.item.input_audio_transcription.failed':
            if (itemId) {
                this.transcript.failed(itemId);
                this.emitFinal();
            }
            return;
        case 'error':
            // Once stopping, teardown is already under way; an error must not
            // cut off the last segment's transcript.
            if (event.error?.code === COMMIT_EMPTY_ERROR) {
                this.acknowledgeCommit();
                return;
            }
            if (this.stopping) {
                return;
            }
            this.fail(new DictationError('provider_error'));
            break;
        default:
            break;
        }
    }

    private emitFinal() {
        this.transcriptHandler(this.transcript.snapshot());
        this.settleIfDone();
    }

    private hasOutstandingWork(): boolean {
        return this.outstandingCommits > 0 || this.transcript.hasPending();
    }

    private settleIfDone() {
        if (this.finalWaiter && !this.hasOutstandingWork() && !this.awaitingProviderCommit) {
            this.finalWaiter();
        }
    }

    private fail(error: DictationError) {
        if (this.stopped) {
            return;
        }
        const wasStarted = this.started;
        this.stopped = true;
        this.release();
        if (wasStarted) {
            this.errorHandler(error);
        }
    }

    stop({waitForFinal = true}: {waitForFinal?: boolean} = {}): Promise<void> {
        if (!this.stopping) {
            this.stopRequested = true;
            if (this.lostTimer !== null) {
                clearTimeout(this.lostTimer);
                this.lostTimer = null;
            }
            this.stopping = this.finish(waitForFinal);
        }
        return this.stopping;
    }

    private async finish(waitForFinal: boolean) {
        if (this.stopped) {
            return;
        }
        if (!this.started) {
            this.stopped = true;
            this.release();
            return;
        }
        if (this.sampler !== null) {
            this.deps.clearInterval(this.sampler);
            this.sampler = null;
        }

        // Server VAD commits only after it hears the speech end, so speech
        // still in the buffer is committed here for every model. Recent
        // speech it already heard end may have a commit on the way.
        const heard = this.segmenter?.lastSpeech() ?? null;
        if (waitForFinal && this.segmenter?.flush()) {
            this.commit();
        } else if (waitForFinal && !this.clientCommits && heard !== null && this.deps.now() - heard < PROVIDER_COMMIT_WINDOW_MS) {
            this.awaitingProviderCommit = true;
        }

        // No audio is sent after stop: the mic goes off now, and only the
        // transcript of audio already sent is awaited.
        this.stream?.getTracks().forEach((track) => track.stop());
        if (waitForFinal && (this.hasOutstandingWork() || this.awaitingProviderCommit)) {
            await new Promise<void>((resolve) => {
                const timer = setTimeout(resolve, FINAL_TRANSCRIPT_WAIT_MS);
                this.finalWaiter = () => {
                    clearTimeout(timer);
                    resolve();
                };
            });
        }
        this.finalWaiter = null;
        this.stopped = true;
        this.release();
    }

    private endSession() {
        const sessionId = this.sessionId;
        this.sessionId = null;
        if (sessionId) {
            this.deps.endSession(sessionId).catch(() => { /* best effort; the provider ends it once the peer closes */ });
        }
    }

    private release() {
        this.connectAbort.abort();
        this.endSession();
        if (this.lostTimer !== null) {
            clearTimeout(this.lostTimer);
            this.lostTimer = null;
        }
        this.unsubscribeSessionEnded?.();
        this.unsubscribeSessionEnded = null;
        if (this.sampler !== null) {
            this.deps.clearInterval(this.sampler);
            this.sampler = null;
        }
        this.meter?.close();
        this.meter = null;
        this.channel?.close();
        this.channel = null;
        this.pc?.close();
        this.pc = null;
        this.stream?.getTracks().forEach((track) => track.stop());
        this.stream = null;
    }
}
