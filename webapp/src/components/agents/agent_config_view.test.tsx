// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor, waitForElementToBeRemoved, within} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {createAgent, updateAgent} from '@/client';
import {EnabledTool, ServiceInfo, UserAgent} from '@/types/agents';

import AgentConfigView, {AgentDraft} from './agent_config_view';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string | number>) => {
                if (!values) {
                    return defaultMessage;
                }
                return Object.entries(values).reduce(
                    (message, [key, value]) => message.replace(`{${key}}`, String(value)),
                    defaultMessage,
                );
            },
        }),
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('@/client', () => ({
    createAgent: jest.fn(),
    updateAgent: jest.fn(),
    uploadAgentAvatar: jest.fn(),
    getUserMCPTools: jest.fn(),
}));

jest.mock('@/client/access_control', () => ({
    getAgentAccessPolicy: jest.fn(),
    deleteAgentAccessPolicy: jest.fn(),
}));

// The view reads currentUserId / system-admin status from the host store and
// probes ABAC support; neither is under test here.
jest.mock('react-redux', () => ({
    useSelector: jest.fn(() => false),
}));

jest.mock('@/utils/access_control', () => ({
    useABACSupport: () => ({supported: false, checking: false}),
}));

jest.mock('@/hooks/use_mcp_connection_events', () => ({
    useMCPConnectionEvents: jest.fn(),
}));

jest.mock('@/components/system_console/bot', () => ({
    ChannelAccessLevel: {
        All: 0,
    },
    UserAccessLevel: {
        All: 0,
        AttributeBased: 4,
    },
}));

jest.mock('./tabs/config_tab', () => ({
    __esModule: true,
    default: ({draft, onChange, errors = {}}: {draft: AgentDraft; onChange: (updates: Partial<AgentDraft>) => void; errors?: Record<string, string>}) => (
        <>
            <input
                aria-label='Display Name'
                value={draft.displayName}
                onChange={(e) => onChange({displayName: e.target.value})}
            />
            <input
                aria-label='Username'
                value={draft.username}
                onChange={(e) => onChange({username: e.target.value})}
            />
            <input
                aria-label='Max tool turns'
                value={draft.maxToolTurns}
                onChange={(e) => onChange({maxToolTurns: Number(e.target.value)})}
            />
            {errors.maxToolTurns && <div>{errors.maxToolTurns}</div>}
            <button
                type='button'
                onClick={() => onChange({serviceId: 'svc_1'})}
            >
                {'Select service'}
            </button>
        </>
    ),
}));

jest.mock('./tabs/access_tab', () => ({
    __esModule: true,
    default: ({onChange}: {onChange: (updates: Partial<AgentDraft>) => void}) => (
        <button
            type='button'
            onClick={() => onChange({userAccessLevel: 0})}
        >
            {'Switch user access to everyone'}
        </button>
    ),
}));

jest.mock('./tabs/mcps_tab', () => ({
    __esModule: true,
    default: ({
        mcpDynamicToolLoading,
        onChange,
        onReconcileEnabledTools,
    }: {
        mcpDynamicToolLoading: boolean;
        onChange: (updates: Partial<AgentDraft>) => void;
        onReconcileEnabledTools?: (cleaned: EnabledTool[]) => void;
    }) => (
        <>
            <input
                aria-label='Dynamic tool loading'
                type='checkbox'
                checked={mcpDynamicToolLoading}
                onChange={(e) => onChange({mcpDynamicToolLoading: e.target.checked})}
            />
            <button
                type='button'
                onClick={() => onReconcileEnabledTools?.([])}
            >
                {'Reconcile (drop all enabled tools)'}
            </button>
        </>
    ),
}));

const services: ServiceInfo[] = [
    {
        id: 'svc_1',
        name: 'Mock Service',
        type: 'openai',
        defaultModel: 'gpt-4.1',
        outputTokenLimit: 4096,
        useResponsesAPI: true,
    },
];

const mockCreateAgent = createAgent as jest.MockedFunction<typeof createAgent>;
const mockUpdateAgent = updateAgent as jest.MockedFunction<typeof updateAgent>;

const accessControlClient = jest.requireMock('@/client/access_control') as {
    getAgentAccessPolicy: jest.Mock;
    deleteAgentAccessPolicy: jest.Mock;
};

const savedAgent = {
    id: 'agent_1',
    name: 'myagent',
    displayName: 'My Agent',
    customInstructions: '',
    serviceID: 'svc_1',
    model: '',
    enableVision: true,
    disableTools: false,
    channelAccessLevel: 0,
    channelIDs: [],
    userAccessLevel: 0,
    userIDs: [],
    teamIDs: [],
    enabledNativeTools: ['web_search'],
    enabledMCPTools: [],
    autoEnableNewMCPTools: true,
    mcpDynamicToolLoading: true,
    reasoningEnabled: true,
    reasoningEffort: 'medium',
    thinkingBudget: 0,
    structuredOutputEnabled: false,
    maxToolTurns: 30,
} satisfies UserAgent;

function renderView(onBack = jest.fn()) {
    const result = render(
        <IntlProvider locale='en'>
            <AgentConfigView
                mode='create'
                services={services}
                onBack={onBack}
                onSaved={jest.fn()}
            />
        </IntlProvider>,
    );

    return {
        ...result,
        onBack,
    };
}

describe('AgentConfigView', () => {
    beforeEach(() => {
        jest.clearAllMocks();
    });

    test('confirms before dismissing unsaved changes from back button', async () => {
        const {onBack} = renderView();

        fireEvent.change(screen.getByLabelText('Display Name'), {target: {value: 'Unsaved Agent'}});
        fireEvent.click(screen.getByRole('button', {name: 'Back to agents'}));

        expect(screen.getByRole('dialog', {name: 'Discard changes?'})).not.toBeNull();
        expect(onBack).not.toHaveBeenCalled();

        fireEvent.click(screen.getByRole('button', {name: 'Keep editing'}));
        await waitForElementToBeRemoved(() => screen.queryByRole('dialog', {name: 'Discard changes?'}));
        expect((screen.getByLabelText('Display Name') as HTMLInputElement).value).toBe('Unsaved Agent');

        fireEvent.click(screen.getByRole('button', {name: 'Back to agents'}));
        fireEvent.click(screen.getByRole('button', {name: 'Discard'}));

        expect(onBack).toHaveBeenCalledTimes(1);
    });

    test('navigates back immediately when there are no unsaved changes', () => {
        const {onBack} = renderView();

        fireEvent.click(screen.getByRole('button', {name: 'Back to agents'}));

        expect(onBack).toHaveBeenCalledTimes(1);
        expect(screen.queryByRole('dialog', {name: 'Discard changes?'})).toBeNull();
    });

    // Regression test for MM-69185.
    //
    // After saving on the MCP tab, navigating back to the same agent's MCP tab and
    // clicking Cancel must not trigger the "Discard changes" modal when the user
    // hasn't made any edits — even if the persisted enabledMCPTools list contains
    // entries that aren't currently visible in the live MCP catalog (e.g. an
    // MCP server is temporarily disconnected). The MCP tab silently reconciles
    // those entries via onReconcileEnabledTools; that callback must update both
    // draft AND baseline so the form does not become dirty.
    test('reconciling orphaned MCP tools does not mark the form dirty (MM-69185)', () => {
        const agent: UserAgent = {
            id: 'agent_1',
            name: 'existingagent',
            displayName: 'Existing Agent',
            customInstructions: '',
            serviceID: 'svc_1',
            model: '',
            enableVision: true,
            disableTools: false,
            channelAccessLevel: 0,
            channelIDs: [],
            userAccessLevel: 0,
            userIDs: [],
            teamIDs: [],
            enabledNativeTools: ['web_search'],

            // The persisted enabledMCPTools include entries that the live MCP catalog
            // no longer surfaces (orphans). McpsTab calls onReconcileEnabledTools to
            // drop them; that path must not mark the form dirty.
            enabledMCPTools: [
                {server_origin: 'embedded://mattermost', tool_name: 'read_post'},
                {server_origin: 'embedded://mattermost', tool_name: 'deleted_tool'},
            ],
            autoEnableNewMCPTools: false,
            mcpDynamicToolLoading: true,
            reasoningEnabled: true,
            reasoningEffort: 'medium',
            thinkingBudget: 0,
            structuredOutputEnabled: false,
            maxToolTurns: 30,
        };

        const onBack = jest.fn();

        render(
            <IntlProvider locale='en'>
                <AgentConfigView
                    mode='edit'
                    agent={agent}
                    services={services}
                    onBack={onBack}
                    onSaved={jest.fn()}
                />
            </IntlProvider>,
        );

        // Open the MCP tab and trigger reconciliation. The mocked McpsTab exposes
        // a button that fires onReconcileEnabledTools with an empty list, which
        // mirrors what the real tab does when every saved tool is orphaned.
        fireEvent.click(screen.getByRole('button', {name: 'MCPs'}));
        fireEvent.click(screen.getByRole('button', {name: /Reconcile/}));

        fireEvent.click(screen.getByRole('button', {name: 'Cancel'}));

        expect(screen.queryByRole('dialog', {name: 'Discard changes?'})).toBeNull();
        expect(onBack).toHaveBeenCalledTimes(1);
    });

    test('loads edit mode without treating existing values as dirty', () => {
        const onBack = jest.fn();

        render(
            <IntlProvider locale='en'>
                <AgentConfigView
                    mode='edit'
                    agent={{
                        id: 'agent_1',
                        name: 'existingagent',
                        displayName: 'Existing Agent',
                        customInstructions: '',
                        serviceID: 'svc_1',
                        model: '',
                        enableVision: true,
                        disableTools: false,
                        channelAccessLevel: 0,
                        channelIDs: [],
                        userAccessLevel: 0,
                        userIDs: [],
                        teamIDs: [],
                        enabledNativeTools: ['web_search'],
                        enabledMCPTools: [],
                        autoEnableNewMCPTools: true,
                        mcpDynamicToolLoading: true,
                        reasoningEnabled: true,
                        reasoningEffort: 'medium',
                        thinkingBudget: 0,
                        structuredOutputEnabled: false,
                        maxToolTurns: 30,
                    }}
                    services={services}
                    onBack={onBack}
                    onSaved={jest.fn()}
                />
            </IntlProvider>,
        );

        fireEvent.keyDown(document, {key: 'Escape'});

        expect(onBack).toHaveBeenCalledTimes(1);
        expect(screen.queryByRole('dialog', {name: 'Discard changes?'})).toBeNull();
    });

    test('legacy agent with unset maxToolTurns is not treated as dirty in edit mode', () => {
        const onBack = jest.fn();

        render(
            <IntlProvider locale='en'>
                <AgentConfigView
                    mode='edit'
                    agent={{
                        id: 'agent_legacy',
                        name: 'legacyagent',
                        displayName: 'Legacy Agent',
                        customInstructions: '',
                        serviceID: 'svc_1',
                        model: '',
                        enableVision: true,
                        disableTools: false,
                        channelAccessLevel: 0,
                        channelIDs: [],
                        userAccessLevel: 0,
                        userIDs: [],
                        teamIDs: [],
                        enabledNativeTools: ['web_search'],
                        enabledMCPTools: [],
                        autoEnableNewMCPTools: true,
                        reasoningEnabled: true,
                        reasoningEffort: 'medium',
                        thinkingBudget: 0,
                        structuredOutputEnabled: false,
                        maxToolTurns: 0,
                    }}
                    services={services}
                    onBack={onBack}
                    onSaved={jest.fn()}
                />
            </IntlProvider>,
        );

        fireEvent.keyDown(document, {key: 'Escape'});

        expect(onBack).toHaveBeenCalledTimes(1);
        expect(screen.queryByRole('dialog', {name: 'Discard changes?'})).toBeNull();
    });

    test('serializes dynamic tool loading default true on create', async () => {
        mockCreateAgent.mockResolvedValue(savedAgent);
        renderView();

        fireEvent.change(screen.getByLabelText('Display Name'), {target: {value: 'My Agent'}});
        fireEvent.change(screen.getByLabelText('Username'), {target: {value: 'myagent'}});
        fireEvent.click(screen.getByText('Select service'));
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        await waitFor(() => expect(mockCreateAgent).toHaveBeenCalledTimes(1));
        expect(mockCreateAgent).toHaveBeenCalledWith(expect.objectContaining({
            mcpDynamicToolLoading: true,
        }));
    });

    test('serializes explicit dynamic tool loading false on create', async () => {
        mockCreateAgent.mockResolvedValue({...savedAgent, mcpDynamicToolLoading: false});
        renderView();

        fireEvent.change(screen.getByLabelText('Display Name'), {target: {value: 'My Agent'}});
        fireEvent.change(screen.getByLabelText('Username'), {target: {value: 'myagent'}});
        fireEvent.click(screen.getByText('Select service'));
        fireEvent.click(screen.getByRole('button', {name: 'MCPs'}));
        fireEvent.click(screen.getByLabelText('Dynamic tool loading'));
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        await waitFor(() => expect(mockCreateAgent).toHaveBeenCalledTimes(1));
        expect(mockCreateAgent).toHaveBeenCalledWith(expect.objectContaining({
            mcpDynamicToolLoading: false,
        }));
    });

    test('defaults missing edit response dynamic tool loading to true on update', async () => {
        mockUpdateAgent.mockResolvedValue(savedAgent);
        const legacyAgent = {
            ...savedAgent,
            id: 'agent_legacy',
            name: 'legacyagent',
            displayName: 'Legacy Agent',
        } as Partial<UserAgent> as UserAgent;
        delete (legacyAgent as Partial<UserAgent>).mcpDynamicToolLoading;

        render(
            <IntlProvider locale='en'>
                <AgentConfigView
                    mode='edit'
                    agent={legacyAgent}
                    services={services}
                    onBack={jest.fn()}
                    onSaved={jest.fn()}
                />
            </IntlProvider>,
        );

        fireEvent.change(screen.getByLabelText('Display Name'), {target: {value: 'Renamed Agent'}});
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        await waitFor(() => expect(mockUpdateAgent).toHaveBeenCalledTimes(1));
        expect(mockUpdateAgent).toHaveBeenCalledWith('agent_legacy', expect.objectContaining({
            mcpDynamicToolLoading: true,
        }));
    });

    test('blocks saving when maxToolTurns exceeds the hard cap', () => {
        render(
            <IntlProvider locale='en'>
                <AgentConfigView
                    mode='edit'
                    agent={{
                        id: 'agent_1',
                        name: 'existingagent',
                        displayName: 'Existing Agent',
                        customInstructions: '',
                        serviceID: 'svc_1',
                        model: '',
                        enableVision: true,
                        disableTools: false,
                        channelAccessLevel: 0,
                        channelIDs: [],
                        userAccessLevel: 0,
                        userIDs: [],
                        teamIDs: [],
                        enabledNativeTools: ['web_search'],
                        enabledMCPTools: [],
                        autoEnableNewMCPTools: true,
                        mcpDynamicToolLoading: true,
                        reasoningEnabled: true,
                        reasoningEffort: 'medium',
                        thinkingBudget: 0,
                        structuredOutputEnabled: false,
                        maxToolTurns: 30,
                    }}
                    services={services}
                    onBack={jest.fn()}
                    onSaved={jest.fn()}
                />
            </IntlProvider>,
        );

        fireEvent.change(screen.getByLabelText('Max tool turns'), {target: {value: '251'}});
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        expect(screen.getByText('Max tool turns must be between 1 and 250')).not.toBeNull();
        expect(updateAgent).not.toHaveBeenCalled();
    });

    // --- Switching away from attribute-based access (policy switch state machine) ---

    const attributeBasedAgent: UserAgent = {
        ...savedAgent,
        id: 'agent_abac',
        name: 'abacagent',
        displayName: 'ABAC Agent',
        userAccessLevel: 4,
    };

    const existingPolicy = {
        id: 'agent_abac',
        name: 'ABAC Agent',
        rules: [{actions: ['use'], expression: 'user.attributes.team == "eng"'}],
    };

    function renderEditView(agent: UserAgent) {
        const onSaved = jest.fn();
        render(
            <IntlProvider locale='en'>
                <AgentConfigView
                    mode='edit'
                    agent={agent}
                    services={services}
                    onBack={jest.fn()}
                    onSaved={onSaved}
                />
            </IntlProvider>,
        );
        return {onSaved};
    }

    function switchAwayAndSave() {
        fireEvent.click(screen.getByRole('button', {name: 'Access'}));
        fireEvent.click(screen.getByRole('button', {name: 'Switch user access to everyone'}));
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));
    }

    test('switching away without an existing policy saves and closes without a dialog', async () => {
        mockUpdateAgent.mockResolvedValue({...attributeBasedAgent, userAccessLevel: 0});
        accessControlClient.getAgentAccessPolicy.mockResolvedValue(null);

        const {onSaved} = renderEditView(attributeBasedAgent);
        switchAwayAndSave();

        await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
        expect(accessControlClient.getAgentAccessPolicy).toHaveBeenCalledWith('agent_abac');
        expect(accessControlClient.deleteAgentAccessPolicy).not.toHaveBeenCalled();
        expect(screen.queryByRole('dialog', {name: 'Delete access policy?'})).toBeNull();
    });

    test('switching away with an existing policy offers deletion; keeping it closes without deleting', async () => {
        mockUpdateAgent.mockResolvedValue({...attributeBasedAgent, userAccessLevel: 0});
        accessControlClient.getAgentAccessPolicy.mockResolvedValue(existingPolicy);

        const {onSaved} = renderEditView(attributeBasedAgent);
        switchAwayAndSave();

        await screen.findByRole('dialog', {name: 'Delete access policy?'});
        expect(onSaved).not.toHaveBeenCalled();

        fireEvent.click(screen.getByRole('button', {name: 'Keep policy'}));

        await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
        expect(accessControlClient.deleteAgentAccessPolicy).not.toHaveBeenCalled();
    });

    test('failed policy deletion keeps the view open with a retry state instead of silently closing', async () => {
        mockUpdateAgent.mockResolvedValue({...attributeBasedAgent, userAccessLevel: 0});
        accessControlClient.getAgentAccessPolicy.mockResolvedValue(existingPolicy);
        accessControlClient.deleteAgentAccessPolicy.mockRejectedValueOnce(new Error('policy delete exploded'));

        const {onSaved} = renderEditView(attributeBasedAgent);
        switchAwayAndSave();

        await screen.findByRole('dialog', {name: 'Delete access policy?'});
        fireEvent.click(screen.getByRole('button', {name: 'Delete policy'}));

        // Deletion failed: the view must stay open with the error surfaced.
        await screen.findByRole('dialog', {name: "Couldn't delete the access policy"});
        expect(screen.getByText(/policy delete exploded/)).not.toBeNull();
        expect(onSaved).not.toHaveBeenCalled();

        // Retry succeeds and only then does the view close.
        accessControlClient.deleteAgentAccessPolicy.mockResolvedValueOnce(null);
        fireEvent.click(screen.getByRole('button', {name: 'Retry'}));

        await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
        expect(accessControlClient.deleteAgentAccessPolicy).toHaveBeenCalledTimes(2);
    });

    test('failed policy deletion can end with keeping the policy after seeing the error', async () => {
        mockUpdateAgent.mockResolvedValue({...attributeBasedAgent, userAccessLevel: 0});
        accessControlClient.getAgentAccessPolicy.mockResolvedValue(existingPolicy);
        accessControlClient.deleteAgentAccessPolicy.mockRejectedValue(new Error('still broken'));

        const {onSaved} = renderEditView(attributeBasedAgent);
        switchAwayAndSave();

        await screen.findByRole('dialog', {name: 'Delete access policy?'});
        fireEvent.click(screen.getByRole('button', {name: 'Delete policy'}));

        // The confirm dialog may still be exiting its transition; scope the
        // click to the retry dialog.
        const retryDialog = await screen.findByRole('dialog', {name: "Couldn't delete the access policy"});
        fireEvent.click(within(retryDialog).getByRole('button', {name: 'Keep policy'}));

        await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
    });

    test('offers the delete flow when the policy existence check fails', async () => {
        mockUpdateAgent.mockResolvedValue({...attributeBasedAgent, userAccessLevel: 0});
        accessControlClient.getAgentAccessPolicy.mockRejectedValue(new Error('lookup failed'));

        const {onSaved} = renderEditView(attributeBasedAgent);
        switchAwayAndSave();

        // Unknown existence must be treated conservatively: ask the user.
        await screen.findByRole('dialog', {name: 'Delete access policy?'});
        expect(onSaved).not.toHaveBeenCalled();
    });

    test('preserves explicit dynamic tool loading false on update', async () => {
        mockUpdateAgent.mockResolvedValue({...savedAgent, mcpDynamicToolLoading: false});
        const agent = {
            ...savedAgent,
            id: 'agent_dynamic_off',
            name: 'dynamicoff',
            displayName: 'Dynamic Off',
            mcpDynamicToolLoading: false,
        };

        render(
            <IntlProvider locale='en'>
                <AgentConfigView
                    mode='edit'
                    agent={agent}
                    services={services}
                    onBack={jest.fn()}
                    onSaved={jest.fn()}
                />
            </IntlProvider>,
        );

        fireEvent.change(screen.getByLabelText('Display Name'), {target: {value: 'Dynamic Off Updated'}});
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        await waitFor(() => expect(mockUpdateAgent).toHaveBeenCalledTimes(1));
        expect(mockUpdateAgent).toHaveBeenCalledWith('agent_dynamic_off', expect.objectContaining({
            mcpDynamicToolLoading: false,
        }));
    });
});
