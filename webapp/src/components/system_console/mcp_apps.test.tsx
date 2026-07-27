// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

import MCPAppsSection, {defaultMCPAppsConfig, MCPAppsConfig} from './mcp_apps';
import {MCPConfig, normalizeMCPConfig} from './mcp_servers';

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

        // Two BooleanItems when enabled: Enable MCP Apps (radios 0-1), insecure (2-3).
        const radios = screen.getAllByRole('radio');
        fireEvent.click(radios[2]);

        expect(onChange).toHaveBeenCalledWith({
            ...value,
            allowInsecureSameOriginSandbox: true,
        });
    });
});

describe('normalizeMCPConfig apps preservation', () => {
    test('unrelated enablePluginServer edit retains apps from production normalizer', () => {
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

        const rebuilt = normalizeMCPConfig({
            ...mcpConfig,
            enablePluginServer: true,
        });

        expect(rebuilt.apps).toEqual(mcpConfig.apps);
        expect(rebuilt.enablePluginServer).toBe(true);
    });

    test('missing apps falls back to defaultMCPAppsConfig', () => {
        const rebuilt = normalizeMCPConfig({
            enabled: true,
            enablePluginServer: false,
            servers: null,
            embeddedServer: {enabled: false},
        });
        expect(rebuilt.apps).toEqual(defaultMCPAppsConfig);
        expect(rebuilt.enabled).toBe(true);
        expect(rebuilt.embeddedServer.enabled).toBe(true);
    });
});
