// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook, waitFor} from '@testing-library/react';

import {resetToolPreferences, useToolPreferences} from './use_tool_preferences';

const mockGetPrefs = jest.fn();
jest.mock('@/client', () => ({
    getUserToolPreferences: () => mockGetPrefs(),
}));

describe('useToolPreferences', () => {
    beforeEach(() => {
        resetToolPreferences();
        mockGetPrefs.mockReset();
        jest.spyOn(console, 'error').mockImplementation(() => null);
    });

    afterEach(() => {
        jest.restoreAllMocks();
    });

    test('controls mounted together share one load and one copy', async () => {
        mockGetPrefs.mockResolvedValue({disabled_servers: ['jira']});
        const header = renderHook(() => useToolPreferences('a'));
        const rhs = renderHook(() => useToolPreferences('a'));
        await waitFor(() => expect(header.result.current.status).toBe('ready'));
        expect(mockGetPrefs).toHaveBeenCalledTimes(1);

        act(() => header.result.current.setDisabledServers(['github']));
        expect(rhs.result.current.disabledServers).toEqual(['github']);
    });

    test('reloads when the agent changes', async () => {
        mockGetPrefs.mockResolvedValueOnce({disabled_servers: []}).mockResolvedValueOnce({disabled_servers: ['jira']});
        const {result, rerender} = renderHook(({agentId}) => useToolPreferences(agentId), {initialProps: {agentId: 'a'}});
        await waitFor(() => expect(result.current.status).toBe('ready'));

        rerender({agentId: 'b'});
        await waitFor(() => expect(result.current.disabledServers).toEqual(['jira']));
        expect(mockGetPrefs).toHaveBeenCalledTimes(2);
    });

    test('a failed reload keeps the loaded preferences instead of an error', async () => {
        mockGetPrefs.mockResolvedValueOnce({disabled_servers: ['jira']}).mockRejectedValueOnce(new Error('down'));
        const {result, rerender} = renderHook(({agentId}) => useToolPreferences(agentId), {initialProps: {agentId: 'a'}});
        await waitFor(() => expect(result.current.status).toBe('ready'));

        rerender({agentId: 'b'});
        await waitFor(() => expect(mockGetPrefs).toHaveBeenCalledTimes(2));
        await act(async () => null);
        expect(result.current.status).toBe('ready');
        expect(result.current.disabledServers).toEqual(['jira']);
    });
});
