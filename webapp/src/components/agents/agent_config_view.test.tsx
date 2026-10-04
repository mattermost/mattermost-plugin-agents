// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor, waitForElementToBeRemoved, within} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {createAgent, getAgentVersion, getAgentVersions, getProfilesByIds, restoreAgentVersion, updateAgent} from '@/client';
import {AgentVersionDetail, AgentVersionList, EnabledTool, MaxCustomInstructionsRunes, ServiceInfo, UserAgent} from '@/types/agents';
import {downloadAgentExport} from '@/utils/download_agent_export';
import {useCurrentUserHasSystemPermission} from '@/utils/permissions';

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
            formatNumber: (value: number) => new Intl.NumberFormat('en').format(value),
            formatDate: (value: number) => new Date(value).toISOString().slice(0, 10),
        }),
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('@/license', () => ({
    useIsLicensedFor: jest.fn(() => true),
}));

jest.mock('@/utils/permissions', () => ({
    useCurrentUserHasSystemPermission: jest.fn(),
}));

jest.mock('@/client', () => ({
    createAgent: jest.fn(),
    updateAgent: jest.fn(),
    uploadAgentAvatar: jest.fn(),
    getUserMCPTools: jest.fn(),
    getAgentVersions: jest.fn(),
    getAgentVersion: jest.fn(),
    restoreAgentVersion: jest.fn(),
    getProfilesByIds: jest.fn(),
}));

jest.mock('@/utils/download_agent_export', () => ({
    downloadAgentExport: jest.fn(),
}));

jest.mock('@/utils/access_control', () => ({
    useABACSupport: () => ({supported: false, loading: false}),
}));

jest.mock('@/hooks/use_mcp_connection_events', () => ({
    useMCPConnectionEvents: jest.fn(),
}));

const mockedUseCurrentUserHasSystemPermission = useCurrentUserHasSystemPermission as unknown as jest.Mock;

jest.mock('@/components/system_console/bot', () => ({
    ChannelAccessLevel: {
        All: 0,
        Allow: 1,
        Block: 2,
        None: 3,
    },
    UserAccessLevel: {
        All: 0,
        Allow: 1,
        Block: 2,
        None: 3,
        AttributeBased: 4,
    },
}));

jest.mock('./tabs/config_tab', () => ({
    __esModule: true,
    default: ({
        draft,
        onChange,
        onAvatarChange,
        avatarFile,
        errors = {},
    }: {
        draft: AgentDraft;
        onChange: (updates: Partial<AgentDraft>) => void;
        onAvatarChange: (file: File | null) => void;
        avatarFile?: File | null;
        errors?: Record<string, string>;
    }) => (
        <>
            <input
                aria-label='Bot avatar'
                type='file'
                onChange={(e) => onAvatarChange(e.target.files?.[0] ?? null)}
            />
            <div data-testid='pending-avatar'>{avatarFile?.name ?? ''}</div>
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
            <textarea
                aria-label='Custom instructions'
                value={draft.customInstructions}
                onChange={(e) => onChange({customInstructions: e.target.value})}
            />
            {errors.customInstructions && <div>{errors.customInstructions}</div>}
            <input
                aria-label='Dynamic tool loading'
                type='checkbox'
                checked={draft.mcpDynamicToolLoading}
                onChange={(e) => onChange({mcpDynamicToolLoading: e.target.checked})}
            />
            <select
                aria-label='AI Service'
                value={draft.serviceId}
                onChange={(e) => onChange({serviceId: e.target.value})}
            >
                <option value='svc_1'>{'Mock Service'}</option>
                <option value='svc_2'>{'Other Service'}</option>
            </select>
            <input
                aria-label='Enable Tools'
                type='checkbox'
                checked={!draft.disableTools}
                onChange={(e) => onChange({disableTools: !e.target.checked})}
            />
            <button
                type='button'
                onClick={() => onChange({serviceId: 'svc_1'})}
            >
                {'Select service'}
            </button>
        </>
    ),
}));

jest.mock('./tabs/access_tab', () => {
    const {useState} = jest.requireActual('react');
    const Select = jest.requireActual('react-select').default;
    const ConfirmationDialog = jest.requireActual('@/components/confirmation_dialog').default;

    const MockAccessTab = ({onChange}: {onChange: (updates: Partial<AgentDraft>) => void}) => {
        const [showNestedDialog, setShowNestedDialog] = useState(false);
        return (
            <>
                <input
                    aria-label='Channel access'
                    type='checkbox'
                />
                <button
                    type='button'
                    onClick={() => onChange({userAccessLevel: 0})}
                >
                    {'Switch user access to everyone'}
                </button>
                <Select
                    aria-label='Agent admins'
                    options={[{value: 'user_1', label: 'Admin User'}]}
                />
                <button
                    type='button'
                    onClick={() => setShowNestedDialog(true)}
                >
                    {'Open nested dialog'}
                </button>
                {showNestedDialog && (
                    <ConfirmationDialog
                        title='Remove access policy?'
                        titleId='nested-dialog-title'
                        message='Nested dialog'
                        confirmButtonText='Remove'
                        onConfirm={() => setShowNestedDialog(false)}
                        onCancel={() => setShowNestedDialog(false)}
                        managedAccessibility={true}
                    />
                )}
            </>
        );
    };

    return {
        __esModule: true,
        default: MockAccessTab,
    };
});

jest.mock('./tabs/mcps_tab', () => ({
    __esModule: true,
    default: ({
        useServiceAccountAuth,
        onChange,
        onReconcileEnabledTools,
    }: {
        useServiceAccountAuth: boolean;
        serviceAccountFieldsLocked: boolean;
        canEditServiceAccountAuth: boolean;
        onChange: (updates: {useServiceAccountAuth?: boolean}) => void;
        onReconcileEnabledTools?: (cleaned: EnabledTool[]) => void;
    }) => (
        <>
            <input
                aria-label='Use service accounts'
                type='checkbox'
                checked={useServiceAccountAuth}
                onChange={(e) => onChange({useServiceAccountAuth: e.target.checked})}
            />
            <input
                aria-label='Automatically enable all MCP tools'
                type='checkbox'
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
    useServiceAccountAuth: false,
    reasoningEnabled: true,
    reasoningEffort: 'medium',
    thinkingBudget: 0,

    // The server still returns the deprecated per-agent structured output flag;
    // structured output is now a per-service policy. Keeping it on the fixture
    // exercises the path where the response carries it and the UI must not
    // adopt it or send it back.
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

const serviceAccountFieldsBanner = /Access and MCP tool grants require a system administrator/;

describe('AgentConfigView', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockedUseCurrentUserHasSystemPermission.mockReturnValue(true);
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
            useServiceAccountAuth: false,
            mcpDynamicToolLoading: true,
            reasoningEnabled: true,
            reasoningEffort: 'medium',
            thinkingBudget: 0,
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
                        useServiceAccountAuth: false,
                        mcpDynamicToolLoading: true,
                        reasoningEnabled: true,
                        reasoningEffort: 'medium',
                        thinkingBudget: 0,
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
                        useServiceAccountAuth: false,
                        reasoningEnabled: true,
                        reasoningEffort: 'medium',
                        thinkingBudget: 0,
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

    test.each([
        {
            name: 'closing an open react-select menu',
            dismissChild: () => {
                const input = screen.getByLabelText('Agent admins');
                fireEvent.keyDown(input, {key: 'ArrowDown'});
                expect(screen.getByText('Admin User')).not.toBeNull();

                fireEvent.keyDown(input, {key: 'Escape'});
                expect(screen.queryByText('Admin User')).toBeNull();
            },
        },
        {
            name: 'cancelling a nested confirmation dialog',
            dismissChild: () => {
                fireEvent.click(screen.getByRole('button', {name: 'Open nested dialog'}));
                expect(screen.getByRole('dialog', {name: 'Remove access policy?'})).not.toBeNull();

                fireEvent.keyDown(document, {key: 'Escape'});
                expect(screen.queryByRole('dialog', {name: 'Remove access policy?'})).toBeNull();
            },
        },
    ])('Escape used for $name stays on the page; a plain Escape still goes back', ({dismissChild}) => {
        const {onBack} = renderView();
        fireEvent.click(screen.getByRole('button', {name: 'Access'}));

        dismissChild();

        expect(onBack).not.toHaveBeenCalled();

        fireEvent.keyDown(document, {key: 'Escape'});

        expect(onBack).toHaveBeenCalledTimes(1);
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

    test.each([
        {licensed: true, expected: ['web_search']},
        {licensed: false, expected: []},
    ])('defaults provider web search on create only where it is licensed (licensed=$licensed)', async ({licensed, expected}) => {
        const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};
        useIsLicensedFor.mockImplementation((capability: string) => capability !== 'provider_web_search' || licensed);
        mockCreateAgent.mockResolvedValue(savedAgent);
        renderView();

        fireEvent.change(screen.getByLabelText('Display Name'), {target: {value: 'My Agent'}});
        fireEvent.change(screen.getByLabelText('Username'), {target: {value: 'myagent'}});
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        await waitFor(() => expect(mockCreateAgent).toHaveBeenCalledTimes(1));
        expect(mockCreateAgent).toHaveBeenCalledWith(expect.objectContaining({enabledNativeTools: expected}));
        useIsLicensedFor.mockReturnValue(true);
    });

    test('serializes explicit MCP settings on create', async () => {
        mockCreateAgent.mockResolvedValue({...savedAgent, mcpDynamicToolLoading: false, useServiceAccountAuth: true});
        renderView();

        fireEvent.change(screen.getByLabelText('Display Name'), {target: {value: 'My Agent'}});
        fireEvent.change(screen.getByLabelText('Username'), {target: {value: 'myagent'}});
        fireEvent.click(screen.getByLabelText('Dynamic tool loading'));
        fireEvent.click(screen.getByRole('button', {name: 'MCPs'}));
        fireEvent.click(screen.getByLabelText('Use service accounts'));
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        await waitFor(() => expect(mockCreateAgent).toHaveBeenCalledTimes(1));
        expect(mockCreateAgent).toHaveBeenCalledWith(expect.objectContaining({
            mcpDynamicToolLoading: false,
            useServiceAccountAuth: true,
        }));
    });

    // Structured output moved to a per-service policy owned by administrators.
    // Sending the deprecated per-agent flag would resurrect the old behaviour
    // for every agent saved from this UI.
    test('omits the deprecated structured output flag from the create payload', async () => {
        mockCreateAgent.mockResolvedValue(savedAgent);
        renderView();

        fireEvent.change(screen.getByLabelText('Display Name'), {target: {value: 'My Agent'}});
        fireEvent.change(screen.getByLabelText('Username'), {target: {value: 'myagent'}});
        fireEvent.click(screen.getByText('Select service'));
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        await waitFor(() => expect(mockCreateAgent).toHaveBeenCalledTimes(1));
        const payload = mockCreateAgent.mock.calls[0][0] as Record<string, unknown>;
        expect('structuredOutputEnabled' in payload).toBe(false);
    });

    test('omits the deprecated structured output flag from the update payload even when the agent response carries it', async () => {
        mockUpdateAgent.mockResolvedValue(savedAgent);

        render(
            <IntlProvider locale='en'>
                <AgentConfigView
                    mode='edit'
                    agent={savedAgent}
                    services={services}
                    onBack={jest.fn()}
                    onSaved={jest.fn()}
                />
            </IntlProvider>,
        );

        fireEvent.change(screen.getByLabelText('Display Name'), {target: {value: 'Renamed Agent'}});
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        await waitFor(() => expect(mockUpdateAgent).toHaveBeenCalledTimes(1));
        const payload = mockUpdateAgent.mock.calls[0][1] as Record<string, unknown>;
        expect('structuredOutputEnabled' in payload).toBe(false);
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
                        useServiceAccountAuth: false,
                        mcpDynamicToolLoading: true,
                        reasoningEnabled: true,
                        reasoningEffort: 'medium',
                        thinkingBudget: 0,
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

    // --- Switching away from attribute-based access ---

    const attributeBasedAgent: UserAgent = {
        ...savedAgent,
        id: 'agent_abac',
        name: 'abacagent',
        displayName: 'ABAC Agent',
        userAccessLevel: 4,
    };

    function editViewElement(agent: UserAgent, onBack: jest.Mock, onSaved: jest.Mock) {
        return (
            <IntlProvider locale='en'>
                <AgentConfigView
                    mode='edit'
                    agent={agent}
                    services={services}
                    onBack={onBack}
                    onSaved={onSaved}
                />
            </IntlProvider>
        );
    }

    function renderEditView(agent: UserAgent) {
        const onSaved = jest.fn();
        const onBack = jest.fn();
        const result = render(editViewElement(agent, onBack, onSaved));
        return {...result, onSaved, onBack};
    }

    function switchAwayAndSave() {
        fireEvent.click(screen.getByRole('button', {name: 'Access'}));
        fireEvent.click(screen.getByRole('button', {name: 'Switch user access to everyone'}));
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));
    }

    test('switching away from attribute-based access saves and closes without a dialog', async () => {
        mockUpdateAgent.mockResolvedValue({...attributeBasedAgent, userAccessLevel: 0});

        const {onSaved} = renderEditView(attributeBasedAgent);
        switchAwayAndSave();

        await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
        expect(mockUpdateAgent).toHaveBeenCalledWith('agent_abac', expect.objectContaining({
            userAccessLevel: 0,
        }));
        expect(screen.queryByRole('dialog', {name: 'Delete access policy?'})).toBeNull();
    });

    test('a failed switch-away update stays open, reports the failure, and can be retried safely', async () => {
        mockUpdateAgent.
            mockRejectedValueOnce(new Error('failed to delete access policy: policy storage unavailable')).
            mockResolvedValueOnce({...attributeBasedAgent, userAccessLevel: 0});

        const {onSaved} = renderEditView(attributeBasedAgent);
        switchAwayAndSave();

        expect(await screen.findByText('failed to delete access policy: policy storage unavailable')).not.toBeNull();
        expect(onSaved).not.toHaveBeenCalled();
        expect(mockUpdateAgent).toHaveBeenCalledTimes(1);
        expect(screen.queryByRole('dialog', {name: 'Delete access policy?'})).toBeNull();

        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
        expect(mockUpdateAgent).toHaveBeenCalledTimes(2);
    });

    test('privilege-only rerenders do not submit an update that could delete the policy', () => {
        const result = renderEditView(attributeBasedAgent);

        mockedUseCurrentUserHasSystemPermission.mockReturnValue(false);
        result.rerender(editViewElement(attributeBasedAgent, result.onBack, result.onSaved));

        expect(mockUpdateAgent).not.toHaveBeenCalled();
        expect(result.onSaved).not.toHaveBeenCalled();
    });

    test.each([
        ['exactly the ASCII character limit', 'a'.repeat(MaxCustomInstructionsRunes)],

        // Code points under the limit but UTF-16 length over it: catches .length-based counting.
        ['astral characters below the character limit', '😀'.repeat((MaxCustomInstructionsRunes / 2) + 1)],
    ])('allows saving with %s', async (_description, customInstructions) => {
        mockCreateAgent.mockResolvedValue(savedAgent);
        renderView();

        fireEvent.change(screen.getByLabelText('Display Name'), {target: {value: 'My Agent'}});
        fireEvent.change(screen.getByLabelText('Username'), {target: {value: 'myagent'}});
        fireEvent.change(screen.getByLabelText('Custom instructions'), {
            target: {value: customInstructions},
        });
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        await waitFor(() => expect(mockCreateAgent).toHaveBeenCalledTimes(1));
        expect(screen.queryByText('Custom instructions must be 100,000 characters or fewer')).toBeNull();
    });

    test.each([
        ['ASCII characters', 'a'.repeat(MaxCustomInstructionsRunes + 1)],
        ['astral characters', '😀'.repeat(MaxCustomInstructionsRunes + 1)],
    ])('blocks saving when custom instructions exceed the character limit using %s', (_description, customInstructions) => {
        renderView();

        fireEvent.change(screen.getByLabelText('Display Name'), {target: {value: 'My Agent'}});
        fireEvent.change(screen.getByLabelText('Username'), {target: {value: 'myagent'}});
        fireEvent.change(screen.getByLabelText('Custom instructions'), {
            target: {value: customInstructions},
        });
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        expect(screen.getByText('Custom instructions must be 100,000 characters or fewer')).not.toBeNull();
        expect(createAgent).not.toHaveBeenCalled();
    });

    // Update is a full-replace PUT: a payload that drops either flag would
    // silently revert the saved MCP settings.
    test('preserves explicit MCP settings on update', async () => {
        mockUpdateAgent.mockResolvedValue({...savedAgent, mcpDynamicToolLoading: false, useServiceAccountAuth: true});
        const agent = {
            ...savedAgent,
            id: 'agent_dynamic_off',
            name: 'dynamicoff',
            displayName: 'Dynamic Off',
            mcpDynamicToolLoading: false,
            useServiceAccountAuth: true,
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
            useServiceAccountAuth: true,
        }));
    });

    // Parent-level soft-lock: Save stays enabled, banner explains the policy, and
    // turning SA off clears the lock. Per-control disabled state is covered by
    // config_tab / access_tab / mcps_tab real-component tests.
    test('shows service-account soft-lock banner and keeps Save enabled for non-admins', () => {
        mockedUseCurrentUserHasSystemPermission.mockReturnValue(false);
        const agent = {
            ...savedAgent,
            id: 'agent_sa',
            name: 'saagent',
            displayName: 'SA Agent',
            useServiceAccountAuth: true,
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

        const saveButton = screen.getByRole('button', {name: 'Save'});
        expect((saveButton as HTMLButtonElement).disabled).toBe(false);
        expect(screen.getByText(serviceAccountFieldsBanner)).not.toBeNull();

        fireEvent.change(screen.getByLabelText('Display Name'), {target: {value: 'SA Agent Updated'}});
        expect((screen.getByRole('button', {name: 'Save'}) as HTMLButtonElement).disabled).toBe(false);

        fireEvent.click(screen.getByRole('button', {name: 'MCPs'}));
        expect((screen.getByLabelText('Use service accounts') as HTMLInputElement).disabled).toBe(false);

        fireEvent.click(screen.getByLabelText('Use service accounts'));
        expect(screen.queryByText(serviceAccountFieldsBanner)).toBeNull();
        expect((screen.getByRole('button', {name: 'Save'}) as HTMLButtonElement).disabled).toBe(false);
    });
});

describe('AgentConfigView history and export', () => {
    const mockGetAgentVersions = getAgentVersions as jest.MockedFunction<typeof getAgentVersions>;
    const mockGetAgentVersion = getAgentVersion as jest.MockedFunction<typeof getAgentVersion>;
    const mockRestoreAgentVersion = restoreAgentVersion as jest.MockedFunction<typeof restoreAgentVersion>;
    const mockGetProfilesByIds = getProfilesByIds as jest.MockedFunction<typeof getProfilesByIds>;
    const mockDownloadAgentExport = downloadAgentExport as jest.MockedFunction<typeof downloadAgentExport>;

    const existingAgent: UserAgent = {
        ...savedAgent,
        id: 'agent_1',
        name: 'existingagent',
        displayName: 'Existing Agent',
        customInstructions: 'current instructions',
    };

    const versionList: AgentVersionList = {
        currentVersion: 2,
        versions: [
            {version: 2, createdBy: 'user_1', createAt: 1759600000000, source: 'update', restoredFromVersion: 0, changedFields: ['customInstructions']},
            {version: 1, createdBy: '', createAt: 1759500000000, source: 'initial', restoredFromVersion: 0, changedFields: []},
        ],
    };

    function versionDetail(version: number, customInstructions: string): AgentVersionDetail {
        const summary = versionList.versions.find((v) => v.version === version)!;
        return {...summary, config: {...existingAgent, customInstructions}};
    }

    function renderEdit(onRestored = jest.fn(), agent: UserAgent = existingAgent) {
        render(
            <IntlProvider locale='en'>
                <AgentConfigView
                    mode='edit'
                    agent={agent}
                    services={services}
                    onBack={jest.fn()}
                    onSaved={jest.fn()}
                    onRestored={onRestored}
                />
            </IntlProvider>,
        );
        return {onRestored};
    }

    beforeEach(() => {
        jest.clearAllMocks();
        mockedUseCurrentUserHasSystemPermission.mockReturnValue(true);
        mockGetAgentVersions.mockResolvedValue(versionList);
        mockGetAgentVersion.mockImplementation(async (_id, version) => (
            version === 2 ? versionDetail(2, 'current instructions') : versionDetail(1, 'original instructions')
        ));
        mockGetProfilesByIds.mockResolvedValue([{id: 'user_1', username: 'alice', first_name: 'Alice', last_name: 'Smith', nickname: ''}] as never);
    });

    test('offers the History tab and Export only for existing agents', () => {
        renderView();
        expect(screen.queryByRole('button', {name: 'History'})).toBeNull();
        expect(screen.queryByRole('button', {name: 'Export'})).toBeNull();
    });

    test('History tab lists versions newest first and badges the current one', async () => {
        renderEdit();
        fireEvent.click(screen.getByRole('button', {name: 'History'}));

        const list = await screen.findByTestId('version-list');
        await waitFor(() => expect(within(list).getByText(/Alice Smith/)).not.toBeNull());

        const items = within(list).getAllByRole('listitem');
        expect(items).toHaveLength(2);
        expect(within(items[0]).getByText('Version 2')).not.toBeNull();
        expect(within(items[0]).getByText('Current')).not.toBeNull();
        expect(within(items[0]).getByText('Edited')).not.toBeNull();
        expect(within(items[0]).getByText('Changed: Custom instructions')).not.toBeNull();
        expect(within(items[1]).getByText('Version 1')).not.toBeNull();
        expect(within(items[1]).queryByText('Current')).toBeNull();
        expect(within(items[1]).getByText('Initial version')).not.toBeNull();
        expect(within(items[1]).getByText(/^System/)).not.toBeNull();
    });

    test('selecting a version shows its instructions read-only', async () => {
        renderEdit();
        fireEvent.click(screen.getByRole('button', {name: 'History'}));
        const list = await screen.findByTestId('version-list');

        fireEvent.click(within(list).getByText('Version 1'));

        const instructions = await screen.findByTestId('version-snapshot-instructions');
        await waitFor(() => expect(instructions.textContent).toBe('original instructions'));
        expect(instructions.tagName).toBe('PRE');
        expect(screen.getByText('Mock Service')).not.toBeNull();
        expect(screen.getByRole('button', {name: 'Restore this version'})).not.toBeNull();
    });

    test('does not offer restore for the current version', async () => {
        renderEdit();
        fireEvent.click(screen.getByRole('button', {name: 'History'}));
        await screen.findByTestId('version-snapshot');

        expect(screen.queryByRole('button', {name: 'Restore this version'})).toBeNull();
    });

    test('restore confirms, warns about unsaved changes, then resets draft and baseline', async () => {
        const restored: UserAgent = {...existingAgent, displayName: 'Restored Agent', customInstructions: 'original instructions'};
        mockRestoreAgentVersion.mockResolvedValue(restored);
        const {onRestored} = renderEdit();

        fireEvent.change(screen.getByLabelText('Display Name'), {target: {value: 'Unsaved edit'}});
        fireEvent.click(screen.getByRole('button', {name: 'History'}));
        const list = await screen.findByTestId('version-list');
        fireEvent.click(within(list).getByText('Version 1'));
        await waitFor(() => expect((screen.getByRole('button', {name: 'Restore this version'}) as HTMLButtonElement).disabled).toBe(false));

        fireEvent.click(screen.getByRole('button', {name: 'Restore this version'}));
        const dialog = screen.getByRole('dialog', {name: 'Restore this version?'});
        expect(within(dialog).getByText(/unsaved changes in the editor/)).not.toBeNull();
        expect(mockRestoreAgentVersion).not.toHaveBeenCalled();

        mockGetAgentVersions.mockResolvedValue({
            currentVersion: 3,
            versions: [
                {version: 3, createdBy: 'user_1', createAt: 1759700000000, source: 'restore', restoredFromVersion: 1, changedFields: ['customInstructions']},
                ...versionList.versions,
            ],
        });
        fireEvent.click(within(dialog).getByRole('button', {name: 'Restore'}));

        await waitFor(() => expect(mockRestoreAgentVersion).toHaveBeenCalledWith('agent_1', 1));
        await waitFor(() => expect(onRestored).toHaveBeenCalledWith(restored));
        expect(await screen.findByText('Version 1 was restored as the current version.')).not.toBeNull();
        await waitFor(() => expect(screen.getByText('Restored from version 1')).not.toBeNull());

        fireEvent.click(screen.getByRole('button', {name: 'Configuration'}));
        expect((screen.getByLabelText('Display Name') as HTMLInputElement).value).toBe('Restored Agent');
        expect((screen.getByLabelText('Custom instructions') as HTMLTextAreaElement).value).toBe('original instructions');

        // Baseline was reset too: leaving the editor must not prompt to discard.
        fireEvent.keyDown(document, {key: 'Escape'});
        expect(screen.queryByRole('dialog', {name: 'Discard changes?'})).toBeNull();
    });

    test('History tab hides the editor Save and Cancel buttons', async () => {
        renderEdit();
        expect(screen.getByRole('button', {name: 'Save'})).not.toBeNull();

        fireEvent.click(screen.getByRole('button', {name: 'History'}));
        await screen.findByTestId('version-list');
        expect(screen.queryByRole('button', {name: 'Save'})).toBeNull();
        expect(screen.queryByRole('button', {name: 'Cancel'})).toBeNull();

        fireEvent.click(screen.getByRole('button', {name: 'Configuration'}));
        expect(screen.getByRole('button', {name: 'Save'})).not.toBeNull();
        expect(screen.getByRole('button', {name: 'Cancel'})).not.toBeNull();
    });

    test('restore discards a pending avatar upload along with the other edits', async () => {
        mockRestoreAgentVersion.mockResolvedValue({...existingAgent, customInstructions: 'original instructions'});
        renderEdit();

        const avatar = new File(['x'], 'new-avatar.png', {type: 'image/png'});
        fireEvent.change(screen.getByLabelText('Bot avatar'), {target: {files: [avatar]}});
        expect(screen.getByTestId('pending-avatar').textContent).toBe('new-avatar.png');

        fireEvent.click(screen.getByRole('button', {name: 'History'}));
        const list = await screen.findByTestId('version-list');
        fireEvent.click(within(list).getByText('Version 1'));
        await waitFor(() => expect((screen.getByRole('button', {name: 'Restore this version'}) as HTMLButtonElement).disabled).toBe(false));
        fireEvent.click(screen.getByRole('button', {name: 'Restore this version'}));
        const dialog = screen.getByRole('dialog', {name: 'Restore this version?'});
        expect(within(dialog).getByText(/unsaved changes in the editor/)).not.toBeNull();
        fireEvent.click(within(dialog).getByRole('button', {name: 'Restore'}));
        expect(await screen.findByText('Version 1 was restored as the current version.')).not.toBeNull();

        fireEvent.click(screen.getByRole('button', {name: 'Configuration'}));
        expect(screen.getByTestId('pending-avatar').textContent).toBe('');
        fireEvent.keyDown(document, {key: 'Escape'});
        expect(screen.queryByRole('dialog', {name: 'Discard changes?'})).toBeNull();
    });

    test('restore confirmation omits the discard warning when the editor is clean', async () => {
        renderEdit();
        fireEvent.click(screen.getByRole('button', {name: 'History'}));
        const list = await screen.findByTestId('version-list');
        fireEvent.click(within(list).getByText('Version 1'));
        await waitFor(() => expect((screen.getByRole('button', {name: 'Restore this version'}) as HTMLButtonElement).disabled).toBe(false));

        fireEvent.click(screen.getByRole('button', {name: 'Restore this version'}));
        const dialog = screen.getByRole('dialog', {name: 'Restore this version?'});

        expect(within(dialog).queryByText(/unsaved changes/)).toBeNull();
    });

    test('shows the server error when restore fails and leaves the draft alone', async () => {
        mockRestoreAgentVersion.mockRejectedValue({message: 'The AI service for this version no longer exists.'});
        const {onRestored} = renderEdit();
        fireEvent.click(screen.getByRole('button', {name: 'History'}));
        const list = await screen.findByTestId('version-list');
        fireEvent.click(within(list).getByText('Version 1'));
        await waitFor(() => expect((screen.getByRole('button', {name: 'Restore this version'}) as HTMLButtonElement).disabled).toBe(false));

        fireEvent.click(screen.getByRole('button', {name: 'Restore this version'}));
        fireEvent.click(within(screen.getByRole('dialog', {name: 'Restore this version?'})).getByRole('button', {name: 'Restore'}));

        expect(await screen.findByText('The AI service for this version no longer exists.')).not.toBeNull();
        expect(onRestored).not.toHaveBeenCalled();
    });

    test.each([
        {name: 'no versions', list: {currentVersion: 0, versions: []}},
        {name: 'a null version list', list: {currentVersion: 0, versions: null}},
        {name: 'no current version', list: {currentVersion: 0, versions: versionList.versions}},
    ])('History tab shows an empty state and requests no version for $name', async ({list}) => {
        mockGetAgentVersions.mockResolvedValue(list as unknown as AgentVersionList);
        renderEdit();
        fireEvent.click(screen.getByRole('button', {name: 'History'}));

        expect(await screen.findByText('No versions recorded yet. Saving the agent creates the first version.')).not.toBeNull();
        expect(screen.queryByTestId('version-list')).toBeNull();
        expect(mockGetAgentVersion).not.toHaveBeenCalled();
    });

    test.each([
        {name: 'warns and opens the Access tab when restoring attribute-based access the agent no longer has', currentLevel: 0, expectWarning: true},
        {name: 'does not warn when the agent still uses attribute-based access', currentLevel: 4, expectWarning: false},
    ])('restoring an attribute-based version $name', async ({currentLevel, expectWarning}) => {
        const agent: UserAgent = {...existingAgent, userAccessLevel: currentLevel};
        mockGetAgentVersion.mockImplementation(async (_id, version) => {
            const summary = versionList.versions.find((v) => v.version === version)!;
            return {...summary, config: {...agent, userAccessLevel: version === 1 ? 4 : currentLevel}};
        });
        const restored: UserAgent = {...agent, userAccessLevel: 4};
        mockRestoreAgentVersion.mockResolvedValue(restored);
        renderEdit(jest.fn(), agent);

        fireEvent.click(screen.getByRole('button', {name: 'History'}));
        const list = await screen.findByTestId('version-list');
        fireEvent.click(within(list).getByText('Version 1'));
        expect(await screen.findByText('Attribute-based (policy not stored in versions)')).not.toBeNull();
        await waitFor(() => expect((screen.getByRole('button', {name: 'Restore this version'}) as HTMLButtonElement).disabled).toBe(false));

        fireEvent.click(screen.getByRole('button', {name: 'Restore this version'}));
        const dialog = screen.getByRole('dialog', {name: 'Restore this version?'});
        const warning = within(dialog).queryByText(/nobody will be able to use the agent until you create a new policy on the Access tab/);
        expect(warning !== null).toBe(expectWarning);

        fireEvent.click(within(dialog).getByRole('button', {name: 'Restore'}));
        await waitFor(() => expect(mockRestoreAgentVersion).toHaveBeenCalledWith('agent_1', 1));

        if (expectWarning) {
            expect(await screen.findByRole('button', {name: 'Switch user access to everyone'})).not.toBeNull();
            expect(screen.queryByTestId('version-list')).toBeNull();
        } else {
            expect(await screen.findByText('Version 1 was restored as the current version.')).not.toBeNull();
            expect(screen.queryByRole('button', {name: 'Switch user access to everyone'})).toBeNull();
        }
    });

    test('Export downloads the agent file via the export helper', async () => {
        mockDownloadAgentExport.mockImplementation(() => Promise.resolve());
        renderEdit();

        fireEvent.click(screen.getByRole('button', {name: 'Export'}));

        await waitFor(() => expect(mockDownloadAgentExport).toHaveBeenCalledWith('agent_1', 'existingagent'));
    });

    test('Export shows the server error when the download fails', async () => {
        mockDownloadAgentExport.mockRejectedValue({message: 'You do not have permission to export this agent.'});
        renderEdit();

        fireEvent.click(screen.getByRole('button', {name: 'Export'}));

        expect(await screen.findByText('You do not have permission to export this agent.')).not.toBeNull();
    });
});
