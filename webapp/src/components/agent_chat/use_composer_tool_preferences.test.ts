// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook, waitFor} from '@testing-library/react';

import {resetToolPreferences, useComposerToolPreferences} from './use_composer_tool_preferences';

const mockGetPrefs = jest.fn();
jest.mock('@/client', () => ({
    getUserToolPreferences: () => mockGetPrefs(),
}));

describe('useComposerToolPreferences', () => {
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
        const header = renderHook(() => useComposerToolPreferences('a'));
        const composer = renderHook(() => useComposerToolPreferences('a'));
        await waitFor(() => expect(header.result.current.status).toBe('ready'));
        expect(mockGetPrefs).toHaveBeenCalledTimes(1);

        act(() => header.result.current.setDisabledServers(['github']));
        expect(composer.result.current.disabledServers).toEqual(['github']);
    });

    test('reloads when the agent changes', async () => {
        mockGetPrefs.mockResolvedValueOnce({disabled_servers: []}).mockResolvedValueOnce({disabled_servers: ['jira']});
        const {result, rerender} = renderHook(({agentId}) => useComposerToolPreferences(agentId), {initialProps: {agentId: 'a'}});
        await waitFor(() => expect(result.current.status).toBe('ready'));

        rerender({agentId: 'b'});
        await waitFor(() => expect(result.current.disabledServers).toEqual(['jira']));
        expect(mockGetPrefs).toHaveBeenCalledTimes(2);
    });

    test('a failed reload keeps the loaded preferences instead of an error', async () => {
        mockGetPrefs.mockResolvedValueOnce({disabled_servers: ['jira']}).mockRejectedValueOnce(new Error('down'));
        const {result, rerender} = renderHook(({agentId}) => useComposerToolPreferences(agentId), {initialProps: {agentId: 'a'}});
        await waitFor(() => expect(result.current.status).toBe('ready'));

        rerender({agentId: 'b'});
        await waitFor(() => expect(mockGetPrefs).toHaveBeenCalledTimes(2));
        await act(async () => null);
        expect(result.current.status).toBe('ready');
        expect(result.current.disabledServers).toEqual(['jira']);
    });
});
