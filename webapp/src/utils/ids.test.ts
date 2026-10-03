// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {isValidId} from './ids';

// Mattermost IDs are 26 characters of lowercase letters and digits.
const WELL_FORMED_ID = 'c7f2m9xq4v1b8n3k6t5w0hzjd2';

describe('isValidId', () => {
    test.each([
        {name: 'lowercase letters and digits', id: WELL_FORMED_ID},
        {name: 'all digits', id: '01234567890123456789012345'},
        {name: 'all letters', id: 'abcdefghijklmnopqrstuvwxyz'},
    ])('accepts a well-formed id: $name', ({id}) => {
        expect(isValidId(id)).toBe(true);
    });

    test.each([
        {name: 'empty', id: ''},
        {name: 'too short', id: 'abc'},
        {name: 'too long', id: 'abcdefghijklmnopqrstuvwxyz7'},
        {name: 'uppercase', id: 'ABCDEFGHIJKLMNOPQRSTUVWXYZ'},
        {name: 'non-ascii', id: 'abcdefghijklmnopqrstuvwxy\u00e9'},
        {name: 'contains a separator', id: 'abcdefghijklmnopqrstuvwxy/'},
        {name: 'relative path segments', id: '../../some/other/route'},
        {name: 'leading whitespace', id: ` ${WELL_FORMED_ID}`},
        {name: 'trailing newline', id: `${WELL_FORMED_ID}\n`},
    ])('rejects a malformed id: $name', ({id}) => {
        expect(isValidId(id)).toBe(false);
    });

    // Post props are decoded from JSON off the websocket, so callers can hand
    // this helper any JSON value regardless of what the TypeScript type says.
    // Anything that is not a string is not an id.
    test.each([
        {name: 'a number', value: 1234567890},
        {name: 'a boolean', value: true},
        {name: 'null', value: null},
        {name: 'a missing value', value: void 0}, // eslint-disable-line no-void
        {name: 'an object', value: {}},
        {name: 'an empty array', value: []},
        {name: 'an array wrapping a well-formed id', value: [WELL_FORMED_ID]},
    ])('rejects a value that is not a string: $name', ({value}) => {
        expect(isValidId(value)).toBe(false);
    });
});
