// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {webcrypto} from 'crypto';

import {createIntl, type IntlShape} from 'react-intl';

import {fetchAgentDocument, getAgentVersion, getAgentVersions, type UserMCPServerInfo} from '@/client';
import {AgentVersionDetail, AgentVersionList} from '@/types/agents';

import {buildAgentExport, downloadAgentExport, matchImportedMCPTools, readAgentExportFile} from './agent_export';

jest.mock('@/client', () => ({
    getAgentVersions: jest.fn(),
    getAgentVersion: jest.fn(),
    fetchAgentDocument: jest.fn(),
}));

const mockGetAgentVersions = getAgentVersions as jest.MockedFunction<typeof getAgentVersions>;
const mockGetAgentVersion = getAgentVersion as jest.MockedFunction<typeof getAgentVersion>;
const mockFetchAgentDocument = fetchAgentDocument as jest.MockedFunction<typeof fetchAgentDocument>;

// Message ids are derived from defaultMessage at build time; mirror that here.
const realIntl = createIntl({locale: 'en', defaultLocale: 'en', onError: () => null});
const intl = {
    formatMessage: (descriptor: {id?: string; defaultMessage: string}, values?: Record<string, unknown>) =>
        realIntl.formatMessage({id: descriptor.id ?? descriptor.defaultMessage, ...descriptor}, values as never),
} as unknown as IntlShape;

const skillID = 'a'.repeat(26);

function agentFile(content: unknown, name = 'agent.json'): File {
    return new File([typeof content === 'string' ? content : JSON.stringify(content)], name, {type: 'application/json'});
}

const validDocument = {
    kind: 'mattermost-agent',
    schemaVersion: 2,
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
        skills: {disabled: false, allowPersonal: true, enabledSharedIDs: [skillID], descriptionOverrides: {[skillID]: 'Use for releases'}},
        documents: [],
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
            skills: {disabled: false, allowPersonal: true, enabledSharedIDs: [skillID], descriptionOverrides: {[skillID]: 'Use for releases'}},
        }, 3, 10);

        expect(doc).toEqual(validDocument);
        expect(JSON.stringify(doc)).not.toContain('svc-secret');
    });

    test('exports default skill settings for snapshots saved before skills existed', () => {
        const doc = buildAgentExport({name: 'helper', skills: null}, 1, 10);
        expect(doc.agent.skills).toEqual({disabled: false, allowPersonal: false, enabledSharedIDs: [], descriptionOverrides: {}});
    });
});

const handbookBytes = new Uint8Array(Buffer.from('Refunds within 30 days.'));

async function sha256Hex(bytes: Uint8Array): Promise<string> {
    const digest = new Uint8Array(await webcrypto.subtle.digest('SHA-256', bytes as unknown as ArrayBuffer));
    return Array.from(digest, (b) => b.toString(16).padStart(2, '0')).join('');
}

async function exportedHandbook(overrides: Record<string, unknown> = {}) {
    return {
        name: 'handbook.txt',
        mimeType: 'text/plain',
        size: handbookBytes.length,
        sha256: await sha256Hex(handbookBytes),
        content: Buffer.from(handbookBytes).toString('base64'),
        ...overrides,
    };
}

function readBlobText(blob: Blob): Promise<string> {
    return new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(String(reader.result));
        reader.onerror = () => reject(reader.error);
        reader.readAsText(blob);
    });
}

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

    test('carries the current version\'s documents with their bytes', async () => {
        jest.useRealTimers();
        mockGetAgentVersions.mockResolvedValue({currentVersion: 2, versions: [], hasMore: false} as AgentVersionList);
        mockGetAgentVersion.mockResolvedValue({
            version: 2,
            config: {
                name: 'helper',
                documents: [{id: 'doc_1', name: 'handbook.txt', mimeType: 'text/plain', size: handbookBytes.length, sha256: await sha256Hex(handbookBytes), textRunes: 22}],
            },
        } as AgentVersionDetail);
        mockFetchAgentDocument.mockResolvedValue(new Blob([handbookBytes]));

        await downloadAgentExport('agent_1');

        expect(mockFetchAgentDocument).toHaveBeenCalledWith('agent_1', 'doc_1');
        const exported = JSON.parse(await readBlobText((URL.createObjectURL as jest.Mock).mock.calls[0][0]));
        expect(exported.schemaVersion).toBe(2);
        expect(exported.agent.documents).toEqual([await exportedHandbook()]);
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
            skills: {disabled: false, allowPersonal: false, enabledSharedIDs: [], descriptionOverrides: {}},
            documents: [],
        });
    });

    test('drops duplicate skill IDs and blank overrides', async () => {
        const skills = {enabledSharedIDs: [skillID, skillID], descriptionOverrides: {[skillID]: '  '}};
        const doc = await readAgentExportFile(agentFile({...validDocument, agent: {...validDocument.agent, skills}}), intl);
        expect(doc.agent.skills).toEqual({disabled: false, allowPersonal: false, enabledSharedIDs: [skillID], descriptionOverrides: {}});
    });

    test.each([
        {name: 'not JSON', content: 'not json', message: 'not a valid Mattermost agent export'},
        {name: 'another kind of file', content: {...validDocument, kind: 'something-else'}, message: 'not a valid Mattermost agent export'},
        {name: 'a newer format', content: {...validDocument, schemaVersion: 3}, message: 'format version 3'},
        {name: 'a missing agent section', content: {kind: 'mattermost-agent', schemaVersion: 1}, message: 'not a valid Mattermost agent export'},
        {name: 'a wrongly typed field', content: {...validDocument, agent: {...validDocument.agent, disableTools: 'yes'}}, message: 'not a valid Mattermost agent export'},
        {name: 'a malformed skill ID', content: {...validDocument, agent: {...validDocument.agent, skills: {enabledSharedIDs: ['not-an-id']}}}, message: 'not a valid Mattermost agent export'},
        {name: 'an override keyed by a malformed ID', content: {...validDocument, agent: {...validDocument.agent, skills: {descriptionOverrides: {bad: 'x'}}}}, message: 'not a valid Mattermost agent export'},
        {name: 'an override over the length limit', content: {...validDocument, agent: {...validDocument.agent, skills: {descriptionOverrides: {[skillID]: 'x'.repeat(1025)}}}}, message: 'not a valid Mattermost agent export'},
        {name: 'a wrongly typed skills flag', content: {...validDocument, agent: {...validDocument.agent, skills: {disabled: 'no'}}}, message: 'not a valid Mattermost agent export'},
        {name: 'a tool without an origin', content: {...validDocument, agent: {...validDocument.agent, mcpTools: [{serverOrigin: ' ', toolName: 'x'}]}}, message: 'not a valid Mattermost agent export'},
    ])('rejects $name', async ({content, message}) => {
        await expect(readAgentExportFile(agentFile(content), intl)).rejects.toThrow(message);
    });

    describe('documents', () => {
        beforeAll(() => {
            Object.defineProperty(globalThis, 'crypto', {value: webcrypto, configurable: true});
        });

        test('reads a version 2 file\'s documents', async () => {
            const documents = [await exportedHandbook()];
            const doc = await readAgentExportFile(agentFile({...validDocument, agent: {...validDocument.agent, documents}}), intl);
            expect(doc.agent.documents).toEqual(documents);
        });

        test('ignores the documents of a version 1 file', async () => {
            const documents = [await exportedHandbook()];
            const doc = await readAgentExportFile(agentFile({...validDocument, schemaVersion: 1, agent: {...validDocument.agent, documents}}), intl);
            expect(doc.schemaVersion).toBe(1);
            expect(doc.agent.documents).toEqual([]);
        });

        test.each([
            {name: 'content that does not match its hash', overrides: {sha256: 'f'.repeat(64)}},
            {name: 'content that does not match its size', overrides: {size: 5}},
            {name: 'content that is not base64', overrides: {content: '!!not base64!!'}},
            {name: 'a malformed hash', overrides: {sha256: 'abc'}},
            {name: 'a blank name', overrides: {name: '  '}},
            {name: 'a size over the file limit', overrides: {size: 11 * 1024 * 1024}},
        ])('rejects a document with $name', async ({overrides}) => {
            const documents = [await exportedHandbook(overrides)];
            await expect(readAgentExportFile(agentFile({...validDocument, agent: {...validDocument.agent, documents}}), intl)).rejects.toThrow('not a valid Mattermost agent export');
        });

        test('rejects more documents than an agent can have', async () => {
            const documents = Array.from({length: 21}, () => ({name: 'x.txt', mimeType: 'text/plain', size: 1, sha256: 'a'.repeat(64), content: 'eA=='}));
            await expect(readAgentExportFile(agentFile({...validDocument, agent: {...validDocument.agent, documents}}), intl)).rejects.toThrow('not a valid Mattermost agent export');
        });
    });

    test('rejects oversized files without reading them', async () => {
        const file = agentFile(validDocument);
        Object.defineProperty(file, 'size', {value: 41 * 1024 * 1024});
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
