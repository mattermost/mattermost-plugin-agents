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

// Words that can continue a sentence and are never names. Only these lose a
// segment's leading capital: a wrongly lowercased name ("christopher",
// "monday") is worse than a stray capital. "May", "Will" and "June" are left
// out because they are also months or names, and "I" always stays capital.
const CONTINUATION_WORDS = new Set([
    'a', 'an', 'the', 'this', 'that', 'these', 'those', 'some', 'any', 'each', 'every', 'all', 'both', 'either', 'neither', 'no', 'not', 'other', 'such', 'more', 'most',
    'and', 'but', 'or', 'nor', 'so', 'yet', 'then', 'than', 'because', 'if', 'when', 'while', 'where', 'whether', 'although', 'though', 'unless', 'once', 'since', 'until',
    'of', 'in', 'on', 'at', 'to', 'for', 'from', 'with', 'without', 'by', 'about', 'as', 'into', 'onto', 'over', 'under', 'after', 'before', 'during', 'between', 'through', 'across', 'against', 'along', 'among', 'around', 'per', 'via', 'up', 'down', 'out', 'off', 'like',
    'which', 'who', 'whom', 'whose', 'what', 'how', 'why', 'there', 'here',
    'it', 'its', 'we', 'us', 'our', 'you', 'your', 'he', 'him', 'his', 'she', 'her', 'they', 'them', 'their', 'me', 'my',
    'is', 'are', 'was', 'were', 'be', 'been', 'being', 'am', 'do', 'does', 'did', 'have', 'has', 'had', 'would', 'can', 'could', 'should', 'might', 'must', 'shall',
    'also', 'just', 'still', 'even', 'only', 'maybe', 'again', 'too', 'very', 'really',
    "it's", "that's", "there's", "we're", "you're", "they're", "we'll", "you'll", "it'll", "they'll", "we've", "you've", "they've",
    "don't", "doesn't", "didn't", "isn't", "aren't", "wasn't", "weren't", "won't", "can't", "couldn't", "shouldn't", "wouldn't",
]);

const firstWord = (text: string) => text.split(/\s+/, 1)[0].replace(/[^\p{L}'’]+$/u, '').replace(/’/g, "'");

// joinSegments joins segments in speaking order. Each segment is transcribed
// as a standalone utterance and starts with a capital, so a segment that
// continues an unfinished sentence and starts with a continuation word gets
// that capital lowered.
export function joinSegments(texts: string[]): string {
    return texts.map((text, i) => {
        if (i === 0 || SENTENCE_END.test(texts[i - 1])) {
            return text;
        }
        const word = firstWord(text);
        const lower = word.toLowerCase();
        if (!CONTINUATION_WORDS.has(lower) || word !== lower.charAt(0).toUpperCase() + lower.slice(1)) {
            return text;
        }
        return text.charAt(0).toLowerCase() + text.slice(1);
    }).join(' ');
}
