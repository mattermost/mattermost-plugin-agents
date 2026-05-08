// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {
    pluginIDFromServerOrigin,
    sanitizeForToolName,
    stripPluginPrefix,
    stripWirePrefix,
} from './tool_names';

// Mirrors external/pluginmcp/pluginmcp_test.go's TestSanitizeForToolName; keep TS and Go cases in sync.
describe('sanitizeForToolName', () => {
    test.each([
        ['', ''],
        ['com.mattermost.plugin-foo', 'com_mattermost_plugin-foo'],
        ['com.mattermost.plugin-mcp-demo', 'com_mattermost_plugin-mcp-demo'],
        ['mattermost-ai', 'mattermost-ai'],
        ['playbooks', 'playbooks'],
        ['ABC_123', 'ABC_123'],
        ['a b', 'a_b'],
        ['x/y/z', 'x_y_z'],
        ['com:mattermost', 'com_mattermost'],
        ['com@plugin', 'com_plugin'],
        ['com mattermost/@evil', 'com_mattermost__evil'],
        ['café', 'caf_'],
    ])('%j -> %j', (input, expected) => {
        expect(sanitizeForToolName(input)).toBe(expected);
    });

    test('idempotent', () => {
        const inputs = ['com.mattermost.plugin-foo', 'com mattermost/@evil', 'café'];
        for (const i of inputs) {
            const once = sanitizeForToolName(i);
            expect(sanitizeForToolName(once)).toBe(once);
        }
    });
});

describe('pluginIDFromServerOrigin', () => {
    test('parses plugin URL with path', () => {
        expect(pluginIDFromServerOrigin('plugin://com.mattermost.plugin-mcp-demo/mcp')).
            toBe('com.mattermost.plugin-mcp-demo');
    });

    test('parses plugin URL without path', () => {
        expect(pluginIDFromServerOrigin('plugin://com.example.plugin')).
            toBe('com.example.plugin');
    });

    test('returns "" for non-plugin URL', () => {
        expect(pluginIDFromServerOrigin('https://example.com/mcp')).toBe('');
        expect(pluginIDFromServerOrigin('embedded://mattermost')).toBe('');
        expect(pluginIDFromServerOrigin('')).toBe('');
    });
});

describe('stripPluginPrefix', () => {
    test('strips matching prefix', () => {
        expect(stripPluginPrefix('com_mattermost_plugin-mcp-demo__echo', 'com.mattermost.plugin-mcp-demo')).
            toBe('echo');
    });

    test('leaves name unchanged when prefix mismatches', () => {
        expect(stripPluginPrefix('com_mattermost_plugin-mcp-demo__echo', 'unrelated.plugin')).
            toBe('com_mattermost_plugin-mcp-demo__echo');
    });

    test('leaves name unchanged when pluginID empty', () => {
        expect(stripPluginPrefix('foo__bar', '')).toBe('foo__bar');
    });

    test('handles tools whose name itself contains __', () => {
        expect(stripPluginPrefix('com_mattermost_plugin-foo__do__thing', 'com.mattermost.plugin-foo')).
            toBe('do__thing');
    });
});

describe('stripWirePrefix', () => {
    test('strips wire prefix from plugin-style name', () => {
        expect(stripWirePrefix('com_mattermost_plugin-mcp-demo__echo')).toBe('echo');
    });

    test('leaves embedded MCP tool names unchanged', () => {
        for (const n of ['add_user_to_channel', 'create_channel', 'create_post', 'dm', 'read_channel']) {
            expect(stripWirePrefix(n)).toBe(n);
        }
    });

    test('leaves names with leading "__" unchanged (no prefix token)', () => {
        expect(stripWirePrefix('__weird')).toBe('__weird');
    });

    test('leaves names whose prefix token has invalid characters unchanged', () => {
        // "with space" doesn't match [a-zA-Z0-9_-]+, so no strip.
        expect(stripWirePrefix('with space__tool')).toBe('with space__tool');
    });
});
