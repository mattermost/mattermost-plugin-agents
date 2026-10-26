// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen, waitFor, waitForElementToBeRemoved, within} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {createAgent, deleteAgentVersions, getAgentVersion, getAgentVersions, getProfilesByIds, getUserMCPTools, restoreAgentVersion, updateAgent} from '@/client';
import {AgentExportDocument, AgentVersionDetail, AgentVersionList, EnabledTool, MaxCustomInstructionsRunes, ServiceInfo, UserAgent} from '@/types/agents';
import {downloadAgentExport} from '@/utils/agent_export';
import {useCurrentUserHasSystemPermission} from '@/utils/permissions';

import AgentConfigView, {AgentDraft} from './agent_config_view';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');

    // Message ids are derived from defaultMessage at build time; mirror that here.
    const real = actual.createIntl({locale: 'en', defaultLocale: 'en', onError: () => null});
    return {
        ...actual,
        useIntl: () => ({
            formatMessage: (descriptor: {id?: string; defaultMessage: string}, values?: Record<string, string | number>) =>
                real.formatMessage({id: descriptor.id ?? descriptor.defaultMessage, ...descriptor}, values),
            formatNumber: (value: number) => new Intl.NumberFormat('en').format(value),
            formatList: (items: string[]) => new Intl.ListFormat('en', {type: 'conjunction'}).format(items),
            formatDate: (value: number) => new Date(value).toISOString().slice(0, 10),
            formatRelativeTime: (value: number, unit: Intl.RelativeTimeFormatUnit, options?: Intl.RelativeTimeFormatOptions) =>
                real.formatRelativeTime(value, unit, options),
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
    deleteAgentVersions: jest.fn(),
    getProfilesByIds: jest.fn(),
}));

jest.mock('@/utils/agent_export', () => ({
    ...jest.requireActual('@/utils/agent_export'),
    downloadAgentExport: jest.fn(),
}));

jest.mock('@/utils/access_control', () => ({
    useABACSupport: () => ({supported: false, loading: false}),
}));

jest.mock('@/hooks/use_mcp_connection_events', () => ({
    useMCPConnectionEvents: jest.fn(),
}));

const mockedUseCurrentUserHasSystemPermission = useCurrentUserHasSystemPermission as unknown as jest.Mock;

async function openHistory() {
    fireEvent.click(await screen.findByRole('button', {name: /^Version history, v/}));
    return screen.findByTestId('version-list');
}

async function closeHistory() {
    fireEvent.click(within(screen.getByRole('dialog', {name: 'Version history'})).getByRole('button', {name: 'Close'}));
    await waitFor(() => expect(screen.queryByTestId('version-list')).toBeNull());
}

async function openVersionMenu(version: number) {
    fireEvent.click(await screen.findByRole('button', {name: `Actions for version ${version}`}));
}

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
            {errors.username && <div>{errors.username}</div>}
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
    const {Combobox} = jest.requireActual('@mattermost/compass-ui/components/combobox');
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
                <Combobox
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
        fireEvent.click(screen.getByRole('tab', {name: 'MCPs'}));
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
            name: 'closing an open picker menu',
            dismissChild: () => {
                const input = screen.getByLabelText('Agent admins');
                fireEvent.keyDown(input, {key: 'ArrowDown'});
                expect(input.getAttribute('aria-expanded')).toBe('true');

                fireEvent.keyDown(input, {key: 'Escape'});
                expect(input.getAttribute('aria-expanded')).toBe('false');
            },
        },
        {
            name: 'cancelling a nested confirmation dialog',
            dismissChild: async () => {
                fireEvent.click(screen.getByRole('button', {name: 'Open nested dialog'}));
                expect(screen.getByRole('dialog', {name: 'Remove access policy?'})).not.toBeNull();

                fireEvent.keyDown(document, {key: 'Escape'});

                // The overlay stays mounted for its exit fade before unmounting.
                await waitFor(() => expect(screen.queryByRole('dialog', {name: 'Remove access policy?'})).toBeNull());
            },
        },
    ])('Escape used for $name stays on the page; a plain Escape still goes back', async ({dismissChild}) => {
        const {onBack} = renderView();
        fireEvent.click(screen.getByRole('tab', {name: 'Access'}));

        await dismissChild();

        expect(onBack).not.toHaveBeenCalled();

        fireEvent.keyDown(document, {key: 'Escape'});

        expect(onBack).toHaveBeenCalledTimes(1);
    });

    test('disabling tools blocks the MCPs tab and leaves it if it was open', () => {
        renderView();

        fireEvent.click(screen.getByRole('tab', {name: 'MCPs'}));
        expect(screen.getByLabelText('Use service accounts')).not.toBeNull();

        fireEvent.click(screen.getByRole('tab', {name: 'Configuration'}));
        fireEvent.click(screen.getByLabelText('Enable Tools'));
        fireEvent.click(screen.getByRole('tab', {name: 'MCPs'}));

        expect(screen.queryByLabelText('Use service accounts')).toBeNull();
        expect(screen.getByRole('tab', {name: 'MCPs'}).getAttribute('title')).toBe('Enable Tools to configure MCP integrations');
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
        fireEvent.click(screen.getByRole('tab', {name: 'MCPs'}));
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

    describe('renaming an agent', () => {
        const renderEdit = (onSaved = jest.fn()) => {
            render(
                <IntlProvider locale='en'>
                    <AgentConfigView
                        mode='edit'
                        agent={savedAgent}
                        services={services}
                        onBack={jest.fn()}
                        onSaved={onSaved}
                    />
                </IntlProvider>,
            );
            return {onSaved};
        };

        test('asks for confirmation and saves the new username only after confirming', async () => {
            mockUpdateAgent.mockResolvedValue({...savedAgent, name: 'renamed'});
            const {onSaved} = renderEdit();

            fireEvent.change(screen.getByLabelText('Username'), {target: {value: 'renamed'}});
            fireEvent.click(screen.getByRole('button', {name: 'Save'}));

            expect(screen.getByRole('dialog', {name: 'Change agent username?'})).not.toBeNull();
            expect(mockUpdateAgent).not.toHaveBeenCalled();

            fireEvent.click(screen.getByRole('button', {name: 'Change username'}));

            await waitFor(() => expect(mockUpdateAgent).toHaveBeenCalledTimes(1));
            expect(mockUpdateAgent).toHaveBeenCalledWith('agent_1', expect.objectContaining({username: 'renamed'}));
            await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
        });

        test('confirming twice while the dialog closes saves only once', async () => {
            mockUpdateAgent.mockReturnValueOnce(new Promise(() => null));
            renderEdit();

            fireEvent.change(screen.getByLabelText('Username'), {target: {value: 'renamed'}});
            fireEvent.click(screen.getByRole('button', {name: 'Save'}));
            const confirm = screen.getByRole('button', {name: 'Change username'});
            fireEvent.click(confirm);
            fireEvent.click(confirm);

            await waitFor(() => expect(mockUpdateAgent).toHaveBeenCalledTimes(1));
            expect(mockUpdateAgent).toHaveBeenCalledTimes(1);
        });

        test('keeping the current username does not save', async () => {
            renderEdit();

            fireEvent.change(screen.getByLabelText('Username'), {target: {value: 'renamed'}});
            fireEvent.click(screen.getByRole('button', {name: 'Save'}));
            fireEvent.click(screen.getByRole('button', {name: 'Keep current username'}));

            await waitForElementToBeRemoved(() => screen.queryByRole('dialog', {name: 'Change agent username?'}));
            expect(mockUpdateAgent).not.toHaveBeenCalled();
            expect((screen.getByLabelText('Username') as HTMLInputElement).value).toBe('renamed');
        });

        test('saves without confirmation when the username is unchanged', async () => {
            mockUpdateAgent.mockResolvedValue(savedAgent);
            renderEdit();

            fireEvent.change(screen.getByLabelText('Display Name'), {target: {value: 'New Display Name'}});
            fireEvent.click(screen.getByRole('button', {name: 'Save'}));

            await waitFor(() => expect(mockUpdateAgent).toHaveBeenCalledTimes(1));
            expect(screen.queryByRole('dialog', {name: 'Change agent username?'})).toBeNull();
        });
    });

    test.each([
        {serviceType: 'anthropic', expected: ''},
        {serviceType: 'openai', expected: 'medium'},
    ])('defaults reasoning effort for a new $serviceType agent to "$expected"', async ({serviceType, expected}) => {
        mockCreateAgent.mockResolvedValue(savedAgent);
        render(
            <IntlProvider locale='en'>
                <AgentConfigView
                    mode='create'
                    services={[{...services[0], type: serviceType}]}
                    onBack={jest.fn()}
                    onSaved={jest.fn()}
                />
            </IntlProvider>,
        );

        fireEvent.change(screen.getByLabelText('Display Name'), {target: {value: 'My Agent'}});
        fireEvent.change(screen.getByLabelText('Username'), {target: {value: 'myagent'}});
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        await waitFor(() => expect(mockCreateAgent).toHaveBeenCalledTimes(1));
        expect(mockCreateAgent).toHaveBeenCalledWith(expect.objectContaining({reasoningEffort: expected}));
    });

    test('keeps an unset Anthropic effort at the model default when saving an existing agent', async () => {
        mockUpdateAgent.mockResolvedValue(savedAgent);
        render(
            <IntlProvider locale='en'>
                <AgentConfigView
                    mode='edit'
                    agent={{...savedAgent, reasoningEffort: ''}}
                    services={[{...services[0], type: 'anthropic'}]}
                    onBack={jest.fn()}
                    onSaved={jest.fn()}
                />
            </IntlProvider>,
        );

        fireEvent.change(screen.getByLabelText('Display Name'), {target: {value: 'Renamed Agent'}});
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        await waitFor(() => expect(mockUpdateAgent).toHaveBeenCalledTimes(1));
        expect(mockUpdateAgent).toHaveBeenCalledWith('agent_1', expect.objectContaining({reasoningEffort: ''}));
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
        fireEvent.click(screen.getByRole('tab', {name: 'Access'}));
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
        ['username_held_by_deactivated_bot', 'This username belongs to a deactivated bot account. Permanently delete that bot account to make the username available.'],
        ['username_taken', 'This username is already taken'],
        ['', 'This username is already taken'],
    ])('shows a translated message on the username field for conflict code %s', async (code, expected) => {
        mockCreateAgent.mockRejectedValue(Object.assign(new Error('username "myagent" is already taken'), {status_code: 409, server_error_id: code}));
        renderView();

        fireEvent.change(screen.getByLabelText('Display Name'), {target: {value: 'My Agent'}});
        fireEvent.change(screen.getByLabelText('Username'), {target: {value: 'myagent'}});
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        expect(await screen.findByText(expected)).not.toBeNull();
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

        fireEvent.click(screen.getByRole('tab', {name: 'MCPs'}));
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
        hasMore: false,
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

    test('offers version history and Export only for existing agents', () => {
        renderView();
        expect(screen.queryByRole('button', {name: /^Version history/})).toBeNull();
        expect(screen.queryByRole('button', {name: 'Export'})).toBeNull();
        expect(screen.queryByRole('tab', {name: 'History'})).toBeNull();
    });

    test('the version history button to the left of Import and Export opens the version history', async () => {
        renderEdit();

        fireEvent.click(await screen.findByRole('button', {name: 'Version history'}));

        expect(await screen.findByTestId('version-list')).not.toBeNull();
        expect(within(screen.getByRole('dialog', {name: 'Version history'})).getByText('Existing Agent', {selector: 'p'})).not.toBeNull();
        expect(within(screen.getByRole('dialog', {name: 'Version history'})).getByRole('button', {name: 'Close'}).getAttribute('title')).toBe('Close');
    });

    test('has no version history button while creating an agent', () => {
        renderView();
        expect(screen.queryByRole('button', {name: 'Version history'})).toBeNull();
    });

    describe('version line', () => {
        let nowSpy: jest.SpyInstance | null = null;

        afterEach(() => {
            nowSpy?.mockRestore();
            nowSpy = null;
        });

        test('shows the current version, its author and relative time next to the title', async () => {
            nowSpy = jest.spyOn(Date, 'now').mockReturnValue(1759600000000 + (2 * 24 * 60 * 60 * 1000));
            renderEdit();

            const button = await screen.findByRole('button', {name: /^Version history, v2/});
            await waitFor(() => expect(button.textContent).toBe('v2 · by Alice Smith, 2d ago'));
            expect(screen.queryByRole('tab', {name: 'History'})).toBeNull();
        });

        test('flags unsaved changes while the editor is dirty', async () => {
            renderEdit();
            const button = await screen.findByRole('button', {name: /^Version history, v2/});
            expect(button.textContent).not.toContain('Unsaved changes');

            fireEvent.change(screen.getByLabelText('Display Name'), {target: {value: 'Unsaved edit'}});

            await waitFor(() => expect(screen.getByRole('button', {name: /^Version history, v2.*Unsaved changes$/}).textContent).toContain('· Unsaved changes'));
        });

        test.each([
            {name: 'no versions', list: {currentVersion: 0, versions: []}},
            {name: 'a null version list', list: {currentVersion: 0, versions: null}},
            {name: 'no current version', list: {currentVersion: 0, versions: versionList.versions}},
        ])('is hidden and requests no version for $name', async ({list}) => {
            mockGetAgentVersions.mockResolvedValue(list as unknown as AgentVersionList);
            renderEdit();

            await waitFor(() => expect(mockGetAgentVersions).toHaveBeenCalled());
            expect(screen.queryByRole('button', {name: /^Version history/})).toBeNull();
            expect(mockGetAgentVersion).not.toHaveBeenCalled();
        });

        test('shows a failed history load with a retry that restores the version line', async () => {
            mockGetAgentVersions.mockRejectedValueOnce(new Error('boom'));
            renderEdit();

            const alert = await screen.findByRole('alert');
            expect(alert.textContent).toContain('boom');
            expect(screen.queryByRole('button', {name: /^Version history/})).toBeNull();

            fireEvent.click(within(alert).getByRole('button', {name: 'Retry'}));

            expect(await screen.findByRole('button', {name: /^Version history, v2/})).not.toBeNull();
            expect(screen.queryByRole('alert')).toBeNull();
        });
    });

    test('selecting another version cancels a pending row-menu restore', async () => {
        renderEdit();
        const list = await openHistory();
        await screen.findByTestId('version-snapshot');

        let resolveVersion1: (d: AgentVersionDetail) => void = jest.fn();
        mockGetAgentVersion.mockImplementation((_id, version) => (
            version === 1 ? new Promise((resolve) => {
                resolveVersion1 = resolve;
            }) : Promise.resolve(versionDetail(2, 'current instructions'))
        ));

        await openVersionMenu(1);
        fireEvent.click(await screen.findByText('Restore this version'));
        fireEvent.click(within(list).getByText('Version 2'));
        resolveVersion1(versionDetail(1, 'original instructions'));

        mockGetAgentVersion.mockImplementation(async (_id, version) => (
            version === 2 ? versionDetail(2, 'current instructions') : versionDetail(1, 'original instructions')
        ));
        fireEvent.click(within(list).getByText('Version 1'));
        await waitFor(() => expect(screen.getByText('original instructions')).not.toBeNull());
        await expect(screen.findByRole('dialog', {name: 'Restore this version?'}, {timeout: 500})).rejects.toThrow();
    });

    test('lists versions newest first with only the author and time on each row', async () => {
        renderEdit();
        const list = await openHistory();
        await waitFor(() => expect(within(list).getByText(/Alice Smith/)).not.toBeNull());

        const items = within(list).getAllByRole('listitem');
        expect(items).toHaveLength(2);
        expect(within(items[0]).getByText('Version 2')).not.toBeNull();
        expect(within(items[0]).getByText('Current')).not.toBeNull();
        expect(within(items[1]).getByText('Version 1')).not.toBeNull();
        expect(within(items[1]).queryByText('Current')).toBeNull();
        expect(within(items[1]).getByText(/^by System · /)).not.toBeNull();

        // What changed lives in the detail column, not in the list.
        expect(within(list).queryByText(/Changed:|Changes:/)).toBeNull();
        expect(within(list).queryByText('Edited')).toBeNull();
        expect(within(list).queryByText('Initial version')).toBeNull();
        expect(screen.queryByTestId('version-changes')).toBeNull();
        expect(screen.getByText(/^Edited · Created by Alice Smith · /)).not.toBeNull();
    });

    describe('changed fields in the detail column', () => {
        const labelOf = (name: string) => screen.getByText(name, {selector: 'dt'});

        test('marks the rows the selected version changed', async () => {
            renderEdit();
            await openHistory();
            await screen.findByTestId('version-snapshot');

            expect(within(labelOf('Custom instructions')).getByText('Changed')).not.toBeNull();
            expect(within(labelOf('Model')).queryByText('Changed')).toBeNull();
            expect(screen.getAllByText('Changed')).toHaveLength(1);
            expect(screen.queryByTestId('version-other-changes')).toBeNull();
        });

        test('marks a row when any setting shown in it changed, and lists settings with no row', async () => {
            mockGetAgentVersion.mockImplementation(async () => ({
                ...versionList.versions[0],
                changedFields: ['userIDs', 'enabledMCPTools', 'name', 'adminUserIDs'],
                config: existingAgent,
            }));
            renderEdit();
            await openHistory();
            await screen.findByTestId('version-snapshot');

            expect(within(labelOf('User access')).getByText('Changed')).not.toBeNull();
            expect(within(labelOf('MCP tools')).getByText('Changed')).not.toBeNull();
            expect(screen.getAllByText('Changed')).toHaveLength(2);
            expect(screen.getByTestId('version-other-changes').textContent).toBe('Also changed: Username, Agent admins');
        });

        test('marks nothing for a version without changes', async () => {
            renderEdit();
            const list = await openHistory();
            fireEvent.click(within(list).getByText('Version 1'));
            await waitFor(() => expect(screen.getByText(/^Initial version · Created by System · /)).not.toBeNull());

            expect(screen.queryByText('Changed')).toBeNull();
        });
    });

    test('the list header singularizes the version count', async () => {
        mockGetAgentVersions.mockResolvedValue({...versionList, versions: [versionList.versions[0]]});
        renderEdit();
        await openHistory();

        expect(screen.getByText('1 version')).not.toBeNull();
    });

    test('the list header shows the version count and swaps to the selection count', async () => {
        renderEdit();
        await openHistory();
        expect(screen.getByText('2 versions')).not.toBeNull();

        fireEvent.click(screen.getByRole('button', {name: 'More version history actions'}));
        fireEvent.click(await screen.findByText('Select multiple versions'));

        expect(screen.queryByText('2 versions')).toBeNull();
        expect(screen.getByText('0 selected')).not.toBeNull();
    });

    test('only previous versions have a row menu', async () => {
        renderEdit();
        await openHistory();

        expect(screen.queryByRole('button', {name: 'Actions for version 2'})).toBeNull();
        expect(screen.getByRole('button', {name: 'Actions for version 1'})).not.toBeNull();
    });

    test('selecting a version shows its instructions read-only', async () => {
        renderEdit();
        const list = await openHistory();

        fireEvent.click(within(list).getByText('Version 1'));

        const instructions = await screen.findByTestId('version-snapshot-instructions');
        await waitFor(() => expect(instructions.textContent).toBe('original instructions'));
        expect(instructions.tagName).not.toBe('TEXTAREA');
        expect(within(screen.getByTestId('version-snapshot')).getByText('Mock Service')).not.toBeNull();
        expect(screen.getByRole('button', {name: 'Restore this version'})).not.toBeNull();
    });

    test('does not offer restore for the current version', async () => {
        renderEdit();
        await openHistory();
        await screen.findByTestId('version-snapshot');

        expect(screen.queryByRole('button', {name: 'Restore this version'})).toBeNull();
    });

    test('restore confirms, warns about unsaved changes, then resets draft and baseline', async () => {
        const restored: UserAgent = {...existingAgent, displayName: 'Restored Agent', customInstructions: 'original instructions'};
        mockRestoreAgentVersion.mockResolvedValue(restored);
        const {onRestored} = renderEdit();

        fireEvent.change(screen.getByLabelText('Display Name'), {target: {value: 'Unsaved edit'}});
        const list = await openHistory();
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
            hasMore: false,
        });
        fireEvent.click(within(dialog).getByRole('button', {name: 'Restore'}));

        await waitFor(() => expect(mockRestoreAgentVersion).toHaveBeenCalledWith('agent_1', 1));
        await waitFor(() => expect(onRestored).toHaveBeenCalledWith(restored));
        expect(await screen.findByText('Version 1 was restored as the current version.')).not.toBeNull();
        await waitFor(() => expect(screen.getByText(/^Restored from version 1 · /)).not.toBeNull());

        await closeHistory();
        expect((screen.getByLabelText('Display Name') as HTMLInputElement).value).toBe('Restored Agent');
        expect((screen.getByLabelText('Custom instructions') as HTMLTextAreaElement).value).toBe('original instructions');

        // Baseline was reset too: leaving the editor must not prompt to discard.
        fireEvent.keyDown(document, {key: 'Escape'});
        expect(screen.queryByRole('dialog', {name: 'Discard changes?'})).toBeNull();
    });

    test('Escape closes the history modal without prompting to discard editor changes', async () => {
        renderEdit();
        fireEvent.change(screen.getByLabelText('Display Name'), {target: {value: 'Unsaved edit'}});
        await openHistory();

        fireEvent.keyDown(screen.getByRole('dialog', {name: 'Version history'}), {key: 'Escape'});

        await waitFor(() => expect(screen.queryByTestId('version-list')).toBeNull());
        expect(screen.queryByRole('dialog', {name: 'Discard changes?'})).toBeNull();
        expect((screen.getByLabelText('Display Name') as HTMLInputElement).value).toBe('Unsaved edit');
    });

    test('restore discards a pending avatar upload along with the other edits', async () => {
        mockRestoreAgentVersion.mockResolvedValue({...existingAgent, customInstructions: 'original instructions'});
        renderEdit();

        const avatar = new File(['x'], 'new-avatar.png', {type: 'image/png'});
        fireEvent.change(screen.getByLabelText('Bot avatar'), {target: {files: [avatar]}});
        expect(screen.getByTestId('pending-avatar').textContent).toBe('new-avatar.png');

        const list = await openHistory();
        fireEvent.click(within(list).getByText('Version 1'));
        await waitFor(() => expect((screen.getByRole('button', {name: 'Restore this version'}) as HTMLButtonElement).disabled).toBe(false));
        fireEvent.click(screen.getByRole('button', {name: 'Restore this version'}));
        const dialog = screen.getByRole('dialog', {name: 'Restore this version?'});
        expect(within(dialog).getByText(/unsaved changes in the editor/)).not.toBeNull();
        fireEvent.click(within(dialog).getByRole('button', {name: 'Restore'}));
        expect(await screen.findByText('Version 1 was restored as the current version.')).not.toBeNull();

        await closeHistory();
        expect(screen.getByTestId('pending-avatar').textContent).toBe('');
        fireEvent.keyDown(document, {key: 'Escape'});
        expect(screen.queryByRole('dialog', {name: 'Discard changes?'})).toBeNull();
    });

    test('restore confirmation omits the discard warning when the editor is clean', async () => {
        renderEdit();
        const list = await openHistory();
        fireEvent.click(within(list).getByText('Version 1'));
        await waitFor(() => expect((screen.getByRole('button', {name: 'Restore this version'}) as HTMLButtonElement).disabled).toBe(false));

        fireEvent.click(screen.getByRole('button', {name: 'Restore this version'}));
        const dialog = screen.getByRole('dialog', {name: 'Restore this version?'});

        expect(within(dialog).queryByText(/unsaved changes/)).toBeNull();
    });

    test('Escape closes an open row menu first and the modal only on the next press', async () => {
        renderEdit();
        await openHistory();

        await openVersionMenu(1);
        const menu = await screen.findByTestId('dropdownmenu');
        fireEvent.keyDown(menu, {key: 'Escape'});

        await waitFor(() => expect(document.querySelector('[data-open-popup]')).toBeNull());
        expect(screen.queryByTestId('version-list')).not.toBeNull();

        fireEvent.keyDown(screen.getByTestId('version-list'), {key: 'Escape'});
        await waitFor(() => expect(screen.queryByTestId('version-list')).toBeNull());
    });

    test('restore confirmation lists the settings it will change', async () => {
        renderEdit();
        const list = await openHistory();
        fireEvent.click(within(list).getByText('Version 1'));
        await waitFor(() => expect((screen.getByRole('button', {name: 'Restore this version'}) as HTMLButtonElement).disabled).toBe(false));

        fireEvent.click(screen.getByRole('button', {name: 'Restore this version'}));
        const dialog = screen.getByRole('dialog', {name: 'Restore this version?'});

        const changes = await within(dialog).findByTestId('restore-changes');
        expect(within(changes).getAllByRole('listitem').map((el) => el.textContent)).toEqual(['Custom instructions']);
        expect(mockGetAgentVersion).toHaveBeenCalledWith('agent_1', 2);
    });

    test('restore confirmation says when the version matches the current settings', async () => {
        mockGetAgentVersion.mockImplementation(async (_id, version) => versionDetail(version, 'current instructions'));
        renderEdit();
        const list = await openHistory();
        fireEvent.click(within(list).getByText('Version 1'));
        await waitFor(() => expect((screen.getByRole('button', {name: 'Restore this version'}) as HTMLButtonElement).disabled).toBe(false));

        fireEvent.click(screen.getByRole('button', {name: 'Restore this version'}));
        const dialog = screen.getByRole('dialog', {name: 'Restore this version?'});

        expect(await within(dialog).findByText('Its settings are the same as the current version.')).not.toBeNull();
        expect(within(dialog).queryByTestId('restore-changes')).toBeNull();
    });

    test('restore confirmation does not list the username, which a restore never changes', async () => {
        mockGetAgentVersion.mockImplementation(async (_id, version) => {
            const detail = versionDetail(version, 'current instructions');
            return {...detail, config: {...detail.config, name: version === 1 ? 'oldusername' : 'newusername'}};
        });
        renderEdit();
        const list = await openHistory();
        fireEvent.click(within(list).getByText('Version 1'));
        await waitFor(() => expect((screen.getByRole('button', {name: 'Restore this version'}) as HTMLButtonElement).disabled).toBe(false));

        fireEvent.click(screen.getByRole('button', {name: 'Restore this version'}));
        const dialog = screen.getByRole('dialog', {name: 'Restore this version?'});

        expect(await within(dialog).findByText('Its settings are the same as the current version.')).not.toBeNull();
        expect(within(dialog).queryByText('Username')).toBeNull();
    });

    test('restores a version from its row menu after confirming', async () => {
        mockRestoreAgentVersion.mockResolvedValue({...existingAgent, customInstructions: 'original instructions'});
        renderEdit();
        await openHistory();

        await openVersionMenu(1);
        fireEvent.click(await screen.findByText('Restore this version'));

        const dialog = await screen.findByRole('dialog', {name: 'Restore this version?'});
        expect(await within(dialog).findByTestId('restore-changes')).not.toBeNull();
        expect(mockRestoreAgentVersion).not.toHaveBeenCalled();

        fireEvent.click(within(dialog).getByRole('button', {name: 'Restore'}));
        await waitFor(() => expect(mockRestoreAgentVersion).toHaveBeenCalledWith('agent_1', 1));
        expect(await screen.findByText('Version 1 was restored as the current version.')).not.toBeNull();
    });

    test('shows the server error when restore fails and leaves the draft alone', async () => {
        mockRestoreAgentVersion.mockRejectedValue({message: 'The AI service for this version no longer exists.'});
        const {onRestored} = renderEdit();
        const list = await openHistory();
        fireEvent.click(within(list).getByText('Version 1'));
        await waitFor(() => expect((screen.getByRole('button', {name: 'Restore this version'}) as HTMLButtonElement).disabled).toBe(false));

        fireEvent.click(screen.getByRole('button', {name: 'Restore this version'}));
        fireEvent.click(within(screen.getByRole('dialog', {name: 'Restore this version?'})).getByRole('button', {name: 'Restore'}));

        expect(await screen.findByText('The AI service for this version no longer exists.')).not.toBeNull();
        expect(onRestored).not.toHaveBeenCalled();
    });

    test.each([
        {name: 'more versions exist', hasMore: true},
        {name: 'all versions are loaded', hasMore: false},
    ])('version history offers older versions only when $name', async ({hasMore}) => {
        mockGetAgentVersions.mockResolvedValue({...versionList, hasMore});
        renderEdit();
        await openHistory();

        expect(mockGetAgentVersions).toHaveBeenCalledWith('agent_1');
        expect(screen.queryByRole('button', {name: 'Load older versions'}) !== null).toBe(hasMore);
    });

    describe('paging older versions', () => {
        const summary = (version: number) => ({
            version,
            createdBy: '',
            createAt: 1759500000000 + version,
            source: 'update' as const,
            restoredFromVersion: 0,
            changedFields: ['customInstructions'],
        });
        const firstPage: AgentVersionList = {currentVersion: 4, versions: [summary(4), summary(3)], hasMore: true};
        const lastPage: AgentVersionList = {currentVersion: 4, versions: [summary(2), summary(1)], hasMore: false};

        beforeEach(() => {
            mockGetAgentVersion.mockImplementation(async (_id, version) => ({
                ...summary(version),
                config: {...existingAgent, customInstructions: `instructions ${version}`},
            }));
        });

        test('appends the page before the oldest loaded version until none are left', async () => {
            mockGetAgentVersions.mockResolvedValueOnce(firstPage).mockResolvedValueOnce(lastPage);
            renderEdit();
            const list = await openHistory();
            expect(within(list).getAllByRole('listitem')).toHaveLength(2);

            fireEvent.click(screen.getByRole('button', {name: 'Load older versions'}));

            await waitFor(() => expect(within(list).getAllByRole('listitem')).toHaveLength(4));
            expect(mockGetAgentVersions).toHaveBeenLastCalledWith('agent_1', {before: 3});
            const titles = within(list).getAllByText(/^Version \d$/).map((el) => el.firstChild?.textContent);
            expect(titles).toEqual(['Version 4', 'Version 3', 'Version 2', 'Version 1']);
            expect(within(list).getAllByText('Current')).toHaveLength(1);
            expect(screen.queryByRole('button', {name: 'Load older versions'})).toBeNull();

            fireEvent.click(within(list).getByText('Version 1'));
            const instructions = await screen.findByTestId('version-snapshot-instructions');
            await waitFor(() => expect(instructions.textContent).toBe('instructions 1'));
            expect(screen.getByRole('button', {name: 'Restore this version'})).not.toBeNull();
        });

        test('drops an older page requested before a restore reloaded the list', async () => {
            let resolveReload: (list: AgentVersionList) => void = jest.fn();
            let resolveOlder: (list: AgentVersionList) => void = jest.fn();
            mockGetAgentVersions.
                mockResolvedValueOnce(firstPage).
                mockImplementationOnce(() => new Promise((resolve) => {
                    resolveReload = resolve;
                })).
                mockImplementationOnce(() => new Promise((resolve) => {
                    resolveOlder = resolve;
                }));
            mockRestoreAgentVersion.mockResolvedValue(existingAgent);
            renderEdit();
            const list = await openHistory();

            fireEvent.click(within(list).getByText('Version 3'));
            await waitFor(() => expect((screen.getByRole('button', {name: 'Restore this version'}) as HTMLButtonElement).disabled).toBe(false));
            fireEvent.click(screen.getByRole('button', {name: 'Restore this version'}));
            fireEvent.click(within(screen.getByRole('dialog', {name: 'Restore this version?'})).getByRole('button', {name: 'Restore'}));
            await waitFor(() => expect(mockGetAgentVersions).toHaveBeenCalledTimes(2));

            // The pre-restore list is still on screen while the reload is pending.
            fireEvent.click(screen.getByRole('button', {name: 'Load older versions'}));
            expect(mockGetAgentVersions).toHaveBeenLastCalledWith('agent_1', {before: 3});

            await act(async () => resolveReload({currentVersion: 5, versions: [summary(5), summary(4)], hasMore: true}));
            await act(async () => resolveOlder(lastPage));

            const titles = within(screen.getByTestId('version-list')).getAllByText(/^Version \d$/).map((el) => el.firstChild?.textContent);
            expect(titles).toEqual(['Version 5', 'Version 4']);
            expect(screen.getByRole('button', {name: 'Load older versions'})).not.toBeNull();
        });

        test('keeps the loaded versions and the button when an older page fails', async () => {
            mockGetAgentVersions.mockResolvedValueOnce(firstPage).mockRejectedValueOnce({message: 'Server unavailable'});
            renderEdit();
            const list = await openHistory();

            fireEvent.click(screen.getByRole('button', {name: 'Load older versions'}));

            expect(await screen.findByText('Server unavailable')).not.toBeNull();
            expect(within(list).getAllByRole('listitem')).toHaveLength(2);
            expect(screen.getByRole('button', {name: 'Load older versions'})).not.toBeNull();
        });
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

        const list = await openHistory();
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

            // The modal closes so the Access tab is in view.
            await waitFor(() => expect(screen.queryByTestId('version-list')).toBeNull());
        } else {
            expect(await screen.findByText('Version 1 was restored as the current version.')).not.toBeNull();
            expect(screen.queryByRole('button', {name: 'Switch user access to everyone'})).toBeNull();
        }
    });

    test('Export downloads the agent file via the export helper', async () => {
        mockDownloadAgentExport.mockImplementation(() => Promise.resolve());
        renderEdit();

        fireEvent.click(screen.getByRole('button', {name: 'Export'}));

        await waitFor(() => expect(mockDownloadAgentExport).toHaveBeenCalledWith('agent_1'));
    });

    test('Export shows the server error when the download fails', async () => {
        mockDownloadAgentExport.mockRejectedValue({message: 'You do not have permission to export this agent.'});
        renderEdit();

        fireEvent.click(screen.getByRole('button', {name: 'Export'}));

        expect(await screen.findByText('You do not have permission to export this agent.')).not.toBeNull();
    });
});

describe('AgentConfigView import from file', () => {
    const mockGetUserMCPTools = getUserMCPTools as jest.MockedFunction<typeof getUserMCPTools>;

    const exported: AgentExportDocument = {
        kind: 'mattermost-agent',
        schemaVersion: 1,
        exportedAt: 1,
        agentVersion: 4,
        agent: {
            name: 'releasehelper',
            displayName: 'Release Helper',
            customInstructions: 'Imported instructions',
            disableTools: false,
            maxToolTurns: 12,
            mcpDynamicToolLoading: false,
            autoEnableNewMCPTools: false,
            mcpTools: [
                {serverOrigin: 'https://jira.example.com/mcp', toolName: 'search'},
                {serverOrigin: 'https://linear.example.com', toolName: 'issues'},
            ],
        },
    };

    const existingAgent: UserAgent = {...savedAgent, name: 'existingagent', displayName: 'Existing Agent', customInstructions: 'old instructions', autoEnableNewMCPTools: true};

    function renderWith(props: Partial<React.ComponentProps<typeof AgentConfigView>>) {
        return render(
            <IntlProvider locale='en'>
                <AgentConfigView
                    mode='create'
                    services={services}
                    onBack={jest.fn()}
                    onSaved={jest.fn()}
                    {...props}
                />
            </IntlProvider>,
        );
    }

    function pickFile(content: unknown) {
        const file = new File([JSON.stringify(content)], 'agent.json', {type: 'application/json'});
        fireEvent.change(screen.getByTestId('agent-import-file-input'), {target: {files: [file]}});
    }

    beforeEach(() => {
        jest.clearAllMocks();
        mockedUseCurrentUserHasSystemPermission.mockReturnValue(true);
        mockGetUserMCPTools.mockResolvedValue({servers: [
            {name: 'Jira', serverOrigin: 'https://jira.example.com/mcp/', tools: []},
        ]} as never);
    });

    test('a file opened from the Agents list fills the new agent and saves through create', async () => {
        mockCreateAgent.mockResolvedValue({...savedAgent, id: 'agent_new'});
        renderWith({importedDocument: exported});

        expect(await screen.findByText(/Tools from MCP servers that are not available here were left out: https:\/\/linear.example.com/)).not.toBeNull();
        expect((screen.getByLabelText('Username') as HTMLInputElement).value).toBe('releasehelper');
        expect((screen.getByLabelText('Display Name') as HTMLInputElement).value).toBe('Release Helper');
        expect((screen.getByLabelText('Custom instructions') as HTMLTextAreaElement).value).toBe('Imported instructions');
        expect(mockGetUserMCPTools).toHaveBeenCalledWith({serviceAccount: false});

        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        await waitFor(() => expect(mockCreateAgent).toHaveBeenCalledTimes(1));
        expect(mockCreateAgent.mock.calls[0][0]).toEqual(expect.objectContaining({
            username: 'releasehelper',
            displayName: 'Release Helper',
            serviceID: 'svc_1',
            customInstructions: 'Imported instructions',
            maxToolTurns: 12,
            mcpDynamicToolLoading: false,
            autoEnableNewMCPTools: false,
            enabledMCPTools: [{server_origin: 'https://jira.example.com/mcp/', tool_name: 'search'}],
        }));
    });

    test('importing into an existing agent replaces only the transferable settings', async () => {
        mockUpdateAgent.mockResolvedValue(existingAgent);
        renderWith({mode: 'edit', agent: existingAgent});

        pickFile(exported);

        await waitFor(() => expect((screen.getByLabelText('Custom instructions') as HTMLTextAreaElement).value).toBe('Imported instructions'));
        expect((screen.getByLabelText('Username') as HTMLInputElement).value).toBe('existingagent');
        expect((screen.getByLabelText('Display Name') as HTMLInputElement).value).toBe('Existing Agent');
        expect(mockGetUserMCPTools).toHaveBeenCalledWith({agentId: 'agent_1', serviceAccount: false});
        expect(mockUpdateAgent).not.toHaveBeenCalled();

        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        await waitFor(() => expect(mockUpdateAgent).toHaveBeenCalledTimes(1));
        expect(mockUpdateAgent.mock.calls[0][1]).toEqual(expect.objectContaining({
            username: 'existingagent',
            displayName: 'Existing Agent',
            customInstructions: 'Imported instructions',
            autoEnableNewMCPTools: false,
            enabledMCPTools: [{server_origin: 'https://jira.example.com/mcp/', tool_name: 'search'}],
        }));
    });

    test('a file that enables every MCP tool does not look up MCP servers', async () => {
        renderWith({importedDocument: {...exported, agent: {...exported.agent, autoEnableNewMCPTools: true}}});

        expect(await screen.findByText('Settings loaded from the agent file')).not.toBeNull();
        expect(screen.getByText('Review the settings, then save to apply them.')).not.toBeNull();
        expect(mockGetUserMCPTools).not.toHaveBeenCalled();
    });

    test('leaves the form unchanged when MCP servers cannot be loaded', async () => {
        mockGetUserMCPTools.mockRejectedValue(new Error('offline'));
        renderWith({mode: 'edit', agent: existingAgent});

        pickFile(exported);

        expect(await screen.findByText('Failed to load your MCP servers, so the agent file was not imported. Please try again.')).not.toBeNull();
        expect((screen.getByLabelText('Custom instructions') as HTMLTextAreaElement).value).toBe('old instructions');
    });

    test('shows why a file cannot be imported', async () => {
        renderWith({mode: 'edit', agent: existingAgent});

        pickFile({kind: 'something-else'});

        expect(await screen.findByText('This file is not a valid Mattermost agent export.')).not.toBeNull();
        expect(mockGetUserMCPTools).not.toHaveBeenCalled();
    });
});

describe('AgentConfigView history deletion', () => {
    const mockGetAgentVersions = getAgentVersions as jest.MockedFunction<typeof getAgentVersions>;
    const mockGetAgentVersion = getAgentVersion as jest.MockedFunction<typeof getAgentVersion>;
    const mockDeleteAgentVersions = deleteAgentVersions as jest.MockedFunction<typeof deleteAgentVersions>;
    const mockGetProfilesByIds = getProfilesByIds as jest.MockedFunction<typeof getProfilesByIds>;

    const summary = (version: number) => ({version, createdBy: '', createAt: 1759500000000 + version, source: 'update' as const, restoredFromVersion: 0, changedFields: []});
    const listOf = (...versions: number[]): AgentVersionList => ({currentVersion: 3, versions: versions.map(summary), hasMore: false});

    async function renderHistory() {
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
        return openHistory();
    }

    async function confirmDelete() {
        const dialog = await screen.findByRole('dialog', {name: /^Delete /});
        fireEvent.click(within(dialog).getByRole('button', {name: 'Delete'}));
    }

    beforeEach(() => {
        jest.clearAllMocks();
        mockedUseCurrentUserHasSystemPermission.mockReturnValue(true);
        mockGetAgentVersions.mockResolvedValueOnce(listOf(3, 2, 1));
        mockGetAgentVersion.mockImplementation(async (_id, version) => ({...summary(version), config: {...savedAgent, customInstructions: `v${version}`}}));
        mockGetProfilesByIds.mockResolvedValue([]);
    });

    test('deletes a version from its row menu and reloads the list', async () => {
        mockDeleteAgentVersions.mockResolvedValue(1);
        mockGetAgentVersions.mockResolvedValue(listOf(3, 2));
        const list = await renderHistory();

        expect(screen.queryByRole('button', {name: 'Delete'})).toBeNull();
        expect(screen.queryByRole('button', {name: 'Actions for version 3'})).toBeNull();
        expect(within(list).queryByRole('checkbox')).toBeNull();

        await openVersionMenu(1);
        fireEvent.click(await screen.findByText('Delete this version'));
        expect(within(await screen.findByRole('dialog', {name: 'Delete version 1?'})).getByText(/Version 1 will be permanently deleted/)).not.toBeNull();
        expect(mockDeleteAgentVersions).not.toHaveBeenCalled();
        await confirmDelete();

        await waitFor(() => expect(mockDeleteAgentVersions).toHaveBeenCalledWith('agent_1', [1]));
        await waitFor(() => expect(within(list).getAllByRole('listitem')).toHaveLength(2));
        expect(mockGetAgentVersions).toHaveBeenCalledTimes(2);
        expect(await screen.findByText('Deleted 1 version.')).not.toBeNull();
    });

    test('selects multiple versions from the list menu and deletes them', async () => {
        mockDeleteAgentVersions.mockResolvedValue(2);
        mockGetAgentVersions.mockResolvedValue(listOf(3));
        const list = await renderHistory();

        expect(within(list).queryAllByRole('checkbox')).toHaveLength(0);
        fireEvent.click(screen.getByRole('button', {name: 'More version history actions'}));
        fireEvent.click(await screen.findByText('Select multiple versions'));

        // The current version is the live configuration, so it has no checkbox.
        expect(within(list).getAllByRole('checkbox')).toHaveLength(2);
        expect(within(list).queryByRole('checkbox', {name: 'Select version 3'})).toBeNull();
        expect(screen.queryByRole('button', {name: 'Actions for version 1'})).toBeNull();
        expect((screen.getByRole('button', {name: 'Delete selected'}) as HTMLButtonElement).disabled).toBe(true);

        fireEvent.click(within(list).getByRole('checkbox', {name: 'Select version 2'}));
        fireEvent.click(within(list).getByRole('checkbox', {name: 'Select version 1'}));
        expect(screen.getByText('2 selected')).not.toBeNull();
        fireEvent.click(screen.getByRole('button', {name: 'Delete selected'}));
        expect(within(await screen.findByRole('dialog', {name: /^Delete /})).getByText(/Versions 1 and 2 will be permanently deleted/)).not.toBeNull();
        expect(mockDeleteAgentVersions).not.toHaveBeenCalled();
        await confirmDelete();

        await waitFor(() => expect(mockDeleteAgentVersions).toHaveBeenCalledWith('agent_1', [1, 2]));
        await waitFor(() => expect(within(list).getAllByRole('listitem')).toHaveLength(1));
        expect(within(list).queryAllByRole('checkbox')).toHaveLength(0);
        expect(screen.queryByRole('button', {name: 'More version history actions'})).toBeNull();
    });

    test('Escape leaves select-multiple mode first and closes the modal on the next press', async () => {
        const list = await renderHistory();

        fireEvent.click(screen.getByRole('button', {name: 'More version history actions'}));
        fireEvent.click(await screen.findByText('Select multiple versions'));
        await waitFor(() => expect(document.querySelector('[data-open-popup]')).toBeNull());
        fireEvent.click(within(list).getByRole('checkbox', {name: 'Select version 1'}));

        fireEvent.keyDown(list, {key: 'Escape'});
        expect(within(list).queryAllByRole('checkbox')).toHaveLength(0);
        expect(screen.queryByTestId('version-list')).not.toBeNull();
        expect(mockDeleteAgentVersions).not.toHaveBeenCalled();

        fireEvent.keyDown(list, {key: 'Escape'});
        await waitFor(() => expect(screen.queryByTestId('version-list')).toBeNull());
    });

    test('cancelling selection leaves the versions alone', async () => {
        const list = await renderHistory();

        fireEvent.click(screen.getByRole('button', {name: 'More version history actions'}));
        fireEvent.click(await screen.findByText('Select multiple versions'));
        fireEvent.click(within(list).getByRole('checkbox', {name: 'Select version 1'}));
        fireEvent.click(within(screen.getByRole('dialog', {name: 'Version history'})).getByRole('button', {name: 'Cancel'}));

        expect(within(list).queryAllByRole('checkbox')).toHaveLength(0);
        expect(screen.getByRole('button', {name: 'Actions for version 1'})).not.toBeNull();
        expect(mockDeleteAgentVersions).not.toHaveBeenCalled();
    });

    test('deletes every previous version from the modal menu', async () => {
        mockDeleteAgentVersions.mockResolvedValue(2);
        mockGetAgentVersions.mockResolvedValue(listOf(3));
        const list = await renderHistory();

        fireEvent.click(screen.getByRole('button', {name: 'More version history actions'}));
        fireEvent.click(await screen.findByText('Delete all previous versions'));
        expect(await screen.findByText('Delete all previous versions?')).not.toBeNull();
        expect(mockDeleteAgentVersions).not.toHaveBeenCalled();
        await confirmDelete();

        await waitFor(() => expect(mockDeleteAgentVersions).toHaveBeenCalledWith('agent_1', 'all'));
        await waitFor(() => expect(within(list).getAllByRole('listitem')).toHaveLength(1));

        // Nothing is left to delete, so the menu goes away.
        expect(screen.queryByRole('button', {name: 'More version history actions'})).toBeNull();
    });

    test('deleting the viewed version falls back to the current version', async () => {
        mockDeleteAgentVersions.mockResolvedValue(1);
        mockGetAgentVersions.mockResolvedValue(listOf(3, 2));
        const list = await renderHistory();

        fireEvent.click(within(list).getByText('Version 1'));
        await waitFor(() => expect(mockGetAgentVersion).toHaveBeenCalledWith('agent_1', 1));
        await openVersionMenu(1);
        fireEvent.click(await screen.findByText('Delete this version'));
        await confirmDelete();

        await waitFor(() => expect(mockDeleteAgentVersions).toHaveBeenCalledWith('agent_1', [1]));
        await waitFor(() => expect(screen.queryByRole('button', {name: 'Restore this version'})).toBeNull());
        expect(mockGetAgentVersion).toHaveBeenLastCalledWith('agent_1', 3);
    });

    test('shows the server error and keeps the list', async () => {
        mockDeleteAgentVersions.mockRejectedValue({message: 'the current version of an agent cannot be deleted'});
        const list = await renderHistory();

        await openVersionMenu(1);
        fireEvent.click(await screen.findByText('Delete this version'));
        await confirmDelete();

        expect(await screen.findByText('the current version of an agent cannot be deleted')).not.toBeNull();

        // The confirmation dialog is portaled to <body>, so the page behind it is aria-hidden.
        expect(within(list).getAllByRole('listitem', {hidden: true})).toHaveLength(3);
        expect(mockGetAgentVersions).toHaveBeenCalledTimes(1);
    });
});
