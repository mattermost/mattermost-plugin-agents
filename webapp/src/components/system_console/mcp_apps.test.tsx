// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

import MCPAppsSection, {defaultMCPAppsConfig, MCPAppsConfig} from './mcp_apps';
import {MCPConfig} from './mcp_servers';

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

const appsConfig = (overrides: Partial<MCPAppsConfig> = {}): MCPAppsConfig => ({
    ...defaultMCPAppsConfig,
    ...overrides,
});

describe('MCPAppsSection', () => {
    test('renders master toggle only when disabled', () => {
        render(
            <MCPAppsSection
                value={appsConfig({enabled: false})}
                onChange={jest.fn()}
            />,
        );

        expect(screen.getByText('Enable MCP Apps')).not.toBeNull();
        expect(screen.queryByText('Sandbox Base URL')).toBeNull();
        expect(screen.queryByText('Sandbox Listener Address')).toBeNull();
        expect(screen.queryByText('Allow insecure same-origin sandbox')).toBeNull();
    });

    test('enabled reveals sandbox fields and insecure toggle', () => {
        render(
            <MCPAppsSection
                value={appsConfig({enabled: true})}
                onChange={jest.fn()}
            />,
        );

        expect(screen.getByText('Sandbox Base URL')).not.toBeNull();
        expect(screen.getByText('Sandbox Listener Address')).not.toBeNull();
        expect(screen.getByText('Allow insecure same-origin sandbox')).not.toBeNull();
        expect(screen.getByText('NOT RECOMMENDED')).not.toBeNull();
    });

    test('typing in the URL field preserves other fields', () => {
        const onChange = jest.fn();
        const value = appsConfig({
            enabled: true,
            sandboxListenAddress: ':9000',
            allowInsecureSameOriginSandbox: false,
        });
        render(
            <MCPAppsSection
                value={value}
                onChange={onChange}
            />,
        );

        fireEvent.change(screen.getByPlaceholderText('https://mm-apps.example.com'), {
            target: {value: 'https://apps.example.com'},
        });

        expect(onChange).toHaveBeenCalledWith({
            ...value,
            sandboxURL: 'https://apps.example.com',
        });
    });

    test('insecure toggle with empty URL shows warning; URL set hides it', () => {
        const {rerender} = render(
            <MCPAppsSection
                value={appsConfig({
                    enabled: true,
                    allowInsecureSameOriginSandbox: true,
                    sandboxURL: '',
                })}
                onChange={jest.fn()}
            />,
        );

        expect(screen.getByText('Warning: MCP app content will run with the same browser origin as Mattermost. Configure a Sandbox Base URL to restore isolation.')).not.toBeNull();

        rerender(
            <MCPAppsSection
                value={appsConfig({
                    enabled: true,
                    allowInsecureSameOriginSandbox: true,
                    sandboxURL: 'https://apps.example.com',
                })}
                onChange={jest.fn()}
            />,
        );

        expect(screen.queryByText('Warning: MCP app content will run with the same browser origin as Mattermost. Configure a Sandbox Base URL to restore isolation.')).toBeNull();
    });

    test('toggling insecure fires onChange with field updated', () => {
        const onChange = jest.fn();
        const value = appsConfig({enabled: true});
        render(
            <MCPAppsSection
                value={value}
                onChange={onChange}
            />,
        );

        const radios = screen.getAllByRole('radio');

        // BooleanItem for insecure is the last pair; click its true radio.
        const insecureTrue = radios[radios.length - 2];
        fireEvent.click(insecureTrue);

        expect(onChange).toHaveBeenCalledWith({
            ...value,
            allowInsecureSameOriginSandbox: true,
        });
    });
});

describe('MCPServers apps field preservation', () => {
    test('rebuilt config literal from unrelated edit must carry apps', () => {
        // Guards the mcp_servers.tsx trap: rebuilding config field-by-field
        // without restating apps silently drops MCP Apps settings on save.
        const mcpConfig: MCPConfig = {
            enabled: true,
            enablePluginServer: false,
            servers: [],
            embeddedServer: {enabled: true},
            apps: appsConfig({
                enabled: true,
                sandboxURL: 'https://apps.example.com',
            }),
        };

        const rebuilt: MCPConfig = {
            enabled: true,
            enablePluginServer: true,
            servers: mcpConfig.servers ?? [],
            embeddedServer: {
                ...(mcpConfig.embeddedServer || {}),
                enabled: true,
            },
            idleTimeoutMinutes: mcpConfig.idleTimeoutMinutes,
            apps: mcpConfig?.apps ?? defaultMCPAppsConfig,
        };

        expect(rebuilt.apps).toEqual(mcpConfig.apps);
        expect(rebuilt.enablePluginServer).toBe(true);
    });
});
