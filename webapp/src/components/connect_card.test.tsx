// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {getUserMCPTools, UserMCPServerInfo} from '@/client';
import {notifyMCPConnectionUpdated} from '@/hooks/use_mcp_connection_events';
import {invalidateUserMCPServers} from '@/utils/user_mcp_servers';

import ConnectCard, {ConnectArgs, parseConnectArgs} from './connect_card';
import {ToolCall, ToolCallStatus} from './tool_types';

jest.mock('@/client', () => ({
    getUserMCPTools: jest.fn(),
}));

const mockGetUserMCPTools = getUserMCPTools as jest.MockedFunction<typeof getUserMCPTools>;

const AUTH_URL = 'http://localhost/plugins/mattermost-ai/oauth/start?server=Jira';

function makeServer(overrides: Partial<UserMCPServerInfo> = {}): UserMCPServerInfo {
    return {
        name: 'Jira',
        serverOrigin: 'remote:jira',
        kind: 'remote',
        authenticated: false,
        needsOAuth: true,
        authURL: AUTH_URL,
        serviceAccountConfigured: false,
        tools: [],
        ...overrides,
    };
}

function mockServers(...servers: UserMCPServerInfo[]) {
    mockGetUserMCPTools.mockResolvedValue({servers});
}

function makeTool(overrides: Partial<ToolCall> = {}): ToolCall {
    return {
        id: 'c_1',
        name: 'ConnectMCPServer',
        description: '',
        status: ToolCallStatus.Pending,
        user_interaction: 'connect',
        arguments: {server: 'Jira'},
        ...overrides,
    };
}

function renderCard(props: Partial<React.ComponentProps<typeof ConnectCard>> = {}) {
    const onAccept = props.onAccept ?? jest.fn();
    const onSkip = props.onSkip ?? jest.fn();
    const utils = render(
        <IntlProvider locale='en'>
            <ConnectCard
                tool={props.tool ?? makeTool()}
                server={props.server === undefined ? 'Jira' : props.server} // eslint-disable-line no-undefined
                isProcessing={props.isProcessing ?? false}
                localDecision={props.localDecision}
                canAnswer={props.canAnswer ?? true}
                onAccept={onAccept}
                onSkip={onSkip}
            />
        </IntlProvider>,
    );
    return {...utils, onAccept, onSkip};
}

let openSpy: jest.SpyInstance;

beforeEach(() => {
    invalidateUserMCPServers();
    mockGetUserMCPTools.mockReset();
    openSpy = jest.spyOn(window, 'open').mockImplementation(() => null);
});

afterEach(() => {
    openSpy.mockRestore();
});

describe('parseConnectArgs', () => {
    test.each<{name: string; args: ToolCall['arguments']; expected: ConnectArgs | null}>([
        {name: 'valid server', args: {server: 'Jira'}, expected: {server: 'Jira'}},
        {name: 'null (redacted)', args: null, expected: null},
        {name: 'missing server', args: {}, expected: null},
        {name: 'blank server', args: {server: '  '}, expected: null},
        {name: 'non-string server', args: {server: 3}, expected: null},
        {name: 'array', args: ['Jira'], expected: null},
    ])('$name', ({args, expected}) => {
        expect(parseConnectArgs(args)).toEqual(expected);
    });
});

describe('ConnectCard', () => {
    test('requester sees server name, Connect and Skip; Connect opens the server auth URL', async () => {
        mockServers(makeServer());
        const {onAccept} = renderCard();

        expect(screen.getByTestId('connect-mcp-server-name').textContent).toBe('Jira');
        const connect = screen.getByTestId('connect-mcp-connect-button') as HTMLButtonElement;
        await waitFor(() => expect(connect.disabled).toBe(false));
        expect(screen.getByTestId('connect-mcp-skip-button')).not.toBeNull();
        expect(screen.queryByTestId('connect-mcp-continue-button')).toBeNull();

        fireEvent.click(connect);
        expect(openSpy).toHaveBeenCalledWith(AUTH_URL, '_blank', 'noopener,noreferrer');

        // Manual fallback appears once the connect flow has started.
        fireEvent.click(screen.getByTestId('connect-mcp-continue-button'));
        expect(onAccept).toHaveBeenCalledTimes(1);
    });

    test('matches the server by exact configured name', async () => {
        mockServers(makeServer({name: 'Jira Cloud', authURL: 'http://other'}), makeServer());
        renderCard();

        const connect = screen.getByTestId('connect-mcp-connect-button') as HTMLButtonElement;
        await waitFor(() => expect(connect.disabled).toBe(false));
        fireEvent.click(connect);
        expect(openSpy).toHaveBeenCalledWith(AUTH_URL, '_blank', 'noopener,noreferrer');
    });

    test('already-authenticated server offers Continue directly', async () => {
        mockServers(makeServer({authenticated: true, needsOAuth: false}));
        const {onAccept} = renderCard();

        const cont = await screen.findByTestId('connect-mcp-continue-button');
        expect(screen.queryByTestId('connect-mcp-connect-button')).toBeNull();
        fireEvent.click(cont);
        expect(onAccept).toHaveBeenCalledTimes(1);
    });

    test('Skip calls onSkip', async () => {
        mockServers(makeServer());
        const {onSkip, onAccept} = renderCard();

        fireEvent.click(screen.getByTestId('connect-mcp-skip-button'));
        expect(onSkip).toHaveBeenCalledTimes(1);
        expect(onAccept).not.toHaveBeenCalled();
        await waitFor(() => expect(mockGetUserMCPTools).toHaveBeenCalled());
    });

    test('connection websocket event after Connect auto-submits acceptance once', async () => {
        mockServers(makeServer());
        const {onAccept} = renderCard();

        const connect = screen.getByTestId('connect-mcp-connect-button') as HTMLButtonElement;
        await waitFor(() => expect(connect.disabled).toBe(false));
        fireEvent.click(connect);

        mockServers(makeServer({authenticated: true, needsOAuth: false}));
        act(() => {
            notifyMCPConnectionUpdated({status: 'connected', serverName: 'Jira'});
        });
        await waitFor(() => expect(onAccept).toHaveBeenCalledTimes(1));

        act(() => {
            notifyMCPConnectionUpdated({status: 'connected', serverName: 'Jira'});
        });
        await waitFor(() => expect(mockGetUserMCPTools).toHaveBeenCalledTimes(3));
        expect(onAccept).toHaveBeenCalledTimes(1);
    });

    test('connection event without clicking Connect only refreshes status', async () => {
        mockServers(makeServer());
        const {onAccept} = renderCard();
        await waitFor(() => expect(mockGetUserMCPTools).toHaveBeenCalledTimes(1));

        mockServers(makeServer({authenticated: true, needsOAuth: false}));
        act(() => {
            notifyMCPConnectionUpdated({status: 'connected', serverName: 'Jira'});
        });

        await screen.findByTestId('connect-mcp-continue-button');
        expect(onAccept).not.toHaveBeenCalled();
    });

    test('connection event that leaves the server unauthenticated does not submit', async () => {
        mockServers(makeServer());
        const {onAccept} = renderCard();

        const connect = screen.getByTestId('connect-mcp-connect-button') as HTMLButtonElement;
        await waitFor(() => expect(connect.disabled).toBe(false));
        fireEvent.click(connect);

        act(() => {
            notifyMCPConnectionUpdated({status: 'disconnected', serverName: 'Other'});
        });
        await waitFor(() => expect(mockGetUserMCPTools).toHaveBeenCalledTimes(2));
        expect(onAccept).not.toHaveBeenCalled();
    });

    test('server missing from the user tool list still offers Continue', async () => {
        mockServers();
        renderCard();

        await screen.findByTestId('connect-mcp-continue-button');
        expect((screen.getByTestId('connect-mcp-connect-button') as HTMLButtonElement).disabled).toBe(true);
    });

    test.each([
        {name: 'named server', server: 'Jira', text: 'Waiting for the requester to connect Jira'},
        {name: 'redacted server', server: null, text: 'Waiting for the requester to connect a tool provider'},
    ])('non-requester sees a read-only waiting state ($name)', ({server, text}) => {
        renderCard({canAnswer: false, server});

        expect(screen.getByTestId('connect-mcp-card')).not.toBeNull();
        expect(screen.getByTestId('connect-mcp-waiting').textContent).toBe(text);
        expect(screen.queryByTestId('connect-mcp-connect-button')).toBeNull();
        expect(screen.queryByTestId('connect-mcp-skip-button')).toBeNull();
        expect(screen.queryByTestId('connect-mcp-continue-button')).toBeNull();
        expect(mockGetUserMCPTools).not.toHaveBeenCalled();
    });

    test.each([
        {name: 'accepted (server still verifying)', status: ToolCallStatus.Accepted, testID: 'connect-mcp-submitting'},
        {name: 'success', status: ToolCallStatus.Success, testID: 'connect-mcp-connected'},
        {name: 'rejected', status: ToolCallStatus.Rejected, testID: 'connect-mcp-skipped'},
        {name: 'error', status: ToolCallStatus.Error, testID: 'connect-mcp-failed'},
    ])('resolved $name call renders its status without buttons', ({status, testID}) => {
        renderCard({tool: makeTool({status})});

        expect(screen.getByTestId(testID)).not.toBeNull();
        expect(screen.queryByTestId('connect-mcp-skip-button')).toBeNull();
        expect(screen.queryByTestId('connect-mcp-waiting')).toBeNull();
        expect(mockGetUserMCPTools).not.toHaveBeenCalled();
    });

    test('an auto-approved status is never shown as connected', () => {
        renderCard({tool: makeTool({status: ToolCallStatus.AutoApproved})});

        expect(screen.getByTestId('connect-mcp-card')).not.toBeNull();
        expect(screen.queryByTestId('connect-mcp-connected')).toBeNull();
        expect(screen.queryByTestId('connect-mcp-skip-button')).toBeNull();
    });

    test('a status lookup failure has its own message and still offers Continue', async () => {
        mockGetUserMCPTools.mockRejectedValue(new Error('boom'));
        const {onAccept} = renderCard();

        expect(await screen.findByTestId('connect-mcp-status-error')).not.toBeNull();
        expect(screen.queryByText(/connection link for Jira is not available/)).toBeNull();
        expect((screen.getByTestId('connect-mcp-connect-button') as HTMLButtonElement).disabled).toBe(true);
        fireEvent.click(screen.getByTestId('connect-mcp-continue-button'));
        expect(onAccept).toHaveBeenCalledTimes(1);
    });

    test('a connection event after a failed lookup retries and recovers', async () => {
        mockGetUserMCPTools.mockRejectedValue(new Error('boom'));
        renderCard();
        await screen.findByTestId('connect-mcp-status-error');

        mockServers(makeServer());
        act(() => {
            notifyMCPConnectionUpdated({status: 'connected', serverName: 'Other'});
        });

        const connect = screen.getByTestId('connect-mcp-connect-button') as HTMLButtonElement;
        await waitFor(() => expect(connect.disabled).toBe(false));
        expect(screen.queryByTestId('connect-mcp-status-error')).toBeNull();
    });

    test('several pending cards share one status request', async () => {
        mockServers(makeServer());
        render(
            <IntlProvider locale='en'>
                <ConnectCard
                    tool={makeTool({id: 'c_1'})}
                    server='Jira'
                    isProcessing={false}
                    canAnswer={true}
                    onAccept={jest.fn()}
                    onSkip={jest.fn()}
                />
                <ConnectCard
                    tool={makeTool({id: 'c_2'})}
                    server='Jira'
                    isProcessing={false}
                    canAnswer={true}
                    onAccept={jest.fn()}
                    onSkip={jest.fn()}
                />
            </IntlProvider>,
        );

        await waitFor(() => {
            for (const button of screen.getAllByTestId('connect-mcp-connect-button')) {
                expect((button as HTMLButtonElement).disabled).toBe(false);
            }
        });
        expect(mockGetUserMCPTools).toHaveBeenCalledTimes(1);
    });

    test('a local decision shows the submitting state without buttons', () => {
        renderCard({localDecision: true});

        expect(screen.getByTestId('connect-mcp-submitting')).not.toBeNull();
        expect(screen.queryByTestId('connect-mcp-skip-button')).toBeNull();
    });
});
