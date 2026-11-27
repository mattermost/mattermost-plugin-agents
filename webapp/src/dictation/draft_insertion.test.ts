// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {DraftWriter, createAnchor, renderDraft} from './draft_insertion';

describe('renderDraft', () => {
    test.each([
        {name: 'empty draft', message: '', caret: 0, text: 'hello', want: 'hello'},
        {name: 'append after a word', message: 'Hi', caret: 2, text: 'there', want: 'Hi there'},
        {name: 'append after a space', message: 'Hi ', caret: 3, text: 'there', want: 'Hi there'},
        {name: 'insert mid-message', message: 'Hello world', caret: 5, text: 'big', want: 'Hello big world'},
        {name: 'insert before a space', message: 'Hello world', caret: 6, text: 'big', want: 'Hello big world'},
        {name: 'insert at start', message: 'world', caret: 0, text: 'Hello', want: 'Hello world'},
        {name: 'no caret (WYSIWYG) appends', message: 'Hi', caret: undefined, text: 'there', want: 'Hi there'}, // eslint-disable-line no-undefined
        {name: 'out-of-range caret appends', message: 'Hi', caret: 99, text: 'there', want: 'Hi there'},
        {name: 'no text restores the draft', message: 'Hello world', caret: 5, text: '', want: 'Hello world'},
    ])('$name', ({message, caret, text, want}) => {
        expect(renderDraft(createAnchor(message, caret), text)).toBe(want);
    });
});

describe('DraftWriter', () => {
    test('recognizes its own writes, including ones the draft has not caught up to', () => {
        const writer = new DraftWriter('Hi', 2);
        const first = writer.render('one');
        const second = writer.render('one two');

        expect(writer.isOwnWrite('Hi')).toBe(true);
        expect(writer.isOwnWrite(first)).toBe(true);
        expect(writer.isOwnWrite(second)).toBe(true);
    });

    test('a message dictation never wrote is a user edit', () => {
        const writer = new DraftWriter('Hi', 2);
        writer.render('one');
        expect(writer.isOwnWrite('Hi one!')).toBe(false);
    });

    test('an older write after a newer one was seen is a user edit', () => {
        const writer = new DraftWriter('', 0);
        const first = writer.render('one');
        const second = writer.render('one two');
        expect(writer.isOwnWrite(second)).toBe(true);
        expect(writer.isOwnWrite(first)).toBe(false);
    });

    test('a cleared draft (message sent) is a user edit', () => {
        const writer = new DraftWriter('', 0);
        expect(writer.isOwnWrite(writer.render('hello'))).toBe(true);
        expect(writer.isOwnWrite('')).toBe(false);
    });
});
