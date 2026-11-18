// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Segmenter, rmsLevel} from './segmenter';

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

describe('Segmenter', () => {
    test('commits after about 700 ms of silence following speech', () => {
        const {segmenter, onCommit} = setup();
        let now = drive(segmenter, SPEECH, 0, 1000);
        now = drive(segmenter, SILENCE, now, 650);
        expect(onCommit).not.toHaveBeenCalled();

        drive(segmenter, SILENCE, now, 200);
        expect(onCommit).toHaveBeenCalledTimes(1);
    });

    test('a short pause does not split a segment', () => {
        const {segmenter, onCommit} = setup();
        let now = drive(segmenter, SPEECH, 0, 1000);
        now = drive(segmenter, SILENCE, now, 300);
        drive(segmenter, SPEECH, now, 1000);
        expect(onCommit).not.toHaveBeenCalled();
    });

    test('silence without speech never commits an empty buffer', () => {
        const {segmenter, onCommit} = setup();
        drive(segmenter, SILENCE, 0, 5000);
        expect(segmenter.flush()).toBe(false);
        expect(onCommit).not.toHaveBeenCalled();
    });

    test('forces a commit every 15 s of continuous speech', () => {
        const {segmenter, onCommit} = setup();
        const now = drive(segmenter, SPEECH, 0, 14900);
        expect(onCommit).not.toHaveBeenCalled();

        drive(segmenter, SPEECH, now, 15200);
        expect(onCommit).toHaveBeenCalledTimes(2);
    });

    test('flush reports speech that has not been committed, once', () => {
        const {segmenter, onCommit} = setup();
        drive(segmenter, SPEECH, 0, 500);
        expect(segmenter.flush()).toBe(true);
        expect(segmenter.flush()).toBe(false);
        expect(onCommit).not.toHaveBeenCalled();
    });

    test('reports idle once after 30 s without speech', () => {
        const {segmenter, onIdle} = setup();
        let now = drive(segmenter, SILENCE, 0, 29900);
        expect(onIdle).not.toHaveBeenCalled();

        now = drive(segmenter, SILENCE, now, 5000);
        expect(onIdle).toHaveBeenCalledTimes(1);

        now = drive(segmenter, SPEECH, now, 500);
        drive(segmenter, SILENCE, now, 29000);
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
