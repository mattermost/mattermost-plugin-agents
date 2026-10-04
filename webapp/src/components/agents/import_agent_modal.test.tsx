// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor, within} from '@testing-library/react';

import {importAgent, previewAgentImport} from '@/client';
import {AgentExportDocument, AgentImportPreview, ServiceInfo, UserAgent} from '@/types/agents';

import ImportAgentModal from './import_agent_modal';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');

    // Real ICU formatting (plurals, values) with ids derived from defaultMessage, as the babel plugin does at build time.
    const real = actual.createIntl({locale: 'en', defaultLocale: 'en', onError: () => null});
    const intl = {
        formatMessage: (descriptor: {id?: string; defaultMessage: string}, values?: Record<string, string | number>) =>
            real.formatMessage({id: descriptor.id ?? descriptor.defaultMessage, ...descriptor}, values),
    };
    return {
        ...actual,
        useIntl: () => intl,
        FormattedMessage: ({defaultMessage, values}: {defaultMessage: string; values?: Record<string, string | number>}) =>
            real.formatMessage({id: defaultMessage, defaultMessage}, values),
    };
});

jest.mock('@/client', () => ({
    importAgent: jest.fn(),
    previewAgentImport: jest.fn(),
}));

const mockPreview = previewAgentImport as jest.MockedFunction<typeof previewAgentImport>;
const mockImport = importAgent as jest.MockedFunction<typeof importAgent>;

const services: ServiceInfo[] = [
    {id: 'svc_1', name: 'First Service', type: 'openai', defaultModel: 'gpt-4.1', outputTokenLimit: 0, useResponsesAPI: false},
    {id: 'svc_2', name: 'Second Service', type: 'anthropic', defaultModel: 'claude', outputTokenLimit: 0, useResponsesAPI: false},
];

const manageable = [
    {id: 'agent_1', name: 'release-helper', displayName: 'Release Helper'},
    {id: 'agent_2', name: 'other', displayName: 'Other Agent'},
] as UserAgent[];

function makeDocument(overrides: Partial<AgentExportDocument['agent']> = {}): AgentExportDocument {
    return {
        kind: 'mattermost-agent',
        schemaVersion: 1,
        exportedAt: 1759600000000,
        agentVersion: 7,
        agent: {
            name: 'release-helper',
            displayName: 'Release Helper',
            customInstructions: 'Help with releases.',
            disableTools: false,
            maxToolTurns: 30,
            mcpDynamicToolLoading: true,
            autoEnableNewMCPTools: false,
            mcpTools: [
                {serverOrigin: 'https://mcp.example.com/mcp', serverName: 'Jira', toolName: 'create_issue'},
                {serverOrigin: 'https://old.example.com/mcp', serverName: 'Old', toolName: 'lookup'},
            ],
            ...overrides,
        },
    };
}

function makePreview(overrides: Partial<AgentImportPreview> = {}, agentOverrides: Partial<AgentExportDocument['agent']> = {}): AgentImportPreview {
    return {
        document: makeDocument(agentOverrides),
        mcpServers: [
            {sourceOrigin: 'https://mcp.example.com/mcp', sourceName: 'Jira', toolNames: ['create_issue'], autoTargetOrigin: 'https://mcp.example.com/mcp'},
            {sourceOrigin: 'https://old.example.com/mcp', sourceName: 'Old', toolNames: ['lookup'], autoTargetOrigin: ''},
        ],
        availableMCPServers: [
            {origin: 'https://mcp.example.com/mcp', name: 'Jira'},
            {origin: 'embedded://mattermost', name: 'Mattermost'},
        ],
        existingAgent: null,
        ...overrides,
    };
}

type RenderOptions = {
    canCreate?: boolean;
    createDisabledReason?: string;
    manageableAgents?: UserAgent[];
}

function renderModal(options: RenderOptions = {}) {
    const onClose = jest.fn();
    const onImported = jest.fn();
    render(
        <ImportAgentModal
            services={services}
            manageableAgents={options.manageableAgents ?? manageable}
            canCreate={options.canCreate ?? true}
            createDisabledReason={options.createDisabledReason ?? ''}
            onClose={onClose}
            onImported={onImported}
        />,
    );
    return {onClose, onImported};
}

function chooseFile(content: string) {
    const input = screen.getByTestId('import-agent-file-input');
    const file = new File([content], 'agent.json', {type: 'application/json'});
    fireEvent.change(input, {target: {files: [file]}});
}

async function loadPreview(preview: AgentImportPreview) {
    mockPreview.mockResolvedValueOnce(preview);
    chooseFile(JSON.stringify(preview.document));
    await screen.findByTestId('import-summary');
}

function importButton() {
    return screen.getByRole('button', {name: 'Import'}) as HTMLButtonElement;
}

function mappingSelect(name: string) {
    return screen.getByLabelText(name) as HTMLSelectElement;
}

beforeEach(() => {
    jest.clearAllMocks();
});

describe('ImportAgentModal', () => {
    test('shows an inline error for a file that is not JSON and does not call the server', async () => {
        renderModal();

        chooseFile('{not json');

        expect(await screen.findByText('This file is not valid JSON.')).not.toBeNull();
        expect(mockPreview).not.toHaveBeenCalled();
        expect(importButton().disabled).toBe(true);
    });

    test('shows the server error from the preview inline', async () => {
        mockPreview.mockRejectedValueOnce({message: 'Unsupported schemaVersion 9'});
        renderModal();

        chooseFile(JSON.stringify({kind: 'mattermost-agent', schemaVersion: 9}));

        expect(await screen.findByText('Unsupported schemaVersion 9')).not.toBeNull();
        expect(importButton().disabled).toBe(true);
    });

    test('summarizes the document after a successful preview', async () => {
        renderModal();

        await loadPreview(makePreview());

        const summary = screen.getByTestId('import-summary');
        expect(within(summary).getByText('Release Helper')).not.toBeNull();
        expect(within(summary).getByText('@release-helper')).not.toBeNull();
        expect(within(summary).getByText('Exported from version 7')).not.toBeNull();
        expect(within(summary).getByText('Custom instructions: 19 characters')).not.toBeNull();
        expect(within(summary).getByText('MCP tools: 2 tools')).not.toBeNull();
    });

    test('preselects auto-matched MCP servers and keeps Import disabled until every row is resolved', async () => {
        const {onImported} = renderModal();
        mockImport.mockResolvedValue({id: 'new', name: 'release-helper', displayName: 'Release Helper'} as UserAgent);

        await loadPreview(makePreview());

        expect(mappingSelect('Jira').value).toBe('https://mcp.example.com/mcp');
        expect(mappingSelect('Old').value).toBe('');
        expect(importButton().disabled).toBe(true);

        fireEvent.change(mappingSelect('Old'), {target: {value: 'embedded://mattermost'}});
        expect(importButton().disabled).toBe(false);

        fireEvent.click(importButton());

        await waitFor(() => expect(onImported).toHaveBeenCalledTimes(1));
        expect(mockImport).toHaveBeenCalledWith({
            document: makePreview().document,
            mode: 'create',
            username: 'release-helper',
            displayName: 'Release Helper',
            serviceID: 'svc_1',
            model: '',
            mcpServerMappings: [
                {sourceOrigin: 'https://mcp.example.com/mcp', targetOrigin: 'https://mcp.example.com/mcp'},
                {sourceOrigin: 'https://old.example.com/mcp', targetOrigin: 'embedded://mattermost'},
            ],
        });
        expect(onImported).toHaveBeenCalledWith({
            agent: expect.objectContaining({id: 'new'}),
            mode: 'create',
        });
    });

    test('"Remove these tools" resolves a row and is sent as an empty target origin', async () => {
        mockImport.mockResolvedValue({id: 'new'} as UserAgent);
        renderModal();
        await loadPreview(makePreview());

        fireEvent.change(mappingSelect('Old'), {target: {value: '__remove__'}});
        fireEvent.change(screen.getByLabelText('AI service'), {target: {value: 'svc_2'}});
        fireEvent.change(screen.getByLabelText('Model (optional)'), {target: {value: ' gpt-x '}});
        fireEvent.change(screen.getByLabelText('Display name'), {target: {value: 'Renamed Helper'}});
        fireEvent.change(screen.getByLabelText('Username'), {target: {value: 'renamed-helper'}});
        fireEvent.click(importButton());

        await waitFor(() => expect(mockImport).toHaveBeenCalledTimes(1));
        expect(mockImport.mock.calls[0][0]).toEqual(expect.objectContaining({
            mode: 'create',
            username: 'renamed-helper',
            displayName: 'Renamed Helper',
            serviceID: 'svc_2',
            model: 'gpt-x',
            mcpServerMappings: [
                {sourceOrigin: 'https://mcp.example.com/mcp', targetOrigin: 'https://mcp.example.com/mcp'},
                {sourceOrigin: 'https://old.example.com/mcp', targetOrigin: ''},
            ],
        }));
    });

    test('hides the mapping table and sends no mappings when all MCP tools are auto-enabled', async () => {
        mockImport.mockResolvedValue({id: 'new'} as UserAgent);
        renderModal();

        await loadPreview(makePreview({mcpServers: []}, {autoEnableNewMCPTools: true, mcpTools: []}));

        expect(screen.queryByTestId('import-mcp-mappings')).toBeNull();
        expect(screen.getByText('MCP tools: all MCP tools')).not.toBeNull();
        expect(importButton().disabled).toBe(false);

        fireEvent.click(importButton());
        await waitFor(() => expect(mockImport).toHaveBeenCalledTimes(1));
        expect(mockImport.mock.calls[0][0].mcpServerMappings).toEqual([]);
    });

    test('validates the username with the editor rule before enabling Import', async () => {
        renderModal();
        await loadPreview(makePreview({mcpServers: []}, {autoEnableNewMCPTools: true}));

        fireEvent.change(screen.getByLabelText('Username'), {target: {value: 'Bad Name'}});

        expect(screen.getByText(/Username must start with a letter/)).not.toBeNull();
        expect(importButton().disabled).toBe(true);

        fireEvent.change(screen.getByLabelText('Username'), {target: {value: 'good_name'}});
        expect(importButton().disabled).toBe(false);
    });

    test('defaults to update mode with the existing manageable agent preselected', async () => {
        mockImport.mockResolvedValue({id: 'agent_1', name: 'release-helper', displayName: 'Release Helper'} as UserAgent);
        const {onImported} = renderModal();

        await loadPreview(makePreview({
            existingAgent: {id: 'agent_1', displayName: 'Release Helper', username: 'release-helper', canManage: true},
            mcpServers: [{sourceOrigin: 'https://mcp.example.com/mcp', sourceName: 'Jira', toolNames: ['create_issue'], autoTargetOrigin: 'https://mcp.example.com/mcp'}],
        }));

        expect((screen.getByLabelText('Update an existing agent') as HTMLInputElement).checked).toBe(true);
        expect((screen.getByLabelText('Agent to update') as HTMLSelectElement).value).toBe('agent_1');
        expect(screen.queryByLabelText('Username')).toBeNull();
        expect(importButton().disabled).toBe(false);

        fireEvent.change(screen.getByLabelText('Agent to update'), {target: {value: 'agent_2'}});
        fireEvent.click(importButton());

        await waitFor(() => expect(onImported).toHaveBeenCalledTimes(1));
        expect(mockImport).toHaveBeenCalledWith({
            document: expect.objectContaining({kind: 'mattermost-agent'}),
            mode: 'update',
            agentID: 'agent_2',
            mcpServerMappings: [
                {sourceOrigin: 'https://mcp.example.com/mcp', targetOrigin: 'https://mcp.example.com/mcp'},
            ],
        });
        expect(mockImport.mock.calls[0][0]).not.toHaveProperty('username');
        expect(mockImport.mock.calls[0][0]).not.toHaveProperty('serviceID');
        expect(onImported).toHaveBeenCalledWith(expect.objectContaining({mode: 'update'}));
    });

    test('does not preselect an existing agent the user cannot manage', async () => {
        renderModal();

        await loadPreview(makePreview({
            existingAgent: {id: 'agent_9', displayName: 'Theirs', username: 'release-helper', canManage: false},
            mcpServers: [],
        }, {autoEnableNewMCPTools: true}));

        expect((screen.getByLabelText('Create a new agent') as HTMLInputElement).checked).toBe(true);
    });

    test('create mode is unavailable when the user cannot create agents', async () => {
        renderModal({canCreate: false});

        await loadPreview(makePreview({mcpServers: []}, {autoEnableNewMCPTools: true}));

        expect((screen.getByLabelText('Create a new agent') as HTMLInputElement).disabled).toBe(true);
        expect((screen.getByLabelText('Update an existing agent') as HTMLInputElement).checked).toBe(true);
    });

    test('create mode is unavailable and explained when the agent quota is reached', async () => {
        renderModal({createDisabledReason: 'Your current plan allows 1 agent.'});

        await loadPreview(makePreview({mcpServers: []}, {autoEnableNewMCPTools: true}));

        expect((screen.getByLabelText('Create a new agent') as HTMLInputElement).disabled).toBe(true);
        expect(screen.getByText('Your current plan allows 1 agent.')).not.toBeNull();
    });

    test('shows the server error inline and stays open when the import fails', async () => {
        mockImport.mockRejectedValueOnce({message: 'username "release-helper" is already taken'});
        const {onClose, onImported} = renderModal();
        await loadPreview(makePreview({mcpServers: []}, {autoEnableNewMCPTools: true}));

        fireEvent.click(importButton());

        expect(await screen.findByText('username "release-helper" is already taken')).not.toBeNull();
        expect(onImported).not.toHaveBeenCalled();
        expect(onClose).not.toHaveBeenCalled();
        expect(importButton().disabled).toBe(false);
    });

    test('Cancel closes the modal', () => {
        const {onClose} = renderModal();

        fireEvent.click(screen.getByRole('button', {name: 'Cancel'}));

        expect(onClose).toHaveBeenCalledTimes(1);
    });
});
