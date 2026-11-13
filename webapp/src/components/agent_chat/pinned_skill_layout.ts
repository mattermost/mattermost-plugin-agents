// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// A pinned skill is an index into the prompts, or 'more' for the "+N" chip.
export type PinnedSkillEntry = number | 'more';

export interface PinnedSkillFitInput {

    /** The measured width of each prompt button, in order. */
    widths: number[];

    /** The width of the "+N" chip, measured with the largest N. */
    chipWidth: number;

    /** The gap between two items on a line. */
    gap: number;

    /** The width the lines can fill. */
    available: number;
    maxLines: number;

    /** At most this many prompts are shown as buttons. Omit it for no count cap. */
    maxVisible?: number;
}

export interface PinnedSkillFit {

    /** The entries on each line. */
    lines: PinnedSkillEntry[][];

    /** How many prompts are shown as buttons; the prompts after them are behind the chip. */
    visibleCount: number;
}

// Lays the entries out left to right, starting a new line when the next one no longer fits.
// Returns null when that takes more than maxLines. An entry wider than a whole line stands
// alone on its own.
function pack(entries: Array<{entry: PinnedSkillEntry; width: number}>, available: number, gap: number, maxLines: number): PinnedSkillEntry[][] | null {
    const lines: PinnedSkillEntry[][] = [];
    let used = 0;
    for (const {entry, width} of entries) {
        const current = lines[lines.length - 1];
        const clamped = Math.min(width, available);
        if (current && used + gap + clamped <= available) {
            current.push(entry);
            used += gap + clamped;
        } else {
            if (lines.length >= maxLines) {
                return null;
            }
            lines.push([entry]);
            used = clamped;
        }
    }
    return lines;
}

// Shows as many prompts as fit in maxLines lines, and at most maxVisible of them. The ones that do
// not fit collapse into one "+N" chip, which takes its own place on the last line, so the lines
// never run past maxLines.
export function fitPinnedSkillLines({widths, chipWidth, gap, available, maxLines, maxVisible}: PinnedSkillFitInput): PinnedSkillFit {
    const items = widths.map((width, index) => ({entry: index as PinnedSkillEntry, width}));
    const limit = Math.min(widths.length, maxVisible ?? widths.length);

    for (let count = limit; count > 0; count--) {
        const entries = items.slice(0, count);
        if (count < widths.length) {
            entries.push({entry: 'more', width: chipWidth});
        }
        const lines = pack(entries, available, gap, maxLines);
        if (lines) {
            return {lines, visibleCount: count};
        }
    }

    if (widths.length === 0) {
        return {lines: [], visibleCount: 0};
    }
    return {lines: [['more']], visibleCount: 0};
}
