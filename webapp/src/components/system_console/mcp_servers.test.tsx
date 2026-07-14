// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

// Minimal react-intl shim: ts-jest bypasses babel, so FormattedMessage needs an id at runtime.
jest.mock('react-intl', () => {
    const React = require('react'); // eslint-disable-line @typescript-eslint/no-shadow, no-shadow, global-require

    return {
        __esModule: true,
        IntlProvider: ({children}: {children: React.ReactNode}) => React.createElement(React.Fragment, null, children),
        FormattedMessage: ({defaultMessage}: {defaultMessage?: string}) =>
            React.createElement(React.Fragment, null, defaultMessage ?? ''),
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage?: string}) => defaultMessage ?? '',
        }),
    };
});

jest.mock('react-redux', () => ({
    __esModule: true,
    useSelector: jest.fn(), // SiteURL unset -> component falls back to window.location.origin
}));

jest.mock('../../client', () => ({
    __esModule: true,

    // Never resolves: the prefetch otherwise updates state outside act().
    getMCPTools: jest.fn().mockReturnValue(new Promise(() => null)),
    getVettedToolSeed: jest.fn().mockResolvedValue([]),
}));

jest.mock('./mcp_tools_viewer', () => ({
    __esModule: true,
    default: () => null,
}));

/* eslint-disable import/first, import/order */
import MCPServers, {MCPConfig, MCPServerConfig} from './mcp_servers';
/* eslint-enable import/first, import/order */

const STABLE_ID = 'abcdefghijklmnopqrstuvwxyz';

function makeMCPConfig(servers: MCPServerConfig[]): MCPConfig {
    return {
        enabled: true,
        enablePluginServer: false,
        servers,
        embeddedServer: {enabled: true, tool_configs: []},
    };
}

function renderServers(servers: MCPServerConfig[]) {
    const onChange = jest.fn();
    const result = render(
        <MCPServers
            mcpConfig={makeMCPConfig(servers)}
            onChange={onChange}
        />,
    );
    return {...result, onChange};
}

function lastChangedServers(onChange: jest.Mock): MCPServerConfig[] {
    expect(onChange).toHaveBeenCalled();
    const config: MCPConfig = onChange.mock.calls[onChange.mock.calls.length - 1][0];
    return config.servers ?? [];
}

const existingServer: MCPServerConfig = {
    id: STABLE_ID,
    name: 'Jira',
    enabled: true,
    baseURL: 'https://jira.example.com',
    headers: {},
};

describe('MCPServers stable ID handling', () => {
    it('preserves the server id through a name edit', () => {
        const {onChange} = renderServers([existingServer]);

        fireEvent.click(screen.getByText('Jira'));
        const nameInput = screen.getByPlaceholderText('Server name');
        fireEvent.change(nameInput, {target: {value: 'Jira Cloud'}});
        fireEvent.blur(nameInput);

        const servers = lastChangedServers(onChange);
        expect(servers[0].name).toBe('Jira Cloud');
        expect(servers[0].id).toBe(STABLE_ID);
    });

    it('preserves the server id through a URL edit', () => {
        const {onChange} = renderServers([existingServer]);

        const urlInput = screen.getByPlaceholderText('https://mcp.example.com');
        fireEvent.change(urlInput, {target: {value: 'https://jira2.example.com'}});

        const servers = lastChangedServers(onChange);
        expect(servers[0].baseURL).toBe('https://jira2.example.com');
        expect(servers[0].id).toBe(STABLE_ID);
    });

    it('adds a new server without an id so the backend mints the stable ID on save', () => {
        const {onChange} = renderServers([existingServer]);

        fireEvent.click(screen.getByText('Add Remote MCP Server'));

        const servers = lastChangedServers(onChange);
        expect(servers).toHaveLength(2);
        expect(servers[1].id).toBeUndefined();
        expect(servers[0].id).toBe(STABLE_ID);
    });
});
