// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {getUserMCPTools, type UserMCPServerInfo} from '@/client';
import {notifyMCPConnectionUpdated} from '@/hooks/use_mcp_connection_events';

import ConnectionRequestCard, {parseConnectionRequestArgs} from './connection_request_card';
import {ToolCall, ToolCallStatus} from './tool_types';

jest.mock('@/client', () => ({
    getUserMCPTools: jest.fn(),
}));

const mockGetUserMCPTools = getUserMCPTools as jest.MockedFunction<typeof getUserMCPTools>;

const authURL = 'http://localhost:8065/plugins/mattermost-ai/mcp/oauth/Jira/start';

function jiraServer(overrides: Partial<UserMCPServerInfo> = {}): UserMCPServerInfo {
    return {
        name: 'Jira',
        serverOrigin: 'https://jira.example.com/mcp',
        kind: 'remote',
        authenticated: false,
        needsOAuth: true,
        authURL,
        serviceAccountConfigured: false,
        tools: [],
        ...overrides,
    };
}

function makeTool(overrides: Partial<ToolCall> = {}): ToolCall {
    return {
        id: 'connect_1',
        name: 'RequestMCPConnection',
        description: '',
        status: ToolCallStatus.Pending,
        user_interaction: 'connect',
        ...overrides,
    };
}

function renderCard(props: Partial<React.ComponentProps<typeof ConnectionRequestCard>> = {}) {
    const onContinue = props.onContinue ?? jest.fn();
    const onSkip = props.onSkip ?? jest.fn();
    render(
        <IntlProvider locale='en'>
            <ConnectionRequestCard
                tool={props.tool ?? makeTool()}
                request={props.request ?? {server: 'Jira', reason: 'To look up your open issues.'}}
                isProcessing={props.isProcessing ?? false}
                localDecision={props.localDecision}
                canAnswer={props.canAnswer ?? true}
                onContinue={onContinue}
                onSkip={onSkip}
            />
        </IntlProvider>,
    );
    return {onContinue, onSkip};
}

beforeEach(() => {
    mockGetUserMCPTools.mockReset();
});

describe('parseConnectionRequestArgs', () => {
    test.each([
        ['well-formed request', {server: ' Jira ', reason: ' Find issues '}, {server: 'Jira', reason: 'Find issues'}],
        ['missing reason', {server: 'Jira'}, {server: 'Jira', reason: ''}],
        ['missing server', {reason: 'x'}, null],
        ['blank server', {server: '  '}, null],
        ['redacted arguments', undefined, null], // eslint-disable-line no-undefined
        ['array arguments', ['Jira'], null],
    ])('%s', (_name, args, expected) => {
        expect(parseConnectionRequestArgs(args as ToolCall['arguments'])).toEqual(expected);
    });
});

describe('ConnectionRequestCard', () => {
    test('offers Skip and Connect, and keeps only those after the sign-in window closes unfinished', async () => {
        mockGetUserMCPTools.mockResolvedValue({servers: [jiraServer()]});
        const openSpy = jest.spyOn(window, 'open').mockImplementation(() => null);
        const {onContinue} = renderCard();

        expect(screen.getByText('Connect Jira')).toBeTruthy();
        expect(screen.getByText('To look up your open issues.')).toBeTruthy();

        const connect = await screen.findByRole('button', {name: /^connect$/i});
        fireEvent.click(connect);
        expect(openSpy).toHaveBeenCalledWith(authURL, '_blank', 'noopener,noreferrer');

        act(() => {
            window.dispatchEvent(new Event('focus'));
        });
        await waitFor(() => expect(mockGetUserMCPTools).toHaveBeenCalledTimes(2));

        expect(screen.getByText(/Finish signing in in the new window/)).toBeTruthy();
        expect(screen.getAllByRole('button').map((button) => button.textContent)).toEqual(['Skip', 'Connect']);
        expect(onContinue).not.toHaveBeenCalled();
        openSpy.mockRestore();
    });

    test.each([
        ['the server reports the new connection', () => notifyMCPConnectionUpdated({status: 'connected', serverName: 'Jira'})],
        ['the user returns from the sign-in window', () => window.dispatchEvent(new Event('focus'))],
    ])('continues automatically once connected when %s', async (_name, signal) => {
        mockGetUserMCPTools.mockResolvedValueOnce({servers: [jiraServer()]});
        const openSpy = jest.spyOn(window, 'open').mockImplementation(() => null);
        const {onContinue} = renderCard();
        fireEvent.click(await screen.findByRole('button', {name: /^connect$/i}));

        mockGetUserMCPTools.mockResolvedValue({servers: [jiraServer({authenticated: true, authURL: undefined})]}); // eslint-disable-line no-undefined
        act(() => {
            signal();
        });

        await waitFor(() => expect(onContinue).toHaveBeenCalledTimes(1));
        expect(screen.getByText('Connected. Continuing…')).toBeTruthy();
        expect(screen.queryByRole('button')).toBeNull();

        act(() => {
            window.dispatchEvent(new Event('focus'));
        });
        await waitFor(() => expect(mockGetUserMCPTools).toHaveBeenCalledTimes(3));
        expect(onContinue).toHaveBeenCalledTimes(1);
        openSpy.mockRestore();
    });

    test('continues automatically when the user is already connected', async () => {
        mockGetUserMCPTools.mockResolvedValue({servers: [jiraServer({authenticated: true, authURL: undefined})]}); // eslint-disable-line no-undefined
        const {onContinue} = renderCard();

        await waitFor(() => expect(onContinue).toHaveBeenCalledTimes(1));
        expect(screen.queryByRole('button')).toBeNull();
    });

    test('does not re-check on focus before the sign-in window was opened', async () => {
        mockGetUserMCPTools.mockResolvedValue({servers: [jiraServer()]});
        renderCard();
        await screen.findByRole('button', {name: /^connect$/i});

        act(() => {
            window.dispatchEvent(new Event('focus'));
        });
        expect(mockGetUserMCPTools).toHaveBeenCalledTimes(1);
    });

    test('ignores connection events for other servers', async () => {
        mockGetUserMCPTools.mockResolvedValue({servers: [jiraServer()]});
        renderCard();
        await screen.findByRole('button', {name: /^connect$/i});

        act(() => {
            notifyMCPConnectionUpdated({status: 'connected', serverName: 'GitHub'});
        });
        expect(mockGetUserMCPTools).toHaveBeenCalledTimes(1);
    });

    test('explains when the server cannot be connected from here and only allows skipping', async () => {
        mockGetUserMCPTools.mockResolvedValue({servers: []});
        const {onSkip} = renderCard();

        expect(await screen.findByText(/can't be connected from here/)).toBeTruthy();
        expect(screen.getAllByRole('button').map((button) => button.textContent)).toEqual(['Skip']);

        fireEvent.click(screen.getByRole('button', {name: /^skip$/i}));
        expect(onSkip).toHaveBeenCalledTimes(1);
    });

    test('offers to try again when the connection lookup fails', async () => {
        mockGetUserMCPTools.mockRejectedValueOnce(new Error('network'));
        renderCard();

        expect(await screen.findByText("Couldn't check your connection.")).toBeTruthy();
        expect(screen.getAllByRole('button').map((button) => button.textContent)).toEqual(['Skip', 'Try again']);

        mockGetUserMCPTools.mockResolvedValueOnce({servers: [jiraServer()]});
        fireEvent.click(screen.getByRole('button', {name: /^try again$/i}));

        expect(await screen.findByRole('button', {name: /^connect$/i})).toBeTruthy();
        expect(screen.queryByText("Couldn't check your connection.")).toBeNull();
    });

    test.each([
        ['connected result', makeTool({status: ToolCallStatus.Success, result: '{"server":"Jira","connected":true}'}), true, 'Connected'],
        ['not connected result', makeTool({status: ToolCallStatus.Success, result: '{"server":"Jira","connected":false}'}), true, 'Not connected'],
        ['skipped request', makeTool({status: ToolCallStatus.Rejected}), true, 'Skipped'],
        ['pending request seen by another user', makeTool(), false, 'Waiting for the requester to connect'],
    ])('shows the %s without controls', async (_name, tool, canAnswer, expectedStatus) => {
        renderCard({tool, canAnswer});

        expect(screen.getByText(expectedStatus)).toBeTruthy();
        expect(screen.queryByRole('button')).toBeNull();
        await waitFor(() => expect(mockGetUserMCPTools).not.toHaveBeenCalled());
    });
});
