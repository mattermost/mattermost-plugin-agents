// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Transcript, joinSegments} from './transcript';

describe('Transcript', () => {
    test('a final transcript replaces the streamed partial', () => {
        const t = new Transcript();
        t.delta('a', 'Hel');
        t.delta('a', 'lo wor');
        expect(t.snapshot()).toEqual({text: 'Hello wor'});

        t.completed('a', 'Hello world.');
        expect(t.snapshot()).toEqual({text: 'Hello world.'});
        expect(t.hasPending()).toBe(false);
    });

    test('completions that arrive out of order are placed by commit order', () => {
        const t = new Transcript();
        t.committed('a', null);
        t.committed('b', 'a');
        t.committed('c', 'b');
        t.completed('c', 'three');
        t.completed('a', 'one');
        expect(t.snapshot().text).toBe('one three');
        expect(t.hasPending()).toBe(true);

        t.completed('b', 'two');
        expect(t.snapshot()).toEqual({text: 'one two three'});
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
        expect(t.snapshot().text).toBe('partial words');
        expect(t.hasPending()).toBe(false);
    });

    test('deltas after completion are ignored', () => {
        const t = new Transcript();
        t.completed('a', 'done');
        t.delta('a', ' extra');
        expect(t.snapshot().text).toBe('done');
    });
});

describe('joinSegments', () => {
    test.each([
        {name: 'a segment continuing a sentence starts lowercase', segments: ['Can you review the Bifrost changes', 'In the dictation pull request'], want: 'Can you review the Bifrost changes in the dictation pull request'},
        {name: 'after a period it keeps its capital', segments: ['That works.', 'Thanks'], want: 'That works. Thanks'},
        {name: 'after a question mark', segments: ['Is it ready?', 'Ship it'], want: 'Is it ready? Ship it'},
        {name: 'after an exclamation mark', segments: ['Done!', 'Next'], want: 'Done! Next'},
        {name: 'after an ellipsis', segments: ['Well…', 'Maybe'], want: 'Well… Maybe'},
        {name: 'after a period inside a closing quote', segments: ['He said "go."', 'Then'], want: 'He said "go." Then'},
        {name: 'after a comma', segments: ['First,', 'Then the rest'], want: 'First, then the rest'},
        {name: 'a contraction', segments: ['and', "Don't forget"], want: "and don't forget"},
        {name: 'the pronoun I', segments: ['before Thursday', 'I will check'], want: 'before Thursday I will check'},
        {name: "the contraction I'm", segments: ['so', "I'm late"], want: "so I'm late"},
        {name: 'an acronym', segments: ['ask', 'QA for help'], want: 'ask QA for help'},
        {name: 'a mixed-case term', segments: ['set up', 'OAuth next'], want: 'set up OAuth next'},
        {name: 'a word the transcript capitalizes mid-sentence', segments: ['ask Christopher about it', 'Christopher agreed'], want: 'ask Christopher about it Christopher agreed'},
        {name: 'leading punctuation is left alone', segments: ['he said', '"Yes" loudly'], want: 'he said "Yes" loudly'},
        {name: 'a non-Latin capital', segments: ['we met', 'Über das'], want: 'we met über das'},
        {name: 'a single segment is unchanged', segments: ['In the dictation pull request'], want: 'In the dictation pull request'},
    ])('$name', ({segments, want}) => {
        expect(joinSegments(segments)).toBe(want);
    });

    test('streamed text is joined the same way', () => {
        const t = new Transcript();
        t.completed('a', 'Can you review the changes');
        t.delta('b', 'In the pull');
        expect(t.snapshot().text).toBe('Can you review the changes in the pull');
    });
});
