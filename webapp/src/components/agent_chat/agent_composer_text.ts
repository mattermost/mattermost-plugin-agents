// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export interface ComposerText {
    text: string;
    found: boolean;

    // True when the user has a non-empty selection. A rewrite then replaces that
    // selection; otherwise it replaces the whole draft.
    selected: boolean;
}

const composerTextarea = (root: ParentNode) => root.querySelector('textarea');
const composerEditable = (root: ParentNode) => root.querySelector<HTMLElement>('[contenteditable="true"]');

const selectionInside = (editable: HTMLElement): string => {
    const selection = window.getSelection();
    if (!selection || selection.isCollapsed || !editable.contains(selection.anchorNode)) {
        return '';
    }
    return selection.toString();
};

export const readComposerText = (root: ParentNode): ComposerText => {
    const area = composerTextarea(root);
    if (area) {
        const start = area.selectionStart ?? 0;
        const end = area.selectionEnd ?? 0;
        const selected = area.value.slice(start, end);
        return {text: selected || area.value, found: true, selected: selected !== ''};
    }
    const editable = composerEditable(root);
    if (!editable) {
        return {text: '', found: false, selected: false};
    }
    const selected = selectionInside(editable);
    const full = editable.innerText ?? editable.textContent ?? '';
    return {text: selected || full, found: true, selected: selected !== ''};
};

const setTextareaValue = (area: HTMLTextAreaElement, value: string) => {
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set;
    setter?.call(area, value);
    area.dispatchEvent(new Event('input', {bubbles: true}));
};

// Replaces the draft, or the current selection when `selected` is set.
// A contenteditable (the host WYSIWYG) is written with insertText. Assigning
// innerText would hand the editor DOM text where it expects markdown, so a
// failed insert leaves the draft untouched.
const writeContentEditable = (editable: HTMLElement, text: string, selected: boolean): boolean => {
    editable.focus();
    const selection = window.getSelection();
    if (!selection) {
        return false;
    }
    const replacingSelection = selected && !selection.isCollapsed && editable.contains(selection.anchorNode);
    if (!replacingSelection) {
        const range = document.createRange();
        range.selectNodeContents(editable);
        selection.removeAllRanges();
        selection.addRange(range);
    }
    if (typeof document.execCommand !== 'function') {
        return false;
    }
    return document.execCommand('insertText', false, text);
};

export const writeComposerText = (root: ParentNode, text: string, selected: boolean): boolean => {
    const area = composerTextarea(root);
    if (area) {
        const start = area.selectionStart ?? 0;
        const end = area.selectionEnd ?? 0;
        const next = selected && start !== end ? area.value.slice(0, start) + text + area.value.slice(end) : text;
        setTextareaValue(area, next);
        return true;
    }
    const editable = composerEditable(root);
    if (!editable) {
        return false;
    }
    return writeContentEditable(editable, text, selected);
};
