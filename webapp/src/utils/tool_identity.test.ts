// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {ToolCall, ToolCallStatus} from '@/components/tool_types';

import {
    EmbeddedServerOrigin,
    bareToolName,
    canonicalToolKey,
    originKind,
    toolDisplayName,
} from './tool_identity';

function makeTool(overrides: Partial<ToolCall> = {}): ToolCall {
    return {
        id: 'tc_1',
        name: 'tool',
        description: '',
        status: ToolCallStatus.Pending,
        ...overrides,
    };
}

describe('originKind', () => {
    test.each([
        [undefined, 'builtin'],
        ['', 'builtin'],
        ['embedded://mattermost', 'embedded'],
        ['plugin://com.example.plugin', 'plugin'],
        ['https://mcp.atlassian.com', 'external'],
        ['http://localhost:9000/mcp', 'external'],
    ] as const)('classifies %s as %s', (origin, expected) => {
        expect(originKind(origin)).toBe(expected);
    });

    test('EmbeddedServerOrigin constant matches the server key', () => {
        expect(EmbeddedServerOrigin).toBe('embedded://mattermost');
    });
});

describe('bareToolName', () => {
    test('prefers mcp_bare_name when present', () => {
        expect(bareToolName(makeTool({name: 'mattermost__create_post', mcp_bare_name: 'create_post'}))).toBe('create_post');
    });

    test('falls back to stripWirePrefix heuristic when mcp_bare_name is absent', () => {
        expect(bareToolName(makeTool({name: 'mattermost__create_post'}))).toBe('create_post');
    });

    test('returns the name unchanged for a builtin tool with no prefix', () => {
        expect(bareToolName(makeTool({name: 'WebSearch'}))).toBe('WebSearch');
    });
});

describe('canonicalToolKey', () => {
    test('combines origin kind and bare name', () => {
        expect(canonicalToolKey(makeTool({
            name: 'mattermost__create_post',
            mcp_bare_name: 'create_post',
            server_origin: 'embedded://mattermost',
        }))).toBe('embedded:create_post');
    });

    test('builtin tools key on the builtin origin', () => {
        expect(canonicalToolKey(makeTool({name: 'WebSearch'}))).toBe('builtin:WebSearch');
    });

    test('external tools key on the external origin and bare name', () => {
        expect(canonicalToolKey(makeTool({
            name: 'jira__get_issue',
            mcp_bare_name: 'get_issue',
            server_origin: 'https://mcp.atlassian.com',
        }))).toBe('external:get_issue');
    });
});

describe('toolDisplayName', () => {
    test('uses the MCP-supplied title verbatim when present', () => {
        expect(toolDisplayName(makeTool({name: 'jira__get_issue', mcp_bare_name: 'get_issue', title: 'Get Jira Issue'}))).toBe('Get Jira Issue');
    });

    test('prettifies the bare name when no title (mcp_bare_name path)', () => {
        expect(toolDisplayName(makeTool({name: 'mattermost__get_channel_info', mcp_bare_name: 'get_channel_info'}))).toBe('Get Channel Info');
    });

    // Parity with the pre-title behavior: embedded/builtin names come from the
    // wire-prefix strip + title-case, so e2e assertions like 'Get Channel Info'
    // and 'Read Post' are unchanged.
    test('prettifies via stripWirePrefix for legacy data without mcp_bare_name', () => {
        expect(toolDisplayName(makeTool({name: 'mattermost__read_post'}))).toBe('Read Post');
        expect(toolDisplayName(makeTool({name: 'mattermost__get_channel_info'}))).toBe('Get Channel Info');
    });

    test('title takes precedence over the bare name', () => {
        expect(toolDisplayName(makeTool({name: 'x__y', mcp_bare_name: 'y', title: 'Custom Title'}))).toBe('Custom Title');
    });

    test('empty title falls back to prettified bare name', () => {
        expect(toolDisplayName(makeTool({name: 'search_posts', title: ''}))).toBe('Search Posts');
    });
});
