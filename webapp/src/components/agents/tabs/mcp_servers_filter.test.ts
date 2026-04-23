// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {filterMcpsServersBySearchQuery, McpsSearchServerRow} from './mcp_servers_filter';

function tool(
    name: string,
    description: string,
    enabled = true,
): McpsSearchServerRow['tools'][number] {
    return {name, description, enabled};
}

describe('filterMcpsServersBySearchQuery', () => {
    const servers: McpsSearchServerRow[] = [
        {
            name: 'Mattermost',
            tools: [
                tool('read_post', 'Read a specific post and thread from Mattermost.', true),
                tool('dm', 'Send a direct message.', true),
            ],
        },
        {
            name: 'OtherCorp',
            tools: [
                tool('other_tool', 'Contains unrelated substring zap for testing.', true),
            ],
        },
    ];

    test('empty query returns all servers', () => {
        expect(filterMcpsServersBySearchQuery(servers, '')).toEqual(servers);
    });

    test('whitespace-only query returns all servers', () => {
        expect(filterMcpsServersBySearchQuery(servers, '   \t')).toEqual(servers);
    });

    test('1-char query matching server name keeps server', () => {
        const result = filterMcpsServersBySearchQuery(servers, 'M');
        expect(result.map((s) => s.name)).toContain('Mattermost');
    });

    test('1-char query matching only disabled tool description does not surface server', () => {
        const disabledOnly: McpsSearchServerRow[] = [
            {
                name: 'HiddenMatch',
                tools: [
                    tool('disabled_tool', 'The word za appears only here in a long description.', false),
                ],
            },
        ];
        expect(filterMcpsServersBySearchQuery(disabledOnly, 'z')).toEqual([]);
    });

    test('2-char query matching only enabled tool description is ignored (no false positive)', () => {
        const withZap: McpsSearchServerRow[] = [
            {
                name: 'Alpha',
                tools: [tool('x', 'only the letters za appear in this long description', true)],
            },
        ];
        expect(filterMcpsServersBySearchQuery(withZap, 'za')).toEqual([]);
    });

    test('2-char query matching enabled tool name returns server', () => {
        const s: McpsSearchServerRow[] = [
            {name: 'S', tools: [tool('ab_foo', 'no', true)]},
        ];
        expect(filterMcpsServersBySearchQuery(s, 'ab')).toEqual(s);
    });

    test('3-char query matching only tool description returns server', () => {
        const s: McpsSearchServerRow[] = [
            {
                name: 'X',
                tools: [tool('t1', 'uniqueZapToken in the description', true)],
            },
        ];
        expect(filterMcpsServersBySearchQuery(s, 'Zap')).toEqual(s);
    });

    test('3-char query with no match returns empty', () => {
        expect(filterMcpsServersBySearchQuery(servers, 'qqq')).toEqual([]);
    });

    test('query match on disabled tool name does not show server', () => {
        const s: McpsSearchServerRow[] = [
            {
                name: 'ServerA',
                tools: [tool('secret_tool', 'y', false)],
            },
        ];
        expect(filterMcpsServersBySearchQuery(s, 'secret')).toEqual([]);
    });

    test('query match on disabled tool description with 3+ chars does not show server', () => {
        const s: McpsSearchServerRow[] = [
            {
                name: 'ServerB',
                tools: [tool('off', 'contains uniqueBlobToken only here', false)],
            },
        ];
        expect(filterMcpsServersBySearchQuery(s, 'Blob')).toEqual([]);
    });

    test('boundary: 2-char query uppercase trimmed still uses short-query rules', () => {
        const s: McpsSearchServerRow[] = [
            {
                name: 'NoSub',
                tools: [tool('ok', 'ZZ substring in description zz', true)],
            },
        ];
        expect(filterMcpsServersBySearchQuery(s, ' ZZ ')).toEqual([]);
    });

    test('boundary: exactly 3 chars enables description search', () => {
        const s: McpsSearchServerRow[] = [
            {
                name: 'NoSub',
                tools: [tool('ok', 'abc-only-in-description-field', true)],
            },
        ];
        expect(filterMcpsServersBySearchQuery(s, 'abc')).toEqual(s);
    });
});
