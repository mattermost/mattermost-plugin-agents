// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export type DictationProvider = 'openai' | 'azure';

export type DictationUnavailableReason = 'disabled' | 'license' | 'misconfigured' | 'access_denied';

// GET /dictation/availability
export type DictationAvailability = {
    enabled: boolean;
    available: boolean;
    provider: DictationProvider | '';
    model: string;
    transport: 'webrtc';

    // Omitted when available.
    reason?: DictationUnavailableReason;
};

// POST /dictation/session
export type DictationSession = {
    clientSecret: string;

    // Unix milliseconds; the secret is only needed until the connection is made.
    expiresAt: number;
    callsURL: string;
    provider: DictationProvider;
    model: string;

    // "none" means the provider does no turn detection and the client
    // commits each segment itself (gpt-live-transcribe).
    turnDetection: 'none' | 'server_vad';
};

export type DictationErrorReason =
    | 'mic_blocked'
    | 'no_mic'
    | 'rate_limited'
    | 'forbidden'
    | 'unavailable'
    | 'provider_unreachable'
    | 'provider_error'
    | 'connection_lost'
    | 'audio_blocked';

// errorReasonForStatus maps a POST /dictation/session failure to an error state.
export function errorReasonForStatus(status: number): DictationErrorReason {
    switch (status) {
    case 403:
        return 'forbidden';
    case 429:
        return 'rate_limited';
    case 502:
        return 'provider_unreachable';
    default:
        return 'unavailable';
    }
}

export class DictationError extends Error {
    readonly reason: DictationErrorReason;

    constructor(reason: DictationErrorReason, message?: string) {
        super(message || reason);
        this.reason = reason;
    }
}

// The transcript so far in speaking order: finalized segments, and streamed
// text for segments not finalized yet.
export type TranscriptUpdate = {
    text: string;
};

// A live speech-to-text connection. WebRTC is the only implementation today;
// a WebSocket transport or a server relay can implement the same interface.
export interface DictationTransport {

    // Resolves once audio is flowing to the provider. Rejects with a DictationError.
    start(): Promise<void>;

    // Finalizes the last segment, waits briefly for its transcript, then
    // releases the microphone and the connection. Safe to call at any time.
    stop(): Promise<void>;

    // The microphone is granted and the connection is being made.
    onConnecting(handler: () => void): void;

    // Every change to the transcript, partial or final.
    onTranscript(handler: (update: TranscriptUpdate) => void): void;

    // An error after start resolved. The transport has already torn down.
    onError(handler: (error: DictationError) => void): void;

    // No speech for the silence cap; the caller should stop.
    onIdle(handler: () => void): void;

    // The microphone's RMS level (0 to 1), sampled while listening.
    onLevel(handler: (level: number) => void): void;
}
