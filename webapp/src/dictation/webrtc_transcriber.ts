// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {createDictationSession} from '@/client';

import {Segmenter, rmsLevel} from './segmenter';
import {Transcript} from './transcript';
import {
    DictationError,
    DictationSession,
    DictationTransport,
    TranscriptUpdate,
    errorReasonForStatus,
} from './types';

const SDP_EXCHANGE_TIMEOUT_MS = 15000;
const CHANNEL_OPEN_TIMEOUT_MS = 15000;
const FINAL_TRANSCRIPT_WAIT_MS = 1500;
const LEVEL_SAMPLE_INTERVAL_MS = 50;

// LevelMeter reports the microphone's current RMS level (0 to 1).
export type LevelMeter = {
    level(): number;
    close(): void;
};

export type TranscriberDeps = {
    getUserMedia: (constraints: MediaStreamConstraints) => Promise<MediaStream>;
    createSession: () => Promise<DictationSession>;
    createPeerConnection: () => RTCPeerConnection;
    fetch: typeof fetch;
    createLevelMeter: (stream: MediaStream) => LevelMeter;
    now: () => number;
    setInterval: (fn: () => void, ms: number) => ReturnType<typeof setInterval>;
    clearInterval: (id: ReturnType<typeof setInterval>) => void;
};

function createAnalyserLevelMeter(stream: MediaStream): LevelMeter {
    const AudioContextClass = window.AudioContext || (window as any).webkitAudioContext;
    const context: AudioContext = new AudioContextClass();
    const analyser = context.createAnalyser();
    analyser.fftSize = 1024;
    context.createMediaStreamSource(stream).connect(analyser);
    const samples = new Float32Array(analyser.fftSize);
    return {
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
    createPeerConnection: () => new RTCPeerConnection(),
    fetch: (...args) => window.fetch(...args),
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
    type?: string;
    item_id?: string;
    previous_item_id?: string | null;
    delta?: string;
    transcript?: string;
    error?: {code?: string};
};

// Sent when a commit holds under 100 ms of audio, e.g. a stop right after a
// forced commit. Nothing is lost.
const COMMIT_EMPTY_ERROR = 'input_audio_buffer_commit_empty';

// WebRTCTranscriber streams the microphone to OpenAI's realtime transcription
// over WebRTC with an ephemeral key minted by the plugin server.
export class WebRTCTranscriber implements DictationTransport {
    private readonly deps: TranscriberDeps;
    private readonly transcript = new Transcript();
    private transcriptHandler: (update: TranscriptUpdate) => void = () => { /* unset */ };
    private errorHandler: (error: DictationError) => void = () => { /* unset */ };
    private idleHandler: () => void = () => { /* unset */ };
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
    private finalWaiter: (() => void) | null = null;

    constructor(deps: TranscriberDeps = browserTranscriberDeps()) {
        this.deps = deps;
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
        // The microphone is requested before minting so a permission prompt
        // the user dismisses doesn't spend a token.
        try {
            this.stream = await this.deps.getUserMedia({audio: {echoCancellation: true, noiseSuppression: true}});
        } catch (err) {
            throw micError(err);
        }
        this.throwIfStopped();
        this.connectingHandler();

        let session: DictationSession;
        try {
            session = await this.deps.createSession();
        } catch (err) {
            throw new DictationError(errorReasonForStatus((err as {status_code?: number})?.status_code ?? 0));
        }
        this.throwIfStopped();
        this.clientCommits = session.turnDetection === 'none';

        const pc = this.deps.createPeerConnection();
        this.pc = pc;
        for (const track of this.stream.getAudioTracks()) {
            pc.addTrack(track, this.stream);
            track.addEventListener('ended', () => this.fail(new DictationError('connection_lost')));
        }
        const channel = pc.createDataChannel('oai-events');
        this.channel = channel;
        channel.addEventListener('message', (e: MessageEvent) => this.handleEvent(e.data));
        pc.addEventListener('connectionstatechange', () => {
            if (pc.connectionState === 'failed') {
                this.fail(new DictationError('connection_lost'));
            }
        });

        let openTimer: ReturnType<typeof setTimeout> | undefined;
        const opened = new Promise<void>((resolve, reject) => {
            openTimer = setTimeout(() => reject(new DictationError('provider_unreachable')), CHANNEL_OPEN_TIMEOUT_MS);
            channel.addEventListener('open', () => resolve());
        });

        // A failure before the channel opens is thrown below; this keeps the
        // pending timeout from surfacing as an unhandled rejection.
        opened.catch(() => { /* handled by the await below */ });
        try {
            await this.negotiate(pc, session);
            this.throwIfStopped();
            await opened;
        } finally {
            clearTimeout(openTimer);
        }
        this.throwIfStopped();
        this.startSampling();
        this.started = true;
    }

    private async negotiate(pc: RTCPeerConnection, session: DictationSession) {
        const offer = await pc.createOffer();
        await pc.setLocalDescription(offer);

        const abort = new AbortController();
        const abortTimer = setTimeout(() => abort.abort(), SDP_EXCHANGE_TIMEOUT_MS);
        let answer: string;
        try {
            const response = await this.deps.fetch(session.callsURL, {
                method: 'POST',
                body: offer.sdp,
                headers: {
                    Authorization: `Bearer ${session.clientSecret}`,
                    'Content-Type': 'application/sdp',
                },
                signal: abort.signal,
            });
            if (!response.ok) {
                throw new DictationError('provider_unreachable');
            }
            answer = await response.text();
        } finally {
            clearTimeout(abortTimer);
        }
        this.throwIfStopped();
        await pc.setRemoteDescription({type: 'answer', sdp: answer});
    }

    private throwIfStopped() {
        if (this.stopped) {
            throw new DictationError('unavailable', 'stopped');
        }
    }

    private startSampling() {
        if (!this.stream) {
            return;
        }
        const meter = this.deps.createLevelMeter(this.stream);
        this.meter = meter;
        this.segmenter = new Segmenter({
            onCommit: () => {
                if (this.clientCommits) {
                    this.send({type: 'input_audio_buffer.commit'});
                }
            },
            onIdle: () => this.idleHandler(),
        }, this.deps.now());
        this.sampler = this.deps.setInterval(() => {
            this.segmenter?.sample(meter.level(), this.deps.now());
        }, LEVEL_SAMPLE_INTERVAL_MS);
    }

    private send(event: Record<string, unknown>) {
        if (this.channel?.readyState === 'open') {
            this.channel.send(JSON.stringify(event));
        }
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
        const itemId = event.item_id;
        switch (event.type) {
        case 'input_audio_buffer.committed':
            if (itemId) {
                this.transcript.committed(itemId, event.previous_item_id);
            }
            return;
        case 'conversation.item.input_audio_transcription.delta':
            if (itemId && event.delta) {
                this.transcript.delta(itemId, event.delta);
                this.transcriptHandler(this.transcript.snapshot());
            }
            return;
        case 'conversation.item.input_audio_transcription.completed':
            if (itemId) {
                this.transcript.completed(itemId, event.transcript ?? '');
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
            if (this.stopping || event.error?.code === COMMIT_EMPTY_ERROR) {
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
        if (this.finalWaiter && !this.transcript.hasPending()) {
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

    stop(): Promise<void> {
        if (!this.stopping) {
            this.stopping = this.finish();
        }
        return this.stopping;
    }

    private async finish() {
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
        if (this.clientCommits) {
            this.segmenter?.flush();
        }
        if (this.transcript.hasPending()) {
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

    private release() {
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
