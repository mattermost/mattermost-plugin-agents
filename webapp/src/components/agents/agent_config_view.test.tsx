// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor, waitForElementToBeRemoved} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {getUserMCPTools} from '@/client';
import {ServiceInfo, UserAgent} from '@/types/agents';

import AgentConfigView, {AgentDraft} from './agent_config_view';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
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

jest.mock('@/hooks/use_mcp_connection_events', () => ({
    useMCPConnectionEvents: jest.fn(),
}));

jest.mock('@/components/system_console/bot', () => ({
    ChannelAccessLevel: {
        All: 0,
    },
    UserAccessLevel: {
        All: 0,
    },
}));

jest.mock('./tabs/config_tab', () => ({
    __esModule: true,
    default: ({draft, onChange}: {draft: AgentDraft; onChange: (updates: Partial<AgentDraft>) => void}) => (
        <input
            aria-label='Display Name'
            value={draft.displayName}
            onChange={(e) => onChange({displayName: e.target.value})}
        />
    ),
}));

jest.mock('./tabs/access_tab', () => ({
    __esModule: true,
    default: () => null,
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
    // MCP server is temporarily disconnected). Silently reconciling those entries
    // is fine, but it must not be treated as a user edit.
    test('does not prompt to discard when visiting MCP tab with orphaned saved tools and no user edits', async () => {
        (getUserMCPTools as jest.Mock).mockResolvedValue({
            servers: [
                {
                    name: 'Mattermost',
                    serverOrigin: 'embedded://mattermost',
                    authenticated: true,
                    needsOAuth: false,
                    authEmail: '',
                    tools: [
                        {name: 'read_post', description: '', enabled: true, policy: 'auto_run'},
                    ],
                },
            ],
        });

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

            // The persisted enabledMCPTools include an entry that's not present in the
            // live MCP catalog above (orphan). The previous behavior silently mutated
            // draft.enabledTools to drop it, dirtying the form on second open.
            enabledMCPTools: [
                {server_origin: 'embedded://mattermost', tool_name: 'read_post'},
                {server_origin: 'embedded://mattermost', tool_name: 'deleted_tool'},
            ],
            autoEnableNewMCPTools: false,
            reasoningEnabled: true,
            reasoningEffort: 'medium',
            thinkingBudget: 0,
            structuredOutputEnabled: false,
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

        // Open the MCP tab — this mounts McpsTab which fetches the live catalog and
        // historically auto-cleaned orphaned entries, silently mutating draft state.
        fireEvent.click(screen.getByRole('button', {name: 'MCPs'}));

        await waitFor(() => expect(getUserMCPTools).toHaveBeenCalled());

        // Wait until the live MCP catalog has been processed (orphan warning visible).
        await screen.findByText(/from servers that are no longer available/);

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
                        reasoningEnabled: true,
                        reasoningEffort: 'medium',
                        thinkingBudget: 0,
                        structuredOutputEnabled: false,
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
});
