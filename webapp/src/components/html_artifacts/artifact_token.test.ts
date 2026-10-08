// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {getArtifactToken} from '@/client';

import {fetchArtifactToken, resetArtifactTokenCache} from './artifact_token';

jest.mock('@/client', () => ({
    getArtifactToken: jest.fn(),
}));

const info = (token: string) => ({token, tooLarge: false});

const mockGetToken = getArtifactToken as jest.MockedFunction<typeof getArtifactToken>;

beforeEach(() => {
    resetArtifactTokenCache();
    mockGetToken.mockReset();
});

describe('fetchArtifactToken', () => {
    test('shares one fetch per user and file', async () => {
        mockGetToken.mockResolvedValue(info('t1'));
        const [a, b] = await Promise.all([fetchArtifactToken('f1', 'u1'), fetchArtifactToken('f1', 'u1')]);
        expect(a).toEqual(info('t1'));
        expect(b).toEqual(info('t1'));
        expect(mockGetToken).toHaveBeenCalledTimes(1);
    });

    test.each([
        {name: 'different user, same file', second: {fileId: 'f1', userId: 'u2'}},
        {name: 'same user, different file', second: {fileId: 'f2', userId: 'u1'}},
    ])('$name fetches its own token', async ({second}) => {
        mockGetToken.mockResolvedValueOnce(info('first')).mockResolvedValueOnce(info('second'));
        expect(await fetchArtifactToken('f1', 'u1')).toEqual(info('first'));
        expect(await fetchArtifactToken(second.fileId, second.userId)).toEqual(info('second'));
        expect(mockGetToken).toHaveBeenCalledTimes(2);
    });

    test('a failed fetch is retried on the next call', async () => {
        mockGetToken.mockRejectedValueOnce(new Error('boom')).mockResolvedValueOnce(info('t2'));
        await expect(fetchArtifactToken('f1', 'u1')).rejects.toThrow('boom');
        expect(await fetchArtifactToken('f1', 'u1')).toEqual(info('t2'));
        expect(mockGetToken).toHaveBeenCalledTimes(2);
    });
});
