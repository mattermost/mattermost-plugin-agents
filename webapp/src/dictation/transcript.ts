// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {TranscriptUpdate} from './types';

type Segment = {
    partial: string;
    final: string | null;
};

// Transcript assembles provider segments in speaking order. Deltas for a
// segment can arrive before it is committed, and completions can arrive out
// of order, so the committed order (previous_item_id) decides placement.
export class Transcript {
    private order: string[] = [];
    private segments = new Map<string, Segment>();

    private ensure(itemId: string): Segment {
        let segment = this.segments.get(itemId);
        if (!segment) {
            segment = {partial: '', final: null};
            this.segments.set(itemId, segment);
            this.order.push(itemId);
        }
        return segment;
    }

    committed(itemId: string, previousItemId?: string | null) {
        this.ensure(itemId);
        if (!previousItemId || !this.segments.has(previousItemId)) {
            return;
        }
        this.order = this.order.filter((id) => id !== itemId);
        this.order.splice(this.order.indexOf(previousItemId) + 1, 0, itemId);
    }

    delta(itemId: string, delta: string) {
        const segment = this.ensure(itemId);
        if (segment.final === null) {
            segment.partial += delta;
        }
    }

    completed(itemId: string, transcript: string) {
        this.ensure(itemId).final = transcript;
    }

    // A segment the provider failed to transcribe keeps what it had streamed.
    failed(itemId: string) {
        const segment = this.ensure(itemId);
        segment.final = segment.partial;
    }

    hasPending(): boolean {
        for (const segment of this.segments.values()) {
            if (segment.final === null) {
                return true;
            }
        }
        return false;
    }

    snapshot(): TranscriptUpdate {
        const texts: string[] = [];
        for (const id of this.order) {
            const segment = this.segments.get(id)!;
            const text = (segment.final ?? segment.partial).trim();
            if (text) {
                texts.push(text);
            }
        }
        return {text: joinSegments(texts)};
    }
}

const SENTENCE_END = /[.!?…]["'”’)\]]*$/u;

// A plain capitalized word: "In", "Christopher", "Don't". Acronyms and
// mixed-case terms such as "QA" or "OAuth" don't match and keep their case.
const PLAIN_CAPITALIZED = /^\p{Lu}\p{Ll}*(['’]\p{Ll}+)?$/u;
const PRONOUN_I = /^I(['’]\p{Ll}+)?$/u;

const bareWord = (token: string) => token.replace(/^[^\p{L}]+|[^\p{L}'’]+$/gu, '');

// wordsCapitalizedMidSentence collects words the transcript capitalizes
// where no sentence starts, which marks them as proper nouns.
function wordsCapitalizedMidSentence(texts: string[]): Set<string> {
    const words = new Set<string>();
    for (const text of texts) {
        const tokens = text.split(/\s+/);
        for (let i = 1; i < tokens.length; i++) {
            const word = bareWord(tokens[i]);
            if (!SENTENCE_END.test(tokens[i - 1]) && (/^\p{Lu}/u).test(word)) {
                words.add(word);
            }
        }
    }
    return words;
}

// joinSegments joins segments in speaking order. Each segment is transcribed
// as a standalone utterance and starts with a capital, so a segment that
// continues an unfinished sentence gets its first letter lowercased, unless
// that word is "I" or a proper noun.
export function joinSegments(texts: string[]): string {
    const properNouns = wordsCapitalizedMidSentence(texts);
    return texts.map((text, i) => {
        if (i === 0 || SENTENCE_END.test(texts[i - 1])) {
            return text;
        }
        const word = bareWord(text.split(/\s+/, 1)[0]);
        if (!PLAIN_CAPITALIZED.test(word) || PRONOUN_I.test(word) || properNouns.has(word) || !text.startsWith(word.charAt(0))) {
            return text;
        }
        return word.charAt(0).toLowerCase() + text.slice(1);
    }).join(' ');
}
