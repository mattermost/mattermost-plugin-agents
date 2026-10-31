// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {act, renderHook, waitFor} from '@testing-library/react';

import {useAgentEditAccess} from './use_agent_edit_access';

const mockGetAgents = jest.fn();
const mockGetServices = jest.fn();

jest.mock('@/client', () => ({
    getAgents: (...args: unknown[]) => mockGetAgents(...args),
    getServices: (...args: unknown[]) => mockGetServices(...args),
}));

jest.mock('@/utils/permissions', () => ({userHasSystemPermission: () => true}));
jest.mock('./agent_access', () => ({...jest.requireActual('./agent_access'), canManageAgent: () => true}));

const wrapper = ({children}: {children: React.ReactNode}) => (
    <Provider store={createStore(() => ({entities: {users: {currentUserId: 'me'}}}))}>{children}</Provider>
);

describe('useAgentEditAccess', () => {
    beforeEach(() => {
        mockGetAgents.mockReset();
        mockGetServices.mockReset().mockResolvedValue([]);
    });

    test('a reload keeps the resolved access until the new one arrives', async () => {
        mockGetAgents.mockResolvedValueOnce({agents: [{id: 'a1', botUserID: 'bot1', displayName: 'One'}]});
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
            resolveReload({agents: [{id: 'a1', botUserID: 'bot1', displayName: 'Renamed'}]});
        });
        await waitFor(() => expect((result.current.access as {agent: {displayName: string}}).agent.displayName).toBe('Renamed'));
    });

    test('another agent starts from loading, not the previous agent\'s access', async () => {
        mockGetAgents.mockResolvedValue({agents: [{id: 'a1', botUserID: 'bot1'}, {id: 'a2', botUserID: 'bot2'}]});
        const {result, rerender} = renderHook(({botId}) => useAgentEditAccess(botId), {wrapper, initialProps: {botId: 'bot1'}});
        await waitFor(() => expect(result.current.access.status).toBe('allowed'));

        rerender({botId: 'bot2'});
        expect(result.current.access.status).toBe('loading');
    });
});
