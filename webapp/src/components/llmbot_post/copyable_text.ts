// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Round} from './turn_content_utils';

/**
 * The text the Copy button puts on the clipboard: the markdown of the answer
 * rounds (the text the post shows as its message), in order, joined by a blank
 * line. Reasoning, tool activity and narration rounds folded into the activity
 * area are not part of it. Inline citation markers stay when they are in the
 * stored text. Rounds with no text are skipped; empty means nothing to copy.
 */
export function getCopyableText(answerRounds: Round[]): string {
    return answerRounds.
        map((round) => round.text).
        filter((text) => text.trim() !== '').
        join('\n\n');
}
