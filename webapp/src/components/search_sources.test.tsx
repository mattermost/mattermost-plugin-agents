// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {MAX_SEARCH_SOURCES, parseSearchSources} from './search_sources';

const VALID_ID = 'c7f2m9xq4v1b8n3k6t5w0hzjd2';

function source(index: number) {
    return {
        postId: String(index).padStart(26, '0'),
        channelId: VALID_ID,
        userId: VALID_ID,
        content: `source ${index}`,
        score: 0.5,
    };
}

describe('parseSearchSources', () => {
    test.each([
        {name: 'null', raw: null},
        {name: 'non-string', raw: 42},
        {name: 'invalid JSON', raw: '{'},
        {name: 'non-array JSON', raw: '{}'},
    ])('returns no sources for $name input', ({raw}) => {
        expect(parseSearchSources(raw)).toEqual([]);
    });

    test('drops malformed ids and normalizes display fields', () => {
        const parsed = parseSearchSources(JSON.stringify([
            {...source(1), content: 42, score: Number.NaN},
            {...source(2), postId: '../invalid'},
        ]));

        expect(parsed).toEqual([{
            ...source(1),
            content: '',
            score: 0,
        }]);
    });

    test('limits sources to the server maximum', () => {
        const raw = JSON.stringify(
            Array.from({length: MAX_SEARCH_SOURCES + 25}, (_, index) => source(index)),
        );

        expect(parseSearchSources(raw)).toHaveLength(MAX_SEARCH_SOURCES);
    });
});
