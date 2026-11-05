// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fitStarterLines} from './starter_prompt_layout';

const base = {chipWidth: 20, gap: 6, maxLines: 2};

describe('fitStarterLines', () => {
    test.each([
        {
            name: 'everything fits on one line',
            widths: [30, 30, 30],
            available: 200,
            expected: {lines: [[0, 1, 2]], visibleCount: 3},
        },
        {
            name: 'a second line takes what does not fit on the first',
            widths: [60, 60, 60],
            available: 130,
            expected: {lines: [[0, 1], [2]], visibleCount: 3},
        },
        {
            name: 'what needs a third line collapses into the chip, which closes the second line (four prompts would fit, but not with the chip)',
            widths: [60, 60, 60, 60, 60],
            available: 130,
            expected: {lines: [[0, 1], [2, 'more']], visibleCount: 3},
        },
        {
            name: 'a prompt too wide to share a line takes the second line alone',
            widths: [60, 60, 100],
            available: 130,
            expected: {lines: [[0, 1], [2]], visibleCount: 3},
        },
        {
            name: 'four prompts that fit on two lines show without a chip',
            widths: [60, 60, 60, 60],
            available: 130,
            expected: {lines: [[0, 1], [2, 3]], visibleCount: 4},
        },
        {
            name: 'a prompt wider than a line stands alone',
            widths: [500, 30],
            available: 100,
            expected: {lines: [[0], [1]], visibleCount: 2},
        },
        {
            name: 'only the chip when not even one prompt leaves room for it',
            widths: [90, 90, 90],
            available: 100,
            expected: {lines: [[0], ['more']], visibleCount: 1},
        },
        {
            name: 'no prompts, no lines',
            widths: [],
            available: 100,
            expected: {lines: [], visibleCount: 0},
        },
    ])('$name', ({widths, available, expected}) => {
        expect(fitStarterLines({...base, widths, available})).toEqual(expected);
    });

    test('never takes more than maxLines, whatever the widths', () => {
        for (let count = 0; count <= 12; count++) {
            for (const available of [40, 90, 130, 400]) {
                const widths = Array.from({length: count}, (_, i) => 20 + ((i * 17) % 70));
                const {lines, visibleCount} = fitStarterLines({...base, widths, available});
                expect(lines.length).toBeLessThanOrEqual(base.maxLines);

                const shown = lines.flat();
                expect(shown.filter((e) => e !== 'more')).toEqual(widths.slice(0, visibleCount).map((_, i) => i));
                expect(shown.includes('more')).toBe(visibleCount < count);
            }
        }
    });
});
