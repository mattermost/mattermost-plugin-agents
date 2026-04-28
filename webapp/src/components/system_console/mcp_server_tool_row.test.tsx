// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

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

/* eslint-disable import/first */
import {IntlProvider} from 'react-intl';

import MCPServerToolRow from './mcp_server_tool_row';
import {MCPServerInfo} from './mcp_tools_viewer';
import {MCPServerConfig} from './mcp_servers';
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

        const select = screen.getByRole('combobox') as HTMLSelectElement;

        expect(select.disabled).toBe(false);
        expect(select.value).toBe('ask');
    });

    test('plugin row policy change calls onServerConfigChange with merged tool_configs', () => {
        const {onServerConfigChange} = renderRow(makePluginServer(), makePluginServerConfig());
        fireEvent.click(screen.getByText('Demo Plugin'));

        const select = screen.getByRole('combobox');
        fireEvent.change(select, {target: {value: 'auto_run_everywhere'}});

        expect(onServerConfigChange).toHaveBeenCalledTimes(1);
        const [updated] = onServerConfigChange.mock.calls[0];

        expect(updated.tool_configs).toEqual([
            {name: 'com_example_demo__echo', policy: 'auto_run_everywhere', enabled: true},
        ]);
    });

    test('plugin row tool toggle disables the tool in tool_configs', () => {
        const {onServerConfigChange} = renderRow(makePluginServer(), makePluginServerConfig());
        fireEvent.click(screen.getByText('Demo Plugin'));

        // ToggleSwitch renders as a native checkbox: index 0 is server-level, index 1 is the per-tool toggle.
        const switches = screen.getAllByRole('checkbox');
        expect(switches.length).toBeGreaterThanOrEqual(2);
        fireEvent.click(switches[1]);

        expect(onServerConfigChange).toHaveBeenCalledTimes(1);
        const [updated] = onServerConfigChange.mock.calls[0];
        expect(updated.tool_configs).toEqual([
            {name: 'com_example_demo__echo', policy: 'ask', enabled: false},
        ]);
    });
});
