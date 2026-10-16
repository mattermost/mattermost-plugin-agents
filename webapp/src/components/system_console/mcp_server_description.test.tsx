// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';

// Minimal react-intl shim: ts-jest bypasses babel, so messages need no ids at runtime.
jest.mock('react-intl', () => {
    const formatMessage = (
        {defaultMessage}: {defaultMessage?: string},
        values?: Record<string, unknown>,
    ) => {
        let message = defaultMessage ?? '';
        for (const [key, value] of Object.entries(values ?? {})) {
            message = message.replace(new RegExp(`\\{${key}\\}`, 'g'), String(value));
        }
        return message;
    };
    return {
        __esModule: true,
        useIntl: () => ({formatMessage}),
    };
});

jest.mock('../../client', () => ({
    __esModule: true,
    getMCPSuggestedDescription: jest.fn(),
}));

/* eslint-disable import/first, import/order */
import {getMCPSuggestedDescription} from '../../client';

import MCPServerDescription from './mcp_server_description';
/* eslint-enable import/first, import/order */

const mockGetSuggestion = getMCPSuggestedDescription as jest.Mock;

function renderDescription(props: Partial<React.ComponentProps<typeof MCPServerDescription>> = {}) {
    const onChange = props.onChange ?? jest.fn();
    render(
        <MCPServerDescription
            serverID={'serverID' in props ? props.serverID : 'server-id'}
            description={props.description ?? ''}
            connectionSaved={props.connectionSaved ?? true}
            onChange={onChange}
        />,
    );
    return {onChange};
}

const autoFillButton = () => screen.getByRole('button', {name: 'Auto-fill from server'});

beforeEach(() => {
    mockGetSuggestion.mockReset();
});

describe('MCPServerDescription', () => {
    test.each([
        ['an unsaved server', {serverID: undefined}], // eslint-disable-line no-undefined
        ['unsaved connection changes', {connectionSaved: false}],
    ])('asks to save first for %s', (_name, props) => {
        renderDescription(props);

        expect((autoFillButton() as HTMLButtonElement).disabled).toBe(true);
        expect(screen.getByText('Save this server to auto-fill its description from the server.')).toBeTruthy();
    });

    test.each([
        ['description', 'Filled from the description the server reports. Edit it as needed, then save.'],
        ['instructions', 'The server has no description, so this was filled from its usage instructions. Edit it as needed, then save.'],
    ])('fills the field from the server %s and says where it came from', async (source, message) => {
        mockGetSuggestion.mockResolvedValue({description: 'Search and update tickets.', source, needsOAuth: false});
        const {onChange} = renderDescription();

        fireEvent.click(autoFillButton());

        expect(await screen.findByText(message)).toBeTruthy();
        expect(mockGetSuggestion).toHaveBeenCalledWith('server-id');
        expect(onChange).toHaveBeenCalledWith('Search and update tickets.');
    });

    test('leaves the field alone when the server does not describe itself', async () => {
        mockGetSuggestion.mockResolvedValue({description: '', source: '', needsOAuth: false});
        const {onChange} = renderDescription({description: 'Kept'});

        fireEvent.click(autoFillButton());

        expect(await screen.findByText("This server doesn't describe itself. Write a description instead.")).toBeTruthy();
        expect(onChange).not.toHaveBeenCalled();
    });

    test('offers to connect when the admin must sign in to the server first', async () => {
        const openSpy = jest.spyOn(window, 'open').mockImplementation(() => null);
        mockGetSuggestion.mockResolvedValue({description: '', source: '', needsOAuth: true, authURL: 'https://mm.example.com/start'});
        const {onChange} = renderDescription();

        fireEvent.click(autoFillButton());

        expect(await screen.findByText('Connect your account to this server, then try again.')).toBeTruthy();
        fireEvent.click(screen.getByRole('button', {name: 'Connect'}));
        expect(openSpy).toHaveBeenCalledWith('https://mm.example.com/start', '_blank', 'noopener,noreferrer');
        expect(onChange).not.toHaveBeenCalled();
        openSpy.mockRestore();
    });

    test('reports a failed lookup and can try again', async () => {
        mockGetSuggestion.mockRejectedValueOnce(new Error('bad gateway'));
        renderDescription();

        fireEvent.click(autoFillButton());

        expect(await screen.findByText("Couldn't get a description from this server.")).toBeTruthy();
        await waitFor(() => expect((autoFillButton() as HTMLButtonElement).disabled).toBe(false));
    });

    test('shows the character count and reports edits', () => {
        const {onChange} = renderDescription({description: 'Tickets'});

        expect(screen.getByText(/7\/500 characters/)).toBeTruthy();
        fireEvent.change(screen.getByDisplayValue('Tickets'), {target: {value: 'Tickets and sprints'}});
        expect(onChange).toHaveBeenCalledWith('Tickets and sprints');
    });
});
