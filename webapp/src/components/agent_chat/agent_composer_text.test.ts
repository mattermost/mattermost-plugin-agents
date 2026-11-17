// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {readComposerText, writeComposerText} from './agent_composer_text';

const rootWith = (html: string) => {
    const root = document.createElement('div');
    root.innerHTML = html;
    document.body.appendChild(root);
    return root;
};

describe('composer text', () => {
    afterEach(() => {
        document.body.innerHTML = '';
        jest.restoreAllMocks();
    });

    test('a textarea reports its selection, or the whole draft when nothing is selected', () => {
        const root = rootWith('<textarea>hello world</textarea>');
        const area = root.querySelector('textarea')!;
        area.setSelectionRange(0, 5);
        expect(readComposerText(root)).toEqual({text: 'hello', found: true, selected: true});

        area.setSelectionRange(0, 0);
        expect(readComposerText(root)).toEqual({text: 'hello world', found: true, selected: false});
    });

    test('writing a textarea replaces the selection or the whole draft and tells the host', () => {
        const root = rootWith('<textarea>hello world</textarea>');
        const area = root.querySelector('textarea')!;
        const onInput = jest.fn();
        area.addEventListener('input', onInput);
        area.setSelectionRange(6, 11);

        expect(writeComposerText(root, 'there', true)).toBe(true);
        expect(area.value).toBe('hello there');
        expect(onInput).toHaveBeenCalledTimes(1);

        expect(writeComposerText(root, 'fresh', false)).toBe(true);
        expect(area.value).toBe('fresh');
    });

    test('a contenteditable is written with insertText and never by replacing its text', () => {
        const root = rootWith('<div contenteditable="true">keep this</div>');
        const editable = root.querySelector<HTMLElement>('[contenteditable="true"]')!;
        const insert = jest.fn(() => true);
        Object.defineProperty(document, 'execCommand', {configurable: true, value: insert});

        expect(writeComposerText(root, '# heading', false)).toBe(true);
        expect(insert).toHaveBeenCalledWith('insertText', false, '# heading');
        expect(editable.textContent).toBe('keep this');
    });

    test('a missing editor is reported and left alone', () => {
        const root = rootWith('<p>no editor</p>');
        expect(readComposerText(root)).toEqual({text: '', found: false, selected: false});
        expect(writeComposerText(root, 'nope', false)).toBe(false);
        expect(root.textContent).toBe('no editor');
    });
});
