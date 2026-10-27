// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {renderHook, waitFor} from '@testing-library/react';

import manifest from '@/manifest';

import {useServerSupportsScopedSessions} from './use_server_supports_scoped_sessions';

jest.mock('@/mm_webapp', () => ({}));

const mockGetAIBots = jest.fn();
jest.mock('@/client', () => ({
    getAIBots: (...args: unknown[]) => mockGetAIBots(...args),
}));

const pluginKey = `plugins-${manifest.id}`;

type PluginSlice = {scopedSessionsSupported?: boolean | null};
type TestState = Record<string, PluginSlice>;

// Handles the action fetchAndStoreBots dispatches, the way the plugin's
// reducer does.
function makeStore() {
    return createStore((state: TestState = {[pluginKey]: {}}, action: {type: string; scopedSessionsSupported?: boolean}): TestState => {
        if (action.type === 'SET_SCOPED_SESSIONS_SUPPORTED') {
            return {...state, [pluginKey]: {...state[pluginKey], scopedSessionsSupported: action.scopedSessionsSupported}};
        }
        return state;
    });
}

function renderSupportHook() {
    const store = makeStore();
    return renderHook(() => useServerSupportsScopedSessions(), {
        wrapper: ({children}: {children: React.ReactNode}) => <Provider store={store}>{children}</Provider>,
    });
}

describe('useServerSupportsScopedSessions', () => {
    beforeEach(() => {
        mockGetAIBots.mockReset();
    });

    test.each([
        {name: 'the server can create scoped sessions', reported: true, expected: true},
        {name: 'the server cannot create scoped sessions', reported: false, expected: false},
        {name: 'the response carries no value', reported: null, expected: false},
    ])('reports what /ai_bots says when $name', async ({reported, expected}) => {
        mockGetAIBots.mockResolvedValue({bots: [], scopedSessionsSupported: reported});

        const {result} = renderSupportHook();

        expect(result.current).toBeNull();
        await waitFor(() => expect(result.current).toBe(expected));
    });

    test('stays unknown when /ai_bots fails', async () => {
        mockGetAIBots.mockRejectedValue(new Error('offline'));

        const {result} = renderSupportHook();

        await waitFor(() => expect(mockGetAIBots).toHaveBeenCalled());
        expect(result.current).toBeNull();
    });
});
