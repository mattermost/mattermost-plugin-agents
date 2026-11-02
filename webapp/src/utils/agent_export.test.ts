// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {createIntl, type IntlShape} from 'react-intl';

import {getAgentVersion, getAgentVersions, type UserMCPServerInfo} from '@/client';
import {AgentVersionDetail, AgentVersionList} from '@/types/agents';

import {buildAgentExport, downloadAgentExport, matchImportedMCPTools, readAgentExportFile} from './agent_export';

jest.mock('@/client', () => ({
    getAgentVersions: jest.fn(),
    getAgentVersion: jest.fn(),
}));

const mockGetAgentVersions = getAgentVersions as jest.MockedFunction<typeof getAgentVersions>;
const mockGetAgentVersion = getAgentVersion as jest.MockedFunction<typeof getAgentVersion>;

// Message ids are derived from defaultMessage at build time; mirror that here.
const realIntl = createIntl({locale: 'en', defaultLocale: 'en', onError: () => null});
const intl = {
    formatMessage: (descriptor: {id?: string; defaultMessage: string}, values?: Record<string, unknown>) =>
        realIntl.formatMessage({id: descriptor.id ?? descriptor.defaultMessage, ...descriptor}, values as never),
} as unknown as IntlShape;

function agentFile(content: unknown, name = 'agent.json'): File {
    return new File([typeof content === 'string' ? content : JSON.stringify(content)], name, {type: 'application/json'});
}

const validDocument = {
    kind: 'mattermost-agent',
    schemaVersion: 1,
    exportedAt: 10,
    agentVersion: 3,
    agent: {
        name: 'helper',
        displayName: 'Helper',
        customInstructions: 'Be brief.',
        disableTools: false,
        maxToolTurns: 12,
        mcpDynamicToolLoading: false,
        autoEnableNewMCPTools: false,
        mcpTools: [{serverOrigin: 'https://jira.example.com/mcp', toolName: 'search'}],
    },
};

describe('buildAgentExport', () => {
    test('keeps only transferable fields and normalizes MCP origins', () => {
        const doc = buildAgentExport({
            name: 'helper',
            displayName: 'Helper',
            customInstructions: 'Be brief.',
            serviceID: 'svc-secret',
            model: 'gpt-x',
            adminUserIDs: ['u1'],
            maxToolTurns: 12,
            mcpDynamicToolLoading: false,
            enabledMCPTools: [
                {server_origin: 'https://jira.example.com/mcp/', tool_name: 'search'},
                {server_origin: 'https://jira.example.com/mcp', tool_name: ' search '},
            ],
        }, 3, 10);

        expect(doc).toEqual(validDocument);
        expect(JSON.stringify(doc)).not.toContain('svc-secret');
    });
});

describe('downloadAgentExport', () => {
    let clickedDownloads: string[];

    beforeEach(() => {
        jest.useFakeTimers();
        Object.assign(URL, {createObjectURL: jest.fn(() => 'blob:agent-export'), revokeObjectURL: jest.fn()});
        clickedDownloads = [];
        jest.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function click(this: HTMLAnchorElement) {
            clickedDownloads.push(this.download);
        });
    });

    afterEach(() => {
        jest.useRealTimers();
        jest.restoreAllMocks();
    });

    test('exports the current version under its number', async () => {
        mockGetAgentVersions.mockResolvedValue({currentVersion: 7, versions: [], hasMore: true} as AgentVersionList);
        mockGetAgentVersion.mockResolvedValue({version: 7, config: {name: 'release-helper'}} as AgentVersionDetail);

        await downloadAgentExport('agent_1');

        expect(mockGetAgentVersions).toHaveBeenCalledWith('agent_1', {limit: 1});
        expect(mockGetAgentVersion).toHaveBeenCalledWith('agent_1', 7);
        expect(clickedDownloads).toEqual(['release-helper-v7.agent.json']);
        expect(document.querySelector('a[download]')).toBeNull();
        jest.runAllTimers();
        expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:agent-export');
    });

    test.each([
        {name: 'the version request fails', setup: () => mockGetAgentVersions.mockRejectedValue(new Error('forbidden'))},
        {name: 'the agent has no version', setup: () => mockGetAgentVersions.mockResolvedValue({currentVersion: 0, versions: [], hasMore: false})},
    ])('does not download when $name', async ({setup}) => {
        setup();
        await expect(downloadAgentExport('agent_1')).rejects.toThrow();
        expect(clickedDownloads).toHaveLength(0);
    });
});

describe('readAgentExportFile', () => {
    test('reads a valid export', async () => {
        const doc = await readAgentExportFile(agentFile(validDocument), intl);
        expect(doc).toEqual(validDocument);
    });

    test('defaults fields that older files omit', async () => {
        const doc = await readAgentExportFile(agentFile({kind: 'mattermost-agent', schemaVersion: 1, agent: {customInstructions: 'Hi'}}), intl);
        expect(doc.agent).toEqual({
            name: '',
            displayName: '',
            customInstructions: 'Hi',
            disableTools: false,
            maxToolTurns: 0,
            mcpDynamicToolLoading: true,
            autoEnableNewMCPTools: false,
            mcpTools: [],
        });
    });

    test.each([
        {name: 'not JSON', content: 'not json', message: 'not a valid Mattermost agent export'},
        {name: 'another kind of file', content: {...validDocument, kind: 'something-else'}, message: 'not a valid Mattermost agent export'},
        {name: 'a newer format', content: {...validDocument, schemaVersion: 2}, message: 'format version 2'},
        {name: 'a missing agent section', content: {kind: 'mattermost-agent', schemaVersion: 1}, message: 'not a valid Mattermost agent export'},
        {name: 'a wrongly typed field', content: {...validDocument, agent: {...validDocument.agent, disableTools: 'yes'}}, message: 'not a valid Mattermost agent export'},
        {name: 'a tool without an origin', content: {...validDocument, agent: {...validDocument.agent, mcpTools: [{serverOrigin: ' ', toolName: 'x'}]}}, message: 'not a valid Mattermost agent export'},
    ])('rejects $name', async ({content, message}) => {
        await expect(readAgentExportFile(agentFile(content), intl)).rejects.toThrow(message);
    });

    test('rejects oversized files without reading them', async () => {
        const file = agentFile(validDocument);
        Object.defineProperty(file, 'size', {value: 3 * 1024 * 1024});
        const readAsText = jest.spyOn(FileReader.prototype, 'readAsText');
        await expect(readAgentExportFile(file, intl)).rejects.toThrow('too large');
        expect(readAsText).not.toHaveBeenCalled();
    });
});

describe('matchImportedMCPTools', () => {
    const server = (serverOrigin: string) => ({serverOrigin, name: serverOrigin, tools: []} as unknown as UserMCPServerInfo);

    test('maps tools to the configured origin and reports missing servers', () => {
        const result = matchImportedMCPTools([
            {serverOrigin: 'https://jira.example.com/mcp', toolName: 'search'},
            {serverOrigin: 'https://jira.example.com/mcp', toolName: 'create'},
            {serverOrigin: 'https://linear.example.com', toolName: 'issues'},
            {serverOrigin: 'https://linear.example.com', toolName: 'teams'},
        ], [server('https://jira.example.com/mcp/'), server('embedded://mattermost')]);

        expect(result).toEqual({
            enabledTools: [
                {server_origin: 'https://jira.example.com/mcp/', tool_name: 'search'},
                {server_origin: 'https://jira.example.com/mcp/', tool_name: 'create'},
            ],
            missingServerOrigins: ['https://linear.example.com'],
        });
    });
});
