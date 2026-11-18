// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Transcript} from './transcript';

describe('Transcript', () => {
    test('a final transcript replaces the streamed partial', () => {
        const t = new Transcript();
        t.delta('a', 'Hel');
        t.delta('a', 'lo wor');
        expect(t.snapshot()).toEqual({text: 'Hello wor', finalText: ''});

        t.completed('a', 'Hello world.');
        expect(t.snapshot()).toEqual({text: 'Hello world.', finalText: 'Hello world.'});
        expect(t.hasPending()).toBe(false);
    });

    test('completions that arrive out of order are placed by commit order', () => {
        const t = new Transcript();
        t.committed('a', null);
        t.committed('b', 'a');
        t.committed('c', 'b');
        t.completed('c', 'three');
        t.completed('a', 'one');
        expect(t.snapshot().finalText).toBe('one three');
        expect(t.hasPending()).toBe(true);

        t.completed('b', 'two');
        expect(t.snapshot()).toEqual({text: 'one two three', finalText: 'one two three'});
    });

    test('a segment seen first through a delta moves to its committed place', () => {
        const t = new Transcript();
        t.delta('b', 'second');
        t.committed('a', null);
        t.committed('b', 'a');
        t.completed('a', 'first');
        expect(t.snapshot().text).toBe('first second');
    });

    test('a failed segment keeps its streamed text', () => {
        const t = new Transcript();
        t.delta('a', 'partial words');
        t.failed('a');
        expect(t.snapshot().finalText).toBe('partial words');
        expect(t.hasPending()).toBe(false);
    });

    test('deltas after completion are ignored', () => {
        const t = new Transcript();
        t.completed('a', 'done');
        t.delta('a', ' extra');
        expect(t.snapshot().text).toBe('done');
    });
});
