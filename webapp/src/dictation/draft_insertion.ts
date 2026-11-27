// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export type InsertionAnchor = {
    prefix: string;
    suffix: string;
};

// createAnchor splits the draft at the caret. The WYSIWYG editor reports no
// caret position, so dictation then appends to the end of the message.
export function createAnchor(message: string, caret: number | undefined | null): InsertionAnchor {
    const at = typeof caret === 'number' && caret >= 0 && caret <= message.length ? caret : message.length;
    return {prefix: message.slice(0, at), suffix: message.slice(at)};
}

const endsWithSpace = (s: string) => (/\s$/).test(s);
const startsWithSpace = (s: string) => (/^\s/).test(s);

export function renderDraft(anchor: InsertionAnchor, text: string): string {
    if (!text) {
        return anchor.prefix + anchor.suffix;
    }
    const before = anchor.prefix && !endsWithSpace(anchor.prefix) ? ' ' : '';
    const after = anchor.suffix && !startsWithSpace(anchor.suffix) ? ' ' : '';
    return anchor.prefix + before + text + after + anchor.suffix;
}

// DraftWriter tracks which messages dictation wrote, so a draft that changes
// to anything else is recognized as the user's own edit. Writes reach the
// draft asynchronously, so a draft can briefly show an older write.
export class DraftWriter {
    private readonly anchor: InsertionAnchor;
    private confirmed: string;
    private pending: string[] = [];

    constructor(message: string, caret: number | undefined | null) {
        this.anchor = createAnchor(message, caret);
        this.confirmed = message;
    }

    render(text: string): string {
        const message = renderDraft(this.anchor, text);
        const last = this.pending.length ? this.pending[this.pending.length - 1] : this.confirmed;
        if (message !== last) {
            this.pending.push(message);
        }
        return message;
    }

    // isOwnWrite reports whether message is the draft before dictation or
    // one dictation wrote; false means the user edited the draft.
    isOwnWrite(message: string): boolean {
        if (message === this.confirmed) {
            return true;
        }
        const index = this.pending.indexOf(message);
        if (index === -1) {
            return false;
        }
        this.confirmed = message;
        this.pending = this.pending.slice(index + 1);
        return true;
    }
}
