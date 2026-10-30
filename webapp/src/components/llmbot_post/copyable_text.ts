// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Round} from './turn_content_utils';

// The smooth-streaming fade wraps revealed text in private-use markers
// (streaming_markdown.tsx: U+E000 + one id char in U+E100..U+EFFF, and U+E001).
// They only exist in the revealed text; a copy never carries them.
const FADE_MARKERS = /[-]|/g;

/**
 * The text the Copy button puts on the clipboard: the markdown of the answer
 * rounds (the text the post shows as its message), in order, joined by a blank
 * line. Reasoning, tool activity and narration rounds folded into the activity
 * area are not part of it. Inline citation markers stay when they are in the
 * stored text. Rounds with no text are skipped; empty means nothing to copy.
 */
export function getCopyableText(answerRounds: Round[]): string {
    return answerRounds.
        map((round) => round.text.replace(FADE_MARKERS, '')).
        filter((text) => text.trim() !== '').
        join('\n\n');
}
