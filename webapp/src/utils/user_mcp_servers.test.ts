// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {getUserMCPTools, type UserMCPToolsResponse} from '@/client';

import {getUserMCPServers, invalidateUserMCPServers, setUserMCPServers} from './user_mcp_servers';

jest.mock('@/client', () => ({
    getUserMCPTools: jest.fn(),
}));

const mockGetUserMCPTools = getUserMCPTools as jest.MockedFunction<typeof getUserMCPTools>;

function response(name: string): UserMCPToolsResponse {
    return {servers: [{name, serverOrigin: name, kind: 'remote', authenticated: false, needsOAuth: true, serviceAccountConfigured: false, tools: []}]};
}

function deferred<T>() {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((r) => {
        resolve = r;
    });
    return {promise, resolve};
}

beforeEach(() => {
    invalidateUserMCPServers();
    mockGetUserMCPTools.mockReset();
    jest.useRealTimers();
});

describe('getUserMCPServers', () => {
    test('concurrent reads share one request', async () => {
        const pending = deferred<UserMCPToolsResponse>();
        mockGetUserMCPTools.mockReturnValue(pending.promise);

        const a = getUserMCPServers();
        const b = getUserMCPServers();
        pending.resolve(response('Jira'));

        expect(await a).toEqual(response('Jira'));
        expect(await b).toEqual(response('Jira'));
        expect(mockGetUserMCPTools).toHaveBeenCalledTimes(1);
    });

    test('serves a fresh cache and refetches once it expires', async () => {
        jest.useFakeTimers();
        mockGetUserMCPTools.mockResolvedValueOnce(response('A')).mockResolvedValueOnce(response('B'));

        expect(await getUserMCPServers()).toEqual(response('A'));
        expect(await getUserMCPServers()).toEqual(response('A'));
        expect(mockGetUserMCPTools).toHaveBeenCalledTimes(1);

        jest.advanceTimersByTime(31 * 1000);
        expect(await getUserMCPServers()).toEqual(response('B'));
        expect(mockGetUserMCPTools).toHaveBeenCalledTimes(2);
    });

    test('invalidation forces a refetch and discards an in-flight stale response', async () => {
        const stale = deferred<UserMCPToolsResponse>();
        mockGetUserMCPTools.mockReturnValueOnce(stale.promise).mockResolvedValueOnce(response('fresh'));

        const first = getUserMCPServers();
        invalidateUserMCPServers();
        expect(await getUserMCPServers()).toEqual(response('fresh'));

        stale.resolve(response('stale'));
        await first;
        expect(await getUserMCPServers()).toEqual(response('fresh'));
        expect(mockGetUserMCPTools).toHaveBeenCalledTimes(2);
    });

    test('a failed request is not cached', async () => {
        mockGetUserMCPTools.mockRejectedValueOnce(new Error('boom')).mockResolvedValueOnce(response('A'));

        await expect(getUserMCPServers()).rejects.toThrow('boom');
        expect(await getUserMCPServers()).toEqual(response('A'));
    });

    test('a seeded response is served without a request', async () => {
        setUserMCPServers(response('seeded'));

        expect(await getUserMCPServers()).toEqual(response('seeded'));
        expect(mockGetUserMCPTools).not.toHaveBeenCalled();
    });
});
