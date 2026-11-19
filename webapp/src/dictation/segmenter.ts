// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export type SegmenterOptions = {

    // RMS level (0 to 1) at or above which a sample counts as speech.
    speechThreshold: number;

    // Silence after speech that ends a segment.
    silenceCommitMs: number;

    // Continuous speech after which a segment is committed anyway.
    maxSegmentMs: number;

    // Silence after which dictation stops itself.
    idleStopMs: number;
};

export const DEFAULT_SEGMENTER_OPTIONS: SegmenterOptions = {
    speechThreshold: 0.02,

    // Each committed segment is transcribed on its own, capitalized and
    // punctuated as a whole utterance, so committing on a thinking pause
    // breaks sentences. Live text streams before the commit either way.
    silenceCommitMs: 2000,
    maxSegmentMs: 30000,
    idleStopMs: 30000,
};

type Handlers = {
    onCommit: () => void;
    onIdle: () => void;
};

// Segmenter decides when to commit the audio buffer for models without
// provider turn detection, from periodic microphone level samples. It also
// reports long silences for every model.
export class Segmenter {
    private readonly options: SegmenterOptions;
    private readonly handlers: Handlers;
    private inSpeech = false;
    private segmentStartedAt = 0;
    private lastSpeechAt: number;
    private idleReported = false;

    constructor(handlers: Handlers, now: number, options: Partial<SegmenterOptions> = {}) {
        this.handlers = handlers;
        this.options = {...DEFAULT_SEGMENTER_OPTIONS, ...options};
        this.lastSpeechAt = now;
    }

    sample(level: number, now: number) {
        if (level >= this.options.speechThreshold) {
            if (!this.inSpeech) {
                this.inSpeech = true;
                this.segmentStartedAt = now;
            }
            this.lastSpeechAt = now;
            this.idleReported = false;
            if (now - this.segmentStartedAt >= this.options.maxSegmentMs) {
                this.handlers.onCommit();
                this.segmentStartedAt = now;
            }
            return;
        }

        if (this.inSpeech && now - this.lastSpeechAt >= this.options.silenceCommitMs) {
            this.inSpeech = false;
            this.handlers.onCommit();
        }
        if (!this.idleReported && now - this.lastSpeechAt >= this.options.idleStopMs) {
            this.idleReported = true;
            this.handlers.onIdle();
        }
    }

    // flush reports whether speech since the last commit is still uncommitted,
    // and forgets it. The caller commits it; committing an empty buffer is a
    // provider error, so nothing should be sent after silence.
    flush(): boolean {
        const pending = this.inSpeech;
        this.inSpeech = false;
        return pending;
    }
}

// rmsLevel computes the root mean square of time-domain samples in [-1, 1].
export function rmsLevel(samples: Float32Array): number {
    if (samples.length === 0) {
        return 0;
    }
    let sum = 0;
    for (const s of samples) {
        sum += s * s;
    }
    return Math.sqrt(sum / samples.length);
}
