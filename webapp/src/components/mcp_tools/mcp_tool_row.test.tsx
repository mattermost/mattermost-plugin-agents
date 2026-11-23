// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

import MCPToolRow from './mcp_tool_row';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
        }),
    };
});

describe('MCPToolRow', () => {
    test('expands to show full description, extra details, and formatted inputSchema', () => {
        render(
            <MCPToolRow
                displayName='search_issues'
                description='Search issues across all configured projects'
                inputSchema={{type: 'object', properties: {query: {type: 'string'}}}}
                badges={<span>{'Not reviewed'}</span>}
                policyControl={<span>{'Auto Run (DM)'}</span>}
                enabled={true}
                toggleAriaLabel='Disable tool search_issues'
                onToggleEnabled={jest.fn()}
                extraDetails={<div>{'Custom details field'}</div>}
            />,
        );

        expect(screen.getByText('search_issues')).not.toBeNull();
        expect(screen.getByText('Not reviewed')).not.toBeNull();
        expect(screen.getByText('Auto Run (DM)')).not.toBeNull();
        expect(screen.queryByText('Custom details field')).toBeNull();

        const expandButton = screen.getByRole('button', {name: 'Show tool details'});
        expect(expandButton.getAttribute('aria-expanded')).toBe('false');
        fireEvent.click(expandButton);

        expect(expandButton.getAttribute('aria-expanded')).toBe('true');
        expect(screen.getByText('Custom details field')).not.toBeNull();
        expect(screen.getByText(/"query"/)).not.toBeNull();
    });

    test('keeps details expandable when dimmed and toggle is disabled', () => {
        const onToggle = jest.fn();
        render(
            <MCPToolRow
                displayName='delete_post'
                description='Delete a post'
                inputSchema={{type: 'object', properties: {post_id: {type: 'string'}}}}
                badges={<span>{'Disabled by admin'}</span>}
                enabled={false}
                toggleDisabled={true}
                toggleAriaLabel='Enable tool delete_post'
                onToggleEnabled={onToggle}
                dimmed={true}
            />,
        );

        const toggle = screen.getByRole('switch', {name: 'Enable tool delete_post'}) as HTMLInputElement;
        expect(toggle.disabled).toBe(true);
        fireEvent.click(toggle);
        expect(onToggle).not.toHaveBeenCalled();

        fireEvent.click(screen.getByRole('button', {name: 'Show tool details'}));
        expect(screen.getByText(/"post_id"/)).not.toBeNull();
    });

    test('hides details expand button when there is no description, schema, or extra details', () => {
        render(
            <MCPToolRow
                displayName='ping'
                enabled={true}
                toggleAriaLabel='Disable tool ping'
                onToggleEnabled={jest.fn()}
            />,
        );

        expect(screen.queryByRole('button', {name: 'Show tool details'})).toBeNull();
    });
});
