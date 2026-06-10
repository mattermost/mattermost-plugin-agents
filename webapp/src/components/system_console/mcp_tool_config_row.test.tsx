// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

import {MCPToolConfig} from './mcp_servers';
import MCPToolConfigRow from './mcp_tool_config_row';
import {MCPToolInfo} from './mcp_tools_viewer';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
        }),
    };
});

const testTool: MCPToolInfo = {
    name: 'get_issue',
    description: 'Upstream issue description',
    inputSchema: {type: 'object'},
};

const testToolConfig = (overrides: Partial<MCPToolConfig> = {}): MCPToolConfig => ({
    name: 'get_issue',
    policy: 'ask',
    enabled: true,
    ...overrides,
});

describe('MCPToolConfigRow', () => {
    test('renders retrieval override field when expanded', () => {
        render(
            <MCPToolConfigRow
                tool={testTool}
                toolConfig={testToolConfig({retrieval_description_override: 'Find incidents'})}
                onToolConfigChange={jest.fn()}
            />,
        );

        fireEvent.click(screen.getByRole('button', {name: 'Show tool details'}));

        expect((screen.getByLabelText('Retrieval description override') as HTMLInputElement).value).toBe('Find incidents');
        expect(screen.getByText('Optional. Used only by dynamic tool loading search to help the agent find this tool. It does not change the tool schema sent after loading.')).not.toBeNull();
    });

    test('editing retrieval override updates tool config', () => {
        const onToolConfigChange = jest.fn();
        render(
            <MCPToolConfigRow
                tool={testTool}
                toolConfig={testToolConfig()}
                onToolConfigChange={onToolConfigChange}
            />,
        );

        fireEvent.click(screen.getByRole('button', {name: 'Show tool details'}));
        fireEvent.change(screen.getByLabelText('Retrieval description override'), {
            target: {value: 'Use for incident lookup'},
        });

        expect(onToolConfigChange).toHaveBeenCalledWith({
            ...testToolConfig(),
            retrieval_description_override: 'Use for incident lookup',
        });
    });

    test('preserves retrieval override spaces while editing and trims on blur', () => {
        const onToolConfigChange = jest.fn();
        render(
            <MCPToolConfigRow
                tool={testTool}
                toolConfig={testToolConfig()}
                onToolConfigChange={onToolConfigChange}
            />,
        );

        fireEvent.click(screen.getByRole('button', {name: 'Show tool details'}));
        const input = screen.getByLabelText('Retrieval description override');
        fireEvent.change(input, {
            target: {value: 'Use for incident lookup '},
        });
        expect(onToolConfigChange).toHaveBeenLastCalledWith({
            ...testToolConfig(),
            retrieval_description_override: 'Use for incident lookup ',
        });

        fireEvent.blur(input, {
            target: {value: 'Use for incident lookup '},
        });
        expect(onToolConfigChange).toHaveBeenLastCalledWith({
            ...testToolConfig(),
            retrieval_description_override: 'Use for incident lookup',
        });
    });

    test('clearing retrieval override removes the value from config', () => {
        const onToolConfigChange = jest.fn();
        render(
            <MCPToolConfigRow
                tool={testTool}
                toolConfig={testToolConfig({retrieval_description_override: 'Find incidents'})}
                onToolConfigChange={onToolConfigChange}
            />,
        );

        fireEvent.click(screen.getByRole('button', {name: 'Show tool details'}));
        fireEvent.change(screen.getByLabelText('Retrieval description override'), {
            target: {value: ''},
        });

        const updatedConfig = onToolConfigChange.mock.calls[0][0];
        expect(updatedConfig).toEqual(testToolConfig());
        expect(updatedConfig).not.toHaveProperty('retrieval_description_override');
    });

    test('retrieval override input is disabled when server is disabled', () => {
        render(
            <MCPToolConfigRow
                tool={testTool}
                toolConfig={testToolConfig()}
                onToolConfigChange={jest.fn()}
                serverDisabled={true}
            />,
        );

        fireEvent.click(screen.getByRole('button', {name: 'Show tool details'}));

        expect((screen.getByLabelText('Retrieval description override') as HTMLInputElement).disabled).toBe(true);
    });
});
