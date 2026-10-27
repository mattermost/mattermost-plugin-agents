// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';
import {useSelector} from 'react-redux';

import {deleteAgent, getAgents, getServices} from '@/client';
import {useAgentLimit, useLicenseLevel} from '@/license';
import {userHasSystemPermission} from '@/utils/permissions';
import {downloadAgentExport, readAgentExportFile} from '@/utils/agent_export';
import {AgentExportDocument, UserAgent} from '@/types/agents';

import AgentsList from './agents_list';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');

    // Stable intl object so effects depending on `intl` don't refire every render.
    const real = actual.createIntl({locale: 'en', defaultLocale: 'en', onError: () => null});
    const intl = {
        formatMessage: (descriptor: {id?: string; defaultMessage: string}, values?: Record<string, string | number>) =>
            real.formatMessage({id: descriptor.id ?? descriptor.defaultMessage, ...descriptor}, values),
    };
    return {
        ...actual,
        useIntl: () => intl,
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('react-redux', () => ({
    useSelector: jest.fn(),
}));

// WithTooltip renders its title alongside children so tests can assert the tooltip text.
jest.mock('@mattermost/shared/components/tooltip', () => ({
    WithTooltip: ({children, title}: {children: React.ReactNode; title?: React.ReactNode}) => (
        <>
            {children}
            <div>{title}</div>
        </>
    ),
}));

jest.mock('@/license', () => ({
    LicenseLevel: {
        Unlicensed: 0,
        Professional: 1,
        Enterprise: 2,
        EnterpriseAdvanced: 3,
    },
    useAgentLimit: jest.fn(),
    useLicenseLevel: jest.fn(),
    useLicenseLevelName: jest.fn(() => (level: number) => {
        const names = ['Unlicensed', 'Professional', 'Enterprise', 'Enterprise Advanced'];
        return names[level] ?? 'Enterprise';
    }),
}));

jest.mock('@/client', () => ({
    getAgents: jest.fn(),
    getServices: jest.fn(),
    deleteAgent: jest.fn(),
}));

jest.mock('@/utils/permissions', () => ({
    userHasSystemPermission: jest.fn(),
}));

jest.mock('@/utils/agent_export', () => ({
    downloadAgentExport: jest.fn(),
    readAgentExportFile: jest.fn(),
}));

jest.mock('./agent_row', () => ({
    __esModule: true,
    default: ({agent, servicesLoaded, onDelete, onExport}: {agent: UserAgent; servicesLoaded: boolean; onDelete: (agent: UserAgent) => void; onExport: (agent: UserAgent) => void}) => (
        <div
            data-testid='agent-row'
            data-services-loaded={String(servicesLoaded)}
        >
            {agent.displayName}
            <button
                type='button'
                onClick={() => onDelete(agent)}
            >
                {`Delete ${agent.displayName}`}
            </button>
            <button
                type='button'
                onClick={() => onExport(agent)}
            >
                {`Export ${agent.displayName}`}
            </button>
        </div>
    ),
}));

jest.mock('./agent_config_view', () => ({
    __esModule: true,
    default: ({mode, importedDocument}: {mode: string; importedDocument?: AgentExportDocument}) => (
        <div
            data-testid='agent-config-view'
            data-mode={mode}
        >
            {importedDocument?.agent.customInstructions}
        </div>
    ),
}));

jest.mock('./delete_agent_dialog', () => ({
    __esModule: true,
    default: ({onConfirm, onCancel}: {onConfirm: () => void; onCancel: () => void}) => (
        <div data-testid='delete-agent-dialog'>
            <button
                type='button'
                onClick={onConfirm}
            >
                {'Confirm delete'}
            </button>
            <button
                type='button'
                onClick={onCancel}
            >
                {'Cancel delete'}
            </button>
        </div>
    ),
}));

const mockUseSelector = useSelector as unknown as jest.Mock;
const mockUseAgentLimit = useAgentLimit as unknown as jest.Mock;
const mockUseLicenseLevel = useLicenseLevel as unknown as jest.Mock;
const mockGetAgents = getAgents as unknown as jest.Mock;
const mockGetServices = getServices as unknown as jest.Mock;
const mockDeleteAgent = deleteAgent as unknown as jest.Mock;
const mockUserHasSystemPermission = userHasSystemPermission as unknown as jest.Mock;
const mockDownloadAgentExport = downloadAgentExport as unknown as jest.Mock;
const mockReadAgentExportFile = readAgentExportFile as unknown as jest.Mock;

const unlicensedQuotaMessage = 'Your current plan allows 1 agent. Additional agents are available on Professional plans and above.';
const professionalQuotaMessage = 'Your current plan allows 3 agents. Additional agents are available on Enterprise plans and above.';

function makeAgent(id: string): UserAgent {
    return {
        id,
        name: id,
        displayName: `Agent ${id}`,
        creatorID: 'user_1',
    } as UserAgent;
}

function renderList() {
    return render(<AgentsList onStartChat={jest.fn()}/>);
}

beforeEach(() => {
    jest.clearAllMocks();
    mockUseSelector.mockImplementation((selector) => selector({
        entities: {users: {currentUserId: 'user_1'}},
    }));

    // manage_own_agent grants create permission.
    mockUserHasSystemPermission.mockImplementation((_state, _userId, permission) => permission === 'manage_own_agent');
    mockGetServices.mockResolvedValue([]);
    mockUseAgentLimit.mockReturnValue(1);
    mockUseLicenseLevel.mockReturnValue(0);
});

describe('AgentsList create-button gating', () => {
    test('unlicensed with no agents enables Create button without a quota message', async () => {
        mockGetAgents.mockResolvedValue({agents: [], activeAgentCount: 0});

        renderList();

        const button = await screen.findByRole('button', {name: 'Create agent'}) as HTMLButtonElement;

        // The button renders disabled while agents load, so wait for the quota fetch to settle.
        await waitFor(() => expect(button.disabled).toBe(false));
        await waitFor(() => expect(screen.queryByText(unlicensedQuotaMessage)).toBeNull());
    });

    test('unlicensed at the one-agent cap disables Create and names the Professional plan', async () => {
        mockGetAgents.mockResolvedValue({agents: [makeAgent('a1')], activeAgentCount: 1});

        renderList();

        await screen.findByText('Agent a1');
        const button = screen.getByRole('button', {name: 'Create agent'});
        expect((button as HTMLButtonElement).disabled).toBe(true);
        expect(screen.getByText(unlicensedQuotaMessage)).not.toBeNull();
    });

    test('Professional at the three-agent cap disables Create and names the Enterprise plan', async () => {
        mockUseAgentLimit.mockReturnValue(3);
        mockUseLicenseLevel.mockReturnValue(1);
        mockGetAgents.mockResolvedValue({
            agents: [makeAgent('a1'), makeAgent('a2'), makeAgent('a3')],
            activeAgentCount: 3,
        });

        renderList();

        await screen.findByText('Agent a1');
        const button = screen.getByRole('button', {name: 'Create agent'});
        expect((button as HTMLButtonElement).disabled).toBe(true);
        expect(screen.getByText(professionalQuotaMessage)).not.toBeNull();
    });

    test('unlicensed disables Create when server quota is reached but list is empty', async () => {
        mockGetAgents.mockResolvedValue({agents: [], activeAgentCount: 1});

        renderList();

        await screen.findByText('Loading agents...').then(() => screen.findByText('No agents have been created yet.'));
        const button = screen.getByRole('button', {name: 'Create agent'});
        expect((button as HTMLButtonElement).disabled).toBe(true);
        expect(screen.getByText(unlicensedQuotaMessage)).not.toBeNull();
    });

    test('Create button stays disabled while agents are loading', () => {
        mockGetAgents.mockImplementation(() => new Promise(() => {
            // Never resolves: keep the component in its loading state.
        }));

        renderList();

        const button = screen.getByRole('button', {name: 'Create agent'});
        expect((button as HTMLButtonElement).disabled).toBe(true);
    });

    test('Enterprise license keeps Create button enabled regardless of agent count', async () => {
        mockUseAgentLimit.mockReturnValue(null);
        mockUseLicenseLevel.mockReturnValue(2);
        mockGetAgents.mockResolvedValue({agents: [makeAgent('a1'), makeAgent('a2')]});

        renderList();

        await screen.findByText('Agent a1');
        const button = screen.getByRole('button', {name: 'Create agent'});
        expect((button as HTMLButtonElement).disabled).toBe(false);
        expect(screen.queryByText(unlicensedQuotaMessage)).toBeNull();
        expect(screen.queryByText(professionalQuotaMessage)).toBeNull();
    });
});

describe('AgentsList services loading', () => {
    test('does not request services for users without agent-management permission', async () => {
        mockUserHasSystemPermission.mockReturnValue(false);
        mockGetAgents.mockResolvedValue({agents: [makeAgent('a1')], activeAgentCount: 1});

        renderList();

        await screen.findByText('Agent a1');
        expect(mockGetServices).not.toHaveBeenCalled();
        expect(screen.queryByText('Failed to load AI services. Using the last loaded list.')).toBeNull();

        // The row must be told the services list is unknown so it never renders
        // an inactive badge for a missing service for these users.
        expect(screen.getByTestId('agent-row').getAttribute('data-services-loaded')).toBe('false');
    });

    test('loads services and shows no warning for a permitted user', async () => {
        mockGetAgents.mockResolvedValue({agents: [makeAgent('a1')], activeAgentCount: 1});
        mockGetServices.mockResolvedValue([
            {id: 'svc-1', name: 'Svc', type: 'openai', defaultModel: 'gpt-4', outputTokenLimit: 0, useResponsesAPI: false},
        ]);

        renderList();

        await screen.findByText('Agent a1');
        await waitFor(() => expect(screen.getByTestId('agent-row').getAttribute('data-services-loaded')).toBe('true'));
        expect(screen.queryByText('Failed to load AI services. Using the last loaded list.')).toBeNull();
    });

    test('warns when a permitted user cannot load services', async () => {
        mockGetAgents.mockResolvedValue({agents: [makeAgent('a1')], activeAgentCount: 1});
        mockGetServices.mockRejectedValue(new Error('forbidden'));

        renderList();

        await screen.findByText('Failed to load AI services. Using the last loaded list.');
        expect(mockGetServices).toHaveBeenCalled();
    });
});

describe('AgentsList delete quota refresh', () => {
    async function deleteLastVisibleAgent() {
        fireEvent.click(screen.getByRole('button', {name: 'Delete Agent a1'}));
        fireEvent.click(screen.getByRole('button', {name: 'Confirm delete'}));
        await waitFor(() => expect(mockDeleteAgent).toHaveBeenCalledWith('a1'));
        await waitFor(() => expect(mockGetAgents).toHaveBeenCalledTimes(2));
    }

    test('refetches quota after deleting last visible agent and re-enables Create when server count is 0', async () => {
        mockGetAgents.
            mockResolvedValueOnce({agents: [makeAgent('a1')], activeAgentCount: 1}).
            mockResolvedValueOnce({agents: [], activeAgentCount: 0});
        mockDeleteAgent.mockImplementation(() => Promise.resolve());

        renderList();

        await screen.findByText('Agent a1');
        expect((screen.getByRole('button', {name: 'Create agent'}) as HTMLButtonElement).disabled).toBe(true);

        await deleteLastVisibleAgent();

        const button = screen.getByRole('button', {name: 'Create agent'});
        expect((button as HTMLButtonElement).disabled).toBe(false);
        expect(screen.queryByText(unlicensedQuotaMessage)).toBeNull();
    });

    test('refetches quota after delete and keeps Create disabled when invisible agents remain', async () => {
        mockGetAgents.
            mockResolvedValueOnce({agents: [makeAgent('a1')], activeAgentCount: 1}).
            mockResolvedValueOnce({agents: [], activeAgentCount: 1});
        mockDeleteAgent.mockImplementation(() => Promise.resolve());

        renderList();

        await screen.findByText('Agent a1');
        await deleteLastVisibleAgent();

        const button = screen.getByRole('button', {name: 'Create agent'});
        expect((button as HTMLButtonElement).disabled).toBe(true);
        expect(screen.getByText(unlicensedQuotaMessage)).not.toBeNull();
    });
});

describe('AgentsList import and export', () => {
    const importedDocument = {
        kind: 'mattermost-agent',
        schemaVersion: 1,
        exportedAt: 1,
        agentVersion: 2,
        agent: {name: 'helper', displayName: 'Helper', customInstructions: 'Imported instructions', disableTools: false, maxToolTurns: 10, mcpDynamicToolLoading: true, autoEnableNewMCPTools: true, mcpTools: []},
    } as AgentExportDocument;

    function pickFile() {
        const file = new File(['{}'], 'helper.agent.json', {type: 'application/json'});
        fireEvent.change(screen.getByTestId('agent-import-file-input'), {target: {files: [file]}});
        return file;
    }

    test('picking a file opens the create view with the imported settings', async () => {
        mockGetAgents.mockResolvedValue({agents: [], activeAgentCount: 0});
        mockReadAgentExportFile.mockResolvedValue(importedDocument);

        renderList();

        const button = await screen.findByRole('button', {name: 'Import'}) as HTMLButtonElement;
        await waitFor(() => expect(button.disabled).toBe(false));
        const file = pickFile();

        const view = await screen.findByTestId('agent-config-view');
        expect(view.getAttribute('data-mode')).toBe('create');
        expect(view.textContent).toBe('Imported instructions');
        expect(mockReadAgentExportFile).toHaveBeenCalledWith(file, expect.anything());
    });

    test('an unreadable file shows the error and stays on the list', async () => {
        mockGetAgents.mockResolvedValue({agents: [], activeAgentCount: 0});
        mockReadAgentExportFile.mockRejectedValue(new Error('This file is not a valid Mattermost agent export.'));

        renderList();

        await screen.findByRole('button', {name: 'Import'});
        pickFile();

        expect(await screen.findByText('This file is not a valid Mattermost agent export.')).not.toBeNull();
        expect(screen.queryByTestId('agent-config-view')).toBeNull();
    });

    test('hides Import from users who cannot create agents', async () => {
        mockUserHasSystemPermission.mockReturnValue(false);
        mockGetAgents.mockResolvedValue({agents: [{...makeAgent('a1'), creatorID: 'someone_else', adminUserIDs: ['user_1']}]});

        renderList();

        await screen.findByText('Agent a1');
        expect(screen.queryByRole('button', {name: 'Import'})).toBeNull();
    });

    test('disables Import when the agent limit is reached', async () => {
        mockGetAgents.mockResolvedValue({agents: [makeAgent('a1')], activeAgentCount: 1});

        renderList();

        await screen.findByText('Agent a1');
        expect((screen.getByRole('button', {name: 'Import'}) as HTMLButtonElement).disabled).toBe(true);
    });

    test('exports an agent from the list and reports failures', async () => {
        mockGetAgents.mockResolvedValue({agents: [makeAgent('a1')], activeAgentCount: 1});
        mockDownloadAgentExport.mockImplementationOnce(() => Promise.resolve());

        renderList();

        await screen.findByText('Agent a1');
        fireEvent.click(screen.getByRole('button', {name: 'Export Agent a1'}));
        await waitFor(() => expect(mockDownloadAgentExport).toHaveBeenCalledWith('a1'));

        mockDownloadAgentExport.mockRejectedValueOnce({message: 'Export is not allowed.'});
        fireEvent.click(screen.getByRole('button', {name: 'Export Agent a1'}));
        expect(await screen.findByText('Export is not allowed.')).not.toBeNull();
    });
});
