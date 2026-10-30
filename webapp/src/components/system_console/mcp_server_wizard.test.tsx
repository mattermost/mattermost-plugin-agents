// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen, waitFor, within} from '@testing-library/react';

// Minimal react-intl shim — see mcp_tools_viewer.test.tsx for rationale.
jest.mock('react-intl', () => {
    const React = require('react'); // eslint-disable-line @typescript-eslint/no-shadow, no-shadow, global-require

    const interpolate = (msg: string, values?: Record<string, unknown>) => {
        if (!values) {
            return msg;
        }
        return msg.replace(/{(\w+)}/g, (_, k) => String(values[k] ?? ''));
    };

    return {
        __esModule: true,
        IntlProvider: ({children}: {children: React.ReactNode}) => React.createElement(React.Fragment, null, children),
        FormattedMessage: ({defaultMessage, values}: {defaultMessage?: string; values?: Record<string, unknown>}) =>
            React.createElement(React.Fragment, null, interpolate(defaultMessage ?? '', values)),
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage?: string}, values?: Record<string, unknown>) =>
                interpolate(defaultMessage ?? '', values),
        }),
    };
});

jest.mock('@/license', () => ({
    useIsLicensedFor: jest.fn(() => true),
    useLicenseLevelName: jest.fn(() => () => 'Enterprise'),
    requiredLevelFor: jest.fn(() => 2),
}));

jest.mock('../../client', () => ({
    getMCPServerTools: jest.fn(),
}));

/* eslint-disable import/first */
import {IntlProvider} from 'react-intl';

import {getMCPServerTools} from '../../client';
import {notifyMCPConnectionUpdated} from '../../hooks/use_mcp_connection_events';
import {chooseOption, getSelect, selectedLabel} from '../../../tests/compass_select';

import MCPServerWizard, {MCPServerWizardMode} from './mcp_server_wizard';
import {MCPServerConfig, MCPServerInfo} from './mcp_types';
/* eslint-enable import/first */

const mockGetMCPServerTools = getMCPServerTools as jest.Mock;

const SERVER_ID = 'srv1234567890abcdefghijklm';

function discoveredServer(overrides: Partial<MCPServerInfo> = {}): MCPServerInfo {
    return {
        name: 'Tracker',
        url: 'https://mcp.example.com/mcp',
        serverType: 'remote',
        enabled: true,
        id: SERVER_ID,
        needsOAuth: false,
        error: null,
        tools: [
            {name: 'list_issues', description: 'List issues', inputSchema: null, readOnlyHint: true, defaultPolicy: 'ask', suggestedPolicy: 'auto_run_in_dm'},
            {name: 'delete_issue', description: 'Delete an issue', inputSchema: null, defaultPolicy: 'ask', suggestedPolicy: 'ask'},
        ],
        ...overrides,
    };
}

function renderWizard(mode: MCPServerWizardMode, existingNames: string[] = []) {
    const onPersist = jest.fn(async (server: MCPServerConfig) => ({...server, id: server.id ?? SERVER_ID}));
    const onClose = jest.fn();
    render(
        <IntlProvider locale='en'>
            <MCPServerWizard
                mode={mode}
                existingNames={existingNames}
                onPersist={onPersist}
                onClose={onClose}
            />
        </IntlProvider>,
    );
    return {onPersist, onClose};
}

function fillDetails(name: string, url: string) {
    fireEvent.change(screen.getByLabelText('Name'), {target: {value: name}});
    fireEvent.change(screen.getByLabelText('Server URL'), {target: {value: url}});
}

describe('MCPServerWizard add flow', () => {
    beforeEach(() => {
        jest.clearAllMocks();
    });

    test('saves the server, connects, and saves an explicit policy for every reviewed tool', async () => {
        mockGetMCPServerTools.mockResolvedValue(discoveredServer());
        const {onPersist, onClose} = renderWizard({kind: 'add'});

        fillDetails('Tracker', 'https://mcp.example.com/mcp');
        fireEvent.click(screen.getByRole('button', {name: 'Save and connect'}));

        await waitFor(() => expect(onPersist).toHaveBeenCalledTimes(1));
        const [created, createdToolConfigsOnly] = onPersist.mock.calls[0] as unknown as [MCPServerConfig, boolean | undefined];
        expect(created).toEqual(expect.objectContaining({name: 'Tracker', baseURL: 'https://mcp.example.com/mcp', enabled: true}));
        expect(created.id).toBeUndefined();
        expect(created.tool_configs).toBeUndefined();
        expect(createdToolConfigsOnly).toBeFalsy();
        expect(mockGetMCPServerTools).toHaveBeenCalledWith(SERVER_ID);

        // Tools are pre-set to the suggested policies, grouped by the hint.
        const readGroup = await screen.findByRole('group', {name: 'Read-only tools'});
        expect(within(readGroup).getByText('list_issues')).not.toBeNull();
        expect(selectedLabel(getSelect('Approval policy for list_issues'))).toBe('Auto Run (DM)');
        expect(selectedLabel(getSelect('Approval policy for delete_issue'))).toBe('Ask Every Time');

        chooseOption(getSelect('Approval policy for delete_issue'), 'Auto Run (DM)');
        fireEvent.click(screen.getByRole('button', {name: 'Accept and save'}));

        await waitFor(() => expect(onClose).toHaveBeenCalled());
        expect(onPersist).toHaveBeenCalledTimes(2);
        const [reviewed, toolConfigsOnly] = onPersist.mock.calls[1] as unknown as [MCPServerConfig, boolean];
        expect(toolConfigsOnly).toBe(true);
        expect(reviewed.id).toBe(SERVER_ID);
        expect(reviewed.tool_configs).toEqual([
            {name: 'list_issues', policy: 'auto_run_in_dm', enabled: true},
            {name: 'delete_issue', policy: 'auto_run_in_dm', enabled: true},
        ]);
    });

    test('runs the OAuth sign-in and lists the tools once it finishes', async () => {
        mockGetMCPServerTools.
            mockResolvedValueOnce(discoveredServer({needsOAuth: true, oauthURL: 'https://idp.example.com/start', tools: []})).
            mockResolvedValueOnce(discoveredServer());
        const open = jest.spyOn(window, 'open').mockReturnValue(null);
        renderWizard({kind: 'add'});

        fillDetails('Tracker', 'https://mcp.example.com/mcp');
        fireEvent.click(screen.getByRole('button', {name: 'Save and connect'}));

        fireEvent.click(await screen.findByRole('button', {name: 'Sign in'}));
        expect(open).toHaveBeenCalledWith('https://idp.example.com/start', '_blank', 'noopener,noreferrer');

        act(() => {
            notifyMCPConnectionUpdated({status: 'connected', serverName: 'Tracker'});
        });

        expect(await screen.findByRole('group', {name: 'Read-only tools'})).not.toBeNull();
        expect(mockGetMCPServerTools).toHaveBeenCalledTimes(2);
        open.mockRestore();
    });

    test('shows a connection error and lets the admin fix the details', async () => {
        mockGetMCPServerTools.mockResolvedValue(discoveredServer({error: 'could not connect to the MCP server', tools: []}));
        const {onPersist} = renderWizard({kind: 'add'});

        fillDetails('Tracker', 'https://mcp.example.com/wrong');
        fireEvent.click(screen.getByRole('button', {name: 'Save and connect'}));

        expect(await screen.findByText('could not connect to the MCP server')).not.toBeNull();
        fireEvent.click(screen.getByRole('button', {name: 'Edit details'}));

        fireEvent.change(screen.getByLabelText('Server URL'), {target: {value: 'https://mcp.example.com/mcp'}});
        mockGetMCPServerTools.mockResolvedValue(discoveredServer());
        fireEvent.click(screen.getByRole('button', {name: 'Save and connect'}));

        expect(await screen.findByRole('group', {name: 'Read-only tools'})).not.toBeNull();

        // The second save updates the server the first one created.
        const updated = onPersist.mock.calls[1][0] as MCPServerConfig;
        expect(updated.id).toBe(SERVER_ID);
        expect(updated.baseURL).toBe('https://mcp.example.com/mcp');
    });

    test('skipping warns about default policies and saves no tool policies', async () => {
        const {onPersist, onClose} = renderWizard({kind: 'add'});

        fillDetails('Tracker', 'https://mcp.example.com/mcp');
        fireEvent.click(screen.getByRole('button', {name: 'Skip connecting'}));

        expect(screen.getByText('Save without reviewing tools?')).not.toBeNull();
        expect(screen.getByText(/ask for approval before every run/)).not.toBeNull();
        expect(onPersist).not.toHaveBeenCalled();

        fireEvent.click(screen.getByRole('button', {name: 'Save without connecting'}));

        await waitFor(() => expect(onClose).toHaveBeenCalled());
        expect(onPersist).toHaveBeenCalledTimes(1);
        expect((onPersist.mock.calls[0][0] as MCPServerConfig).tool_configs).toBeUndefined();
        expect(mockGetMCPServerTools).not.toHaveBeenCalled();
    });

    test('requires a unique name and a URL before saving', () => {
        const {onPersist} = renderWizard({kind: 'add'}, ['Tracker']);

        fireEvent.click(screen.getByRole('button', {name: 'Save and connect'}));
        expect(screen.getByText('Enter a name for the server.')).not.toBeNull();
        expect(screen.getByText(/Enter the server URL/)).not.toBeNull();

        fillDetails('Tracker', 'https://mcp.example.com/mcp');
        fireEvent.click(screen.getByRole('button', {name: 'Skip connecting'}));
        expect(screen.getByText('Another server already uses this name.')).not.toBeNull();
        expect(onPersist).not.toHaveBeenCalled();
    });
});

describe('MCPServerWizard review of an existing server', () => {
    beforeEach(() => {
        jest.clearAllMocks();
    });

    test('keeps chosen policies, pre-sets the rest, and saves only tool policies', async () => {
        mockGetMCPServerTools.mockResolvedValue(discoveredServer());
        const existing: MCPServerConfig = {
            id: SERVER_ID,
            name: 'Tracker',
            enabled: true,
            baseURL: 'https://mcp.example.com/mcp',
            headers: {},
            tool_configs: [
                {name: 'list_issues', policy: 'ask', enabled: true},
                {name: 'delete_issue', policy: '', enabled: false},
                {name: 'retired_tool', policy: 'ask', enabled: true},
            ],
        };
        const {onPersist, onClose} = renderWizard({kind: 'review', server: existing});

        expect(await screen.findByRole('dialog', {name: 'Review tools for Tracker'})).not.toBeNull();
        expect(await screen.findByRole('group', {name: 'Read-only tools'})).not.toBeNull();
        expect(selectedLabel(getSelect('Approval policy for list_issues'))).toBe('Ask Every Time');

        fireEvent.click(screen.getByRole('button', {name: 'Accept and save'}));

        await waitFor(() => expect(onClose).toHaveBeenCalled());
        const [reviewed, toolConfigsOnly] = onPersist.mock.calls[0] as unknown as [MCPServerConfig, boolean];
        expect(toolConfigsOnly).toBe(true);
        expect(reviewed.tool_configs).toEqual([
            {name: 'list_issues', policy: 'ask', enabled: true},
            {name: 'delete_issue', policy: 'ask', enabled: false},
            {name: 'retired_tool', policy: 'ask', enabled: true},
        ]);
    });
});
