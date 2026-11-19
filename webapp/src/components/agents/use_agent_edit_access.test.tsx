// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {act, renderHook, waitFor} from '@testing-library/react';

import {useAgentEditAccess} from './use_agent_edit_access';

const mockGetAgents = jest.fn();
const mockGetServices = jest.fn();
const mockGetMySettingsAccess = jest.fn();
const mockUserHasSystemPermission = jest.fn();

jest.mock('@/client', () => ({
    getAgents: (...args: unknown[]) => mockGetAgents(...args),
    getServices: (...args: unknown[]) => mockGetServices(...args),
    getMySettingsAccess: (...args: unknown[]) => mockGetMySettingsAccess(...args),
}));

jest.mock('@/utils/permissions', () => ({userHasSystemPermission: (...args: unknown[]) => mockUserHasSystemPermission(...args)}));

const wrapper = ({children}: {children: React.ReactNode}) => (
    <Provider store={createStore(() => ({entities: {users: {currentUserId: 'me'}}}))}>{children}</Provider>
);

describe('useAgentEditAccess', () => {
    beforeEach(() => {
        mockGetAgents.mockReset();
        mockGetServices.mockReset().mockResolvedValue([]);
        mockGetMySettingsAccess.mockReset().mockResolvedValue({level: 'none', manageOthersAgents: false});
        mockUserHasSystemPermission.mockReset().mockReturnValue(true);
    });

    // The agent managers list grants manage_others_agent to people whose roles do not: the editor
    // gets the service list for them as it does for the permission.
    test.each([
        {name: 'an agent manager from the System Console list', manageOthersAgents: true, getsServices: true},
        {name: 'someone who manages only this agent', manageOthersAgents: false, getsServices: false},
    ])('$name, without agent permissions from a role', async ({manageOthersAgents, getsServices}) => {
        mockUserHasSystemPermission.mockReturnValue(false);
        mockGetMySettingsAccess.mockResolvedValue({level: 'none', manageOthersAgents});
        mockGetServices.mockResolvedValue([{id: 's1', name: 'Service', type: 'openai'}]);
        mockGetAgents.mockResolvedValue({agents: [{id: 'a1', botUserID: 'bot1', canManage: true}]});

        const {result} = renderHook(() => useAgentEditAccess('bot1'), {wrapper});
        await waitFor(() => expect(mockGetMySettingsAccess).toHaveBeenCalled());
        await waitFor(() => expect(result.current.access.status).toBe('allowed'));
        await act(async () => {/* let a reload for the delegated access settle */});

        const services = (result.current.access as {services: unknown[]}).services;
        expect(services).toHaveLength(getsServices ? 1 : 0);
        expect(mockGetServices).toHaveBeenCalledTimes(getsServices ? 1 : 0);
    });

    test('a reload keeps the resolved access until the new one arrives', async () => {
        mockGetAgents.mockResolvedValueOnce({agents: [{id: 'a1', botUserID: 'bot1', displayName: 'One', canManage: true}]});
        const {result} = renderHook(() => useAgentEditAccess('bot1'), {wrapper});
        expect(result.current.access.status).toBe('loading');
        await waitFor(() => expect(result.current.access.status).toBe('allowed'));

        let resolveReload: (value: unknown) => void = () => null;
        mockGetAgents.mockReturnValueOnce(new Promise((resolve) => {
            resolveReload = resolve;
        }));
        act(() => result.current.reload());

        // Still allowed while the reload is pending: an editor showing it is not unmounted.
        expect(result.current.access.status).toBe('allowed');

        await act(async () => {
            resolveReload({agents: [{id: 'a1', botUserID: 'bot1', displayName: 'Renamed', canManage: true}]});
        });
        await waitFor(() => expect((result.current.access as {agent: {displayName: string}}).agent.displayName).toBe('Renamed'));
    });

    test('another agent starts from loading, not the previous agent\'s access', async () => {
        mockGetAgents.mockResolvedValue({agents: [{id: 'a1', botUserID: 'bot1', canManage: true}, {id: 'a2', botUserID: 'bot2', canManage: true}]});
        const {result, rerender} = renderHook(({botId}) => useAgentEditAccess(botId), {wrapper, initialProps: {botId: 'bot1'}});
        await waitFor(() => expect(result.current.access.status).toBe('allowed'));

        rerender({botId: 'bot2'});
        expect(result.current.access.status).toBe('loading');
    });
});
