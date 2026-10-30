// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, within} from '@testing-library/react';

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

jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children, overlay}: {children: React.ReactNode; overlay: React.ReactNode}) => <>{children}{overlay}</>,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

/* eslint-disable import/first */
import {IntlProvider} from 'react-intl';

import {chooseOption, getSelect, selectedLabel} from '../../../tests/compass_select';

import MCPServerToolRow from './mcp_server_tool_row';
import {MCPServerConfig, MCPServerInfo} from './mcp_types';
/* eslint-enable import/first */

function makePluginServer(): MCPServerInfo {
    return {
        name: 'Demo Plugin',
        url: 'plugin://com.example.demo/mcp',
        serverType: 'plugin',
        enabled: true,
        tools: [
            {name: 'com_example_demo__echo', description: 'Echo', inputSchema: null},
        ],
        needsOAuth: false,
        error: null,
        toolConfigs: [{name: 'com_example_demo__echo', policy: 'ask', enabled: true}],
    };
}

function makePluginServerConfig(): MCPServerConfig {
    return {
        name: 'Demo Plugin',
        enabled: true,
        baseURL: 'plugin://com.example.demo/mcp',
        headers: {},
        tool_configs: [{name: 'com_example_demo__echo', policy: 'ask', enabled: true}],
    };
}

function renderRow(server: MCPServerInfo, serverConfig: MCPServerConfig | null) {
    const onServerConfigChange = jest.fn();
    return {
        ...render(
            <IntlProvider locale='en'>
                <MCPServerToolRow
                    server={server}
                    serverConfig={serverConfig}
                    onServerConfigChange={onServerConfigChange}
                />
            </IntlProvider>,
        ),
        onServerConfigChange,
    };
}

describe('MCPServerToolRow — plugin row policy dropdown re-enable', () => {
    test('plugin wire tool name renders with short user-visible label', () => {
        renderRow(makePluginServer(), makePluginServerConfig());

        fireEvent.click(screen.getByText('Demo Plugin'));

        expect(screen.getByText('echo')).not.toBeNull();
        expect(screen.queryByText('com_example_demo__echo')).toBeNull();
    });

    test('plugin row renders policy dropdown enabled', () => {
        renderRow(makePluginServer(), makePluginServerConfig());

        fireEvent.click(screen.getByText('Demo Plugin'));

        const select = getSelect('Approval policy for echo') as HTMLButtonElement;

        expect(select.disabled).toBe(false);
        expect(selectedLabel(select)).toBe('Ask Every Time');
    });

    test('plugin row policy change calls onServerConfigChange with merged tool_configs', () => {
        const {onServerConfigChange} = renderRow(makePluginServer(), makePluginServerConfig());
        fireEvent.click(screen.getByText('Demo Plugin'));

        chooseOption(getSelect('Approval policy for echo'), 'Auto Run (Everywhere)');

        expect(onServerConfigChange).toHaveBeenCalledTimes(1);
        const [updated] = onServerConfigChange.mock.calls[0];

        expect(updated.tool_configs).toEqual([
            {name: 'com_example_demo__echo', policy: 'auto_run_everywhere', enabled: true},
        ]);
    });

    test('plugin row tool toggle disables the tool in tool_configs', () => {
        const {onServerConfigChange} = renderRow(makePluginServer(), makePluginServerConfig());
        fireEvent.click(screen.getByText('Demo Plugin'));

        // ToggleSwitch renders as role="switch": index 0 is server-level, index 1 is the per-tool toggle.
        const switches = screen.getAllByRole('switch');
        expect(switches.length).toBeGreaterThanOrEqual(2);
        fireEvent.click(switches[1]);

        expect(onServerConfigChange).toHaveBeenCalledTimes(1);
        const [updated] = onServerConfigChange.mock.calls[0];
        expect(updated.tool_configs).toEqual([
            {name: 'com_example_demo__echo', policy: 'ask', enabled: false},
        ]);
    });
});

describe('MCPServerToolRow — unconfigured tool defaults', () => {
    test('unconfigured tools show the server default policy', () => {
        const server: MCPServerInfo = {
            ...makePluginServer(),
            serverType: 'remote',
            name: 'Remote',
            url: 'https://mcp.example.com',
            tools: [
                {name: 'list_things', description: 'List', inputSchema: null, readOnlyHint: true, defaultPolicy: 'auto_run_in_dm'},
                {name: 'delete_thing', description: 'Delete', inputSchema: null, defaultPolicy: 'ask'},
            ],
        };
        const config = {...makePluginServerConfig(), name: 'Remote', baseURL: 'https://mcp.example.com', tool_configs: []};
        const {onServerConfigChange} = renderRow(server, config);
        fireEvent.click(screen.getByText('Remote'));

        expect(selectedLabel(getSelect('Approval policy for list_things'))).toBe('Auto Run (DM)');
        expect(selectedLabel(getSelect('Approval policy for delete_thing'))).toBe('Ask Every Time');

        // Disabling the tool does not choose a policy for it, so the stored
        // entry keeps following the default.
        fireEvent.click(screen.getAllByRole('switch')[1]);
        expect(onServerConfigChange).toHaveBeenCalledWith(expect.objectContaining({
            tool_configs: [{name: 'list_things', policy: '', enabled: false}],
        }));
    });

    test('a stored entry without a policy shows the default and stores a policy only when one is picked', () => {
        const server: MCPServerInfo = {
            ...makePluginServer(),
            serverType: 'remote',
            name: 'Remote',
            url: 'https://mcp.example.com',
            tools: [{name: 'list_things', description: 'List', inputSchema: null, readOnlyHint: true, defaultPolicy: 'auto_run_in_dm'}],
        };
        const config = {
            ...makePluginServerConfig(),
            name: 'Remote',
            baseURL: 'https://mcp.example.com',
            tool_configs: [{name: 'list_things', policy: '' as const, enabled: false}],
        };
        const {onServerConfigChange} = renderRow(server, config);
        fireEvent.click(screen.getByText('Remote'));

        expect(selectedLabel(getSelect('Approval policy for list_things'))).toBe('Auto Run (DM)');

        fireEvent.click(screen.getAllByRole('switch')[1]);
        expect(onServerConfigChange).toHaveBeenLastCalledWith(expect.objectContaining({
            tool_configs: [{name: 'list_things', policy: '', enabled: true}],
        }));

        chooseOption(getSelect('Approval policy for list_things'), 'Ask Every Time');
        expect(onServerConfigChange).toHaveBeenLastCalledWith(expect.objectContaining({
            tool_configs: [{name: 'list_things', policy: 'ask', enabled: false}],
        }));
    });
});

describe('MCPServerToolRow — grouped tools and bulk policy controls', () => {
    const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};

    beforeEach(() => {
        useIsLicensedFor.mockReturnValue(true);
    });

    function makeRemoteServer(tools: MCPServerInfo['tools']): MCPServerInfo {
        return {
            name: 'Remote',
            url: 'https://mcp.example.com',
            serverType: 'remote',
            enabled: true,
            tools,
            needsOAuth: false,
            error: null,
        };
    }

    const annotatedTools: MCPServerInfo['tools'] = [
        {name: 'list_things', description: 'List', inputSchema: null, readOnlyHint: true, defaultPolicy: 'auto_run_in_dm'},
        {name: 'get_thing', description: 'Get', inputSchema: null, readOnlyHint: true, defaultPolicy: 'auto_run_in_dm'},
        {name: 'delete_thing', description: 'Delete', inputSchema: null, defaultPolicy: 'ask'},
    ];

    function makeRemoteConfig(toolConfigs: MCPServerConfig['tool_configs']): MCPServerConfig {
        return {name: 'Remote', enabled: true, baseURL: 'https://mcp.example.com', headers: {}, tool_configs: toolConfigs};
    }

    test('groups tools by the read-only annotation with counts', () => {
        renderRow(makeRemoteServer(annotatedTools), makeRemoteConfig([]));
        fireEvent.click(screen.getByText('Remote'));

        const readGroup = screen.getByRole('group', {name: 'Read-only tools'});
        const writeGroup = screen.getByRole('group', {name: 'Write tools'});
        expect(within(readGroup).getByText('list_things')).not.toBeNull();
        expect(within(readGroup).getByText('get_thing')).not.toBeNull();
        expect(within(readGroup).queryByText('delete_thing')).toBeNull();
        expect(within(writeGroup).getByText('delete_thing')).not.toBeNull();
        expect(within(readGroup).getByText('2')).not.toBeNull();
    });

    test('does not group a server that annotates no tool as read-only', () => {
        const tools = annotatedTools.map((tool) => ({...tool, readOnlyHint: false}));
        renderRow(makeRemoteServer(tools), makeRemoteConfig([]));
        fireEvent.click(screen.getByText('Remote'));

        expect(screen.queryByRole('group')).toBeNull();
        expect(screen.getByText('delete_thing')).not.toBeNull();
        expect(getSelect('Approval policy for all tools on Remote')).not.toBeNull();
    });

    test('group control sets only that group, keeping enabled state and overrides', () => {
        const {onServerConfigChange} = renderRow(makeRemoteServer(annotatedTools), makeRemoteConfig([
            {name: 'list_things', policy: 'auto_run_in_dm', enabled: false, retrieval_description_override: 'Lists things'},
            {name: 'delete_thing', policy: 'ask', enabled: true},
        ]));
        fireEvent.click(screen.getByText('Remote'));

        chooseOption(getSelect('Approval policy for all read-only tools on Remote'), 'Auto Run (Everywhere)');

        expect(onServerConfigChange).toHaveBeenCalledTimes(1);
        expect(onServerConfigChange.mock.calls[0][0].tool_configs).toEqual([
            {name: 'list_things', policy: 'auto_run_everywhere', enabled: false, retrieval_description_override: 'Lists things'},
            {name: 'delete_thing', policy: 'ask', enabled: true},
            {name: 'get_thing', policy: 'auto_run_everywhere', enabled: true},
        ]);
    });

    test('all-tools control sets every tool on the server', () => {
        const {onServerConfigChange} = renderRow(makeRemoteServer(annotatedTools), makeRemoteConfig([]));
        fireEvent.click(screen.getByText('Remote'));

        chooseOption(getSelect('Approval policy for all tools on Remote'), 'Ask Every Time');

        expect(onServerConfigChange.mock.calls[0][0].tool_configs).toEqual([
            {name: 'list_things', policy: 'ask', enabled: true},
            {name: 'get_thing', policy: 'ask', enabled: true},
            {name: 'delete_thing', policy: 'ask', enabled: true},
        ]);
    });

    test('bulk controls show the shared policy, or Mixed when tools differ', () => {
        renderRow(makeRemoteServer(annotatedTools), makeRemoteConfig([]));
        fireEvent.click(screen.getByText('Remote'));

        expect(selectedLabel(getSelect('Approval policy for all read-only tools on Remote'))).toBe('Auto Run (DM)');
        expect(selectedLabel(getSelect('Approval policy for all write tools on Remote'))).toBe('Ask Every Time');
        expect(selectedLabel(getSelect('Approval policy for all tools on Remote'))).toBe('Mixed');
    });

    test('bulk controls are disabled without tool approval policies', () => {
        useIsLicensedFor.mockImplementation((capability: string) => capability !== 'tool_approval_policies');
        renderRow(makeRemoteServer(annotatedTools), makeRemoteConfig([]));
        fireEvent.click(screen.getByText('Remote'));

        expect((getSelect('Approval policy for all tools on Remote') as HTMLButtonElement).disabled).toBe(true);
        expect((getSelect('Approval policy for all read-only tools on Remote') as HTMLButtonElement).disabled).toBe(true);
    });
});

describe('MCPServerToolRow license gating', () => {
    const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};

    beforeEach(() => {
        useIsLicensedFor.mockReturnValue(true);
    });

    test('keeps a remote server enable toggle usable at Enterprise', () => {
        const server = {...makePluginServer(), serverType: 'remote' as const, name: 'Remote'};
        const config = {...makePluginServerConfig(), enabled: false};
        renderRow(server, config);

        const enable = screen.getByLabelText('Enable Remote') as HTMLInputElement;
        expect(enable.disabled).toBe(false);
    });

    test('disables enabling a remote server below Enterprise and still allows turning it off', () => {
        useIsLicensedFor.mockImplementation((capability: string) => capability !== 'remote_mcp');

        const server = {...makePluginServer(), serverType: 'remote' as const, name: 'Remote'};
        const offConfig = {...makePluginServerConfig(), enabled: false};
        const off = renderRow(server, offConfig);
        expect((screen.getByLabelText('Enable Remote') as HTMLInputElement).disabled).toBe(true);
        off.unmount();

        const onConfig = {...makePluginServerConfig(), enabled: true};
        const {onServerConfigChange} = renderRow(server, onConfig);
        const toggle = screen.getByLabelText('Enable Remote') as HTMLInputElement;
        expect(toggle.disabled).toBe(false);
        fireEvent.click(toggle);
        expect(onServerConfigChange).toHaveBeenCalledWith(expect.objectContaining({enabled: false}));
    });

    test('hides Connect Account when remote MCP is not licensed', () => {
        useIsLicensedFor.mockImplementation((capability: string) => capability !== 'remote_mcp');
        const server = {
            ...makePluginServer(),
            serverType: 'remote' as const,
            name: 'Remote',
            needsOAuth: true,
            oauthURL: 'https://example.com/oauth',
        };
        renderRow(server, makePluginServerConfig());
        fireEvent.click(screen.getByText('Remote'));
        expect(screen.queryByText('Connect Account')).toBeNull();
    });
});
