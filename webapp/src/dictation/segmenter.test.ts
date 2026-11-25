// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {DEFAULT_SEGMENTER_OPTIONS, Segmenter, rmsLevel} from './segmenter';

const SPEECH = 0.2;
const SILENCE = 0;
const STEP_MS = 50;

// drive feeds level samples every STEP_MS from `from` for `durationMs`.
function drive(segmenter: Segmenter, level: number, from: number, durationMs: number): number {
    let now = from;
    for (; now < from + durationMs; now += STEP_MS) {
        segmenter.sample(level, now);
    }
    return now;
}

function setup() {
    const onCommit = jest.fn();
    const onIdle = jest.fn();
    const segmenter = new Segmenter({onCommit, onIdle}, 0);
    return {segmenter, onCommit, onIdle};
}

const {silenceCommitMs, maxSegmentMs, idleStopMs} = DEFAULT_SEGMENTER_OPTIONS;

type Step = {level: number; ms: number};
const speech = (ms: number): Step => ({level: SPEECH, ms});
const silence = (ms: number): Step => ({level: SILENCE, ms});

describe('Segmenter', () => {
    test('defaults: commit on 2 s of silence, at most every 30 s of speech, stop after 30 s of silence', () => {
        expect(DEFAULT_SEGMENTER_OPTIONS).toMatchObject({silenceCommitMs: 2000, maxSegmentMs: 30000, idleStopMs: 30000});
    });

    test.each([
        {name: 'a thinking pause does not split a sentence', steps: [speech(1000), silence(1000), speech(1000), silence(silenceCommitMs - 100)], commits: 0},
        {name: 'a pause just short of the threshold', steps: [speech(1000), silence(silenceCommitMs - 100)], commits: 0},
        {name: 'silence past the threshold commits once', steps: [speech(1000), silence(silenceCommitMs + 200), silence(5000)], commits: 1},
        {name: 'two phrases with a long pause between', steps: [speech(1000), silence(silenceCommitMs + 200), speech(1000), silence(silenceCommitMs + 200)], commits: 2},
        {name: 'silence without speech never commits an empty buffer', steps: [silence(5000)], commits: 0},
        {name: 'continuous speech under the cap', steps: [speech(maxSegmentMs - 100)], commits: 0},
        {name: 'continuous speech is committed at the cap, then again', steps: [speech((maxSegmentMs * 2) + 200)], commits: 2},
    ])('$name: $commits commits', ({steps, commits}) => {
        const {segmenter, onCommit} = setup();
        let now = 0;
        for (const step of steps) {
            now = drive(segmenter, step.level, now, step.ms);
        }
        expect(onCommit).toHaveBeenCalledTimes(commits);
    });

    test('flush reports speech that has not been committed, once', () => {
        const {segmenter, onCommit} = setup();
        drive(segmenter, SPEECH, 0, 500);
        expect(segmenter.flush()).toBe(true);
        expect(segmenter.flush()).toBe(false);
        expect(onCommit).not.toHaveBeenCalled();
    });

    test('silence leaves nothing to flush', () => {
        const {segmenter} = setup();
        drive(segmenter, SILENCE, 0, 5000);
        expect(segmenter.flush()).toBe(false);
    });

    test('reports idle once after 30 s without speech', () => {
        const {segmenter, onIdle} = setup();
        let now = drive(segmenter, SILENCE, 0, idleStopMs - 100);
        expect(onIdle).not.toHaveBeenCalled();

        now = drive(segmenter, SILENCE, now, 5000);
        expect(onIdle).toHaveBeenCalledTimes(1);

        now = drive(segmenter, SPEECH, now, 500);
        drive(segmenter, SILENCE, now, idleStopMs - 1000);
        expect(onIdle).toHaveBeenCalledTimes(1);
    });
});

describe('rmsLevel', () => {
    test.each([
        {name: 'empty', samples: [], want: 0},
        {name: 'silence', samples: [0, 0, 0], want: 0},
        {name: 'full scale', samples: [1, -1, 1, -1], want: 1},
    ])('$name', ({samples, want}) => {
        expect(rmsLevel(new Float32Array(samples))).toBeCloseTo(want);
    });
});
