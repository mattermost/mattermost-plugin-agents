// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';

jest.mock('react-intl', () => {
    const React = require('react'); // eslint-disable-line @typescript-eslint/no-shadow, no-shadow, global-require

    const formatMessage = (
        {defaultMessage}: {defaultMessage?: string},
        values?: Record<string, unknown>,
    ) => {
        let message = defaultMessage ?? '';
        if (values) {
            for (const [key, value] of Object.entries(values)) {
                message = message.replace(new RegExp(`\\{${key}\\}`, 'g'), String(value));
            }
        }
        return message;
    };

    return {
        __esModule: true,
        IntlProvider: ({children}: {children: React.ReactNode}) => React.createElement(React.Fragment, null, children),
        FormattedMessage: ({defaultMessage, values}: {defaultMessage?: string; values?: Record<string, unknown>}) =>
            React.createElement(React.Fragment, null, formatMessage({defaultMessage}, values)),
        useIntl: () => ({
            formatMessage,
        }),
    };
});

jest.mock('../access_control/console_policy_section', () => ({
    __esModule: true,
    default: ({resourceId}: {resourceId: string}) => (
        <div data-testid='console-policy-section'>{resourceId}</div>
    ),
}));

/* eslint-disable import/first, import/order */
import {IntlProvider} from 'react-intl';

import {BuiltInPluginServersSection} from './mcp_builtin_servers_section';
import {MCPServerInfo} from './mcp_types';
/* eslint-enable import/first, import/order */

const EMBEDDED_ID = 'abcdefghijklmnopqrstuvwxyz';

function makePluginServer(overrides: Partial<MCPServerInfo> = {}): MCPServerInfo {
    return {
        name: 'Demo Plugin',
        url: 'plugin://com.example.demo/mcp',
        tools: [],
        needsOAuth: false,
        error: null,
        serverType: 'plugin',
        id: 'pluginstableidabcdefghijklm',
        ...overrides,
    };
}

describe('BuiltInPluginServersSection', () => {
    it('renders the Mattermost built-in card and plugin cards', () => {
        render(
            <IntlProvider locale='en'>
                <BuiltInPluginServersSection
                    embeddedServerId={EMBEDDED_ID}
                    pluginServers={[makePluginServer()]}
                />
            </IntlProvider>,
        );

        expect(screen.getByTestId('built-in-plugin-servers-section')).not.toBeNull();
        expect(screen.getByText('Mattermost')).not.toBeNull();
        expect(screen.getByText('Built-in')).not.toBeNull();
        expect(screen.getByText('Demo Plugin')).not.toBeNull();
        expect(screen.getByText('Plugin ID: com.example.demo')).not.toBeNull();
        expect(screen.getByText(EMBEDDED_ID)).not.toBeNull();
        expect(screen.getByText('pluginstableidabcdefghijklm')).not.toBeNull();
    });

    it('omits policy sections when ids are absent', () => {
        const serverWithoutId: MCPServerInfo = {
            name: 'Demo Plugin',
            url: 'plugin://com.example.demo/mcp',
            tools: [],
            needsOAuth: false,
            error: null,
            serverType: 'plugin',
        };
        render(
            <IntlProvider locale='en'>
                <BuiltInPluginServersSection
                    pluginServers={[serverWithoutId]}
                />
            </IntlProvider>,
        );

        expect(screen.queryByTestId('console-policy-section')).toBeNull();
    });
});
