// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {PluginConfig} from '@/components/system_console/plugin_config_types';

import {withSavedMCPServer, withoutMCPServer} from './mcp_server_persistence';
import {defaultConfig} from './settings_sections';

describe('withSavedMCPServer', () => {
    const server = (id: string, policy: 'ask' | 'auto_run_in_dm' = 'ask', baseURL = `https://${id}.example.com`) => ({
        id,
        name: id,
        enabled: true,
        baseURL,
        headers: {},
        tool_configs: [{name: 'tool', policy, enabled: true}],
    });
    const pageWith = (...servers: ReturnType<typeof server>[]) => ({mcp: {servers}} as unknown as PluginConfig);
    const merge = (prev: PluginConfig, saved: ReturnType<typeof server>, scope: 'server' | 'tool_configs' | 'settings') => withSavedMCPServer(prev, saved, scope, defaultConfig.mcp);

    test.each([
        {name: 'a new server is added to the page', prev: pageWith(server('a')), saved: server('b'), scope: 'server' as const, want: ['a', 'b']},
        {name: 'a review updates the tool policies of a server on the page', prev: pageWith(server('a')), saved: server('a', 'auto_run_in_dm'), scope: 'tool_configs' as const, want: ['a']},
        {name: 'a review does not bring back a server removed from the page', prev: pageWith(server('a')), saved: server('b'), scope: 'tool_configs' as const, want: ['a']},
        {name: 'an edit does not bring back a server removed from the page', prev: pageWith(server('a')), saved: server('b'), scope: 'settings' as const, want: ['a']},
    ])('$name', ({prev, saved, scope, want}) => {
        const next = merge(prev, saved, scope);
        expect((next.mcp.servers ?? []).map((s) => s.id)).toEqual(want);
        if (scope === 'tool_configs' && want.includes(saved.id)) {
            expect(next.mcp.servers?.find((s) => s.id === saved.id)?.tool_configs).toEqual(saved.tool_configs);
        }
    });

    test('an edit keeps tool policy changes still pending on the page', () => {
        const pending = server('a', 'auto_run_in_dm');
        const next = merge(pageWith(pending), {...server('a'), description: 'edited'} as ReturnType<typeof server>, 'settings');
        expect(next.mcp.servers?.[0]).toEqual(expect.objectContaining({description: 'edited', tool_configs: pending.tool_configs}));
    });

    test('an edit that moves the server to another URL drops its tool policies', () => {
        const moved = {...server('a', 'ask', 'https://moved.example.com'), tool_configs: []};
        const next = merge(pageWith(server('a', 'auto_run_in_dm')), moved, 'settings');
        expect(next.mcp.servers?.[0].tool_configs).toEqual([]);
    });

    test('an edit replaces the server on the page with the saved one', () => {
        const next = merge(pageWith(server('a')), server('a', 'ask', 'https://moved.example.com'), 'settings');
        expect(next.mcp.servers?.[0].baseURL).toBe('https://moved.example.com');
    });

    test('withoutMCPServer removes only that server from the page', () => {
        const next = withoutMCPServer(pageWith(server('a'), server('b')), 'a', defaultConfig.mcp);
        expect((next.mcp.servers ?? []).map((s) => s.id)).toEqual(['b']);
    });
});
