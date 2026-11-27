// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {filterMCPServers, SearchableMCPServer, SearchableMCPTool} from './mcp_tools_filter';

function tool(
    name: string,
    description: string,
    extra: Partial<SearchableMCPTool> = {},
): SearchableMCPTool {
    return {name, description, ...extra};
}

describe('filterMCPServers', () => {
    const servers: SearchableMCPServer[] = [
        {
            name: 'Mattermost',
            tools: [
                tool('read_post', 'Read a specific post and thread from Mattermost.'),
                tool('dm', 'Send a direct message.'),
            ],
        },
        {
            name: 'OtherCorp',
            tools: [
                tool('other_tool', 'Contains unrelated substring zap for testing.'),
                tool('disabled_tool', 'Admin disabled tool with uniqueDisabledToken.'),
            ],
        },
    ];

    test('empty and whitespace queries return all servers unfiltered', () => {
        for (const query of ['', '   \t']) {
            const result = filterMCPServers(servers, query);
            expect(result).toHaveLength(2);
            expect(result[0]).toEqual({server: servers[0], matchedTools: servers[0].tools, isFiltered: false});
            expect(result[1]).toEqual({server: servers[1], matchedTools: servers[1].tools, isFiltered: false});
        }
    });

    test('server name match returns all tools on that server with isFiltered false', () => {
        const result = filterMCPServers(servers, 'M');
        expect(result.map((r) => r.server.name)).toEqual(['Mattermost']);
        expect(result[0].matchedTools).toEqual(servers[0].tools);
        expect(result[0].isFiltered).toBe(false);
    });

    test('short queries (<3 chars) match tool names and displayNames only, not descriptions', () => {
        const list: SearchableMCPServer[] = [
            {
                name: 'Alpha',
                tools: [
                    tool('x', 'only the letters za appear in this long description'),
                    tool('com_example__ab_foo', 'no', {displayName: 'ab_foo'}),
                ],
            },
        ];

        expect(filterMCPServers(list, 'za')).toEqual([]);
        expect(filterMCPServers(list, ' ZA ')).toEqual([]);

        const matched = filterMCPServers(list, 'ab');
        expect(matched).toHaveLength(1);
        expect(matched[0].isFiltered).toBe(true);
        expect(matched[0].matchedTools.map((t) => t.name)).toEqual(['com_example__ab_foo']);
    });

    test('queries of 3+ chars match tool descriptions and narrow tools on that server', () => {
        const result = filterMCPServers(servers, 'Zap');
        expect(result).toHaveLength(1);
        expect(result[0].server.name).toBe('OtherCorp');
        expect(result[0].isFiltered).toBe(true);
        expect(result[0].matchedTools.map((t) => t.name)).toEqual(['other_tool']);
    });

    test('queries of 3+ chars match retrievalDescriptionOverride', () => {
        const list: SearchableMCPServer[] = [
            {
                name: 'Tracker',
                tools: [
                    tool('get_issue', 'Fetch ticket', {retrievalDescriptionOverride: 'Lookup incident escalation'}),
                    tool('create_issue', 'Create ticket'),
                ],
            },
        ];

        const result = filterMCPServers(list, 'escalation');
        expect(result).toHaveLength(1);
        expect(result[0].isFiltered).toBe(true);
        expect(result[0].matchedTools.map((t) => t.name)).toEqual(['get_issue']);
    });

    test('matches all visible tools including admin-disabled ones', () => {
        const result = filterMCPServers(servers, 'uniqueDisabledToken');
        expect(result).toHaveLength(1);
        expect(result[0].server.name).toBe('OtherCorp');
        expect(result[0].isFiltered).toBe(true);
        expect(result[0].matchedTools.map((t) => t.name)).toEqual(['disabled_tool']);
    });

    test('returns empty array when nothing matches', () => {
        expect(filterMCPServers(servers, 'qqq')).toEqual([]);
    });
});
