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
        {name: 'after a period it keeps its capital', segments: ['That works.', 'The rest'], want: 'That works. The rest'},
        {name: 'after a question mark', segments: ['Is it ready?', 'Then ship it'], want: 'Is it ready? Then ship it'},
        {name: 'after an exclamation mark', segments: ['Done!', 'And next'], want: 'Done! And next'},
        {name: 'after an ellipsis', segments: ['Well…', 'Maybe'], want: 'Well… Maybe'},
        {name: 'after a period inside a closing quote', segments: ['He said "go."', 'Then'], want: 'He said "go." Then'},
        {name: 'after a comma', segments: ['First,', 'Then the rest'], want: 'First, then the rest'},
        {name: 'a contraction', segments: ['and', "Don't forget"], want: "and don't forget"},
        {name: 'a curly-quote contraction', segments: ['and', 'It’s done'], want: 'and it’s done'},
        {name: 'a pronoun other than I', segments: ['ask if', 'We can ship'], want: 'ask if we can ship'},
        {name: 'an auxiliary', segments: ['the build', 'Should pass'], want: 'the build should pass'},
        {name: 'the pronoun I', segments: ['before Thursday', 'I will check'], want: 'before Thursday I will check'},
        {name: "the contraction I'm", segments: ['so', "I'm late"], want: "so I'm late"},
        {name: 'a name not seen before', segments: ['Please contact', 'Christopher tomorrow'], want: 'Please contact Christopher tomorrow'},
        {name: 'a weekday', segments: ['Schedule this for', 'Monday morning'], want: 'Schedule this for Monday morning'},
        {name: 'a month', segments: ['the release in', 'March'], want: 'the release in March'},
        {name: 'May, a month and an auxiliary', segments: ['ship it in', 'May'], want: 'ship it in May'},
        {name: 'Will, a name and an auxiliary', segments: ['ask', 'Will about it'], want: 'ask Will about it'},
        {name: 'a word not on the list', segments: ['we need', 'Better tests'], want: 'we need Better tests'},
        {name: 'an acronym', segments: ['ask', 'QA for help'], want: 'ask QA for help'},
        {name: 'a mixed-case term', segments: ['set up', 'OAuth next'], want: 'set up OAuth next'},
        {name: 'an all-caps continuation word', segments: ['this is', 'THE plan'], want: 'this is THE plan'},
        {name: 'leading punctuation is left alone', segments: ['he said', '"The end" loudly'], want: 'he said "The end" loudly'},
        {name: 'a single segment is unchanged', segments: ['In the dictation pull request'], want: 'In the dictation pull request'},
    ])('$name', ({segments, want}) => {
        expect(joinSegments(segments)).toBe(want);
    });

    test('streamed text is joined the same way', () => {
        const t = new Transcript();
        t.completed('a', 'Can you review the changes');
        t.delta('b', 'In the pull');
        expect(t.snapshot().text).toBe('Can you review the changes in the pull');
        t.delta('c', 'Christopher');
        expect(t.snapshot().text).toBe('Can you review the changes in the pull Christopher');
    });
});
