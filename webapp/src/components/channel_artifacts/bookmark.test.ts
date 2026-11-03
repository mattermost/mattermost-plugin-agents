// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Client4} from '@/client';

import {
    artifactIdFromBookmarkUrl,
    buildLibraryArtifactBookmarkUrl,
    findLibraryArtifactBookmark,
    parseLibraryArtifactIdFromSearch,
    stripLibraryArtifactQuery,
    syncLibraryArtifactBookmarkTitle,
} from './bookmark';

jest.mock('@/client', () => ({
    Client4: {
        getChannelBookmarks: jest.fn(),
        updateChannelBookmark: jest.fn(),
        createChannelBookmark: jest.fn(),
        deleteChannelBookmark: jest.fn(),
        getUrl: jest.fn(() => 'http://localhost:8065'),
    },
}));

const mockGetBookmarks = Client4.getChannelBookmarks as jest.Mock;
const mockUpdateBookmark = Client4.updateChannelBookmark as jest.Mock;

describe('channel artifact bookmark helpers', () => {
    beforeEach(() => {
        mockGetBookmarks.mockReset();
        mockUpdateBookmark.mockReset();
    });

    test('buildLibraryArtifactBookmarkUrl encodes path and query', () => {
        expect(buildLibraryArtifactBookmarkUrl(
            'http://localhost:8065/',
            'test',
            'off-topic',
            'abcdefghijklmnopqrstuvwxyz',
        )).toBe('http://localhost:8065/test/channels/off-topic?mm_ai_library_artifact=abcdefghijklmnopqrstuvwxyz');
    });

    test('artifactIdFromBookmarkUrl reads absolute and relative links', () => {
        expect(artifactIdFromBookmarkUrl(
            'http://localhost:8065/test/channels/off-topic?mm_ai_library_artifact=abc',
        )).toBe('abc');
        expect(artifactIdFromBookmarkUrl('/test/channels/off-topic?mm_ai_library_artifact=xyz&x=1')).toBe('xyz');
        expect(artifactIdFromBookmarkUrl('http://localhost:8065/test/channels/off-topic')).toBeNull();
    });

    test('findLibraryArtifactBookmark matches a same-origin library link', () => {
        const hit = findLibraryArtifactBookmark([
            {id: '1', type: 'link', link_url: 'http://x/a?mm_ai_library_artifact=want'} as any,
            {id: '2', type: 'link', link_url: '/test/channels/off-topic?mm_ai_library_artifact=other'} as any,
            {id: '3', type: 'link', link_url: 'http://localhost:8065/test/channels/off-topic?mm_ai_library_artifact=want'} as any,
        ], 'want');
        expect(hit?.id).toBe('3');
        expect(findLibraryArtifactBookmark([
            {id: '2', type: 'link', link_url: '/test/channels/off-topic?mm_ai_library_artifact=other'} as any,
        ], 'other')?.id).toBe('2');
        expect(findLibraryArtifactBookmark([
            {id: '4', type: 'link', link_url: '//foreign.example/a?mm_ai_library_artifact=want'} as any,
        ], 'want')).toBeNull();
    });

    test('parse and strip query helpers', () => {
        expect(parseLibraryArtifactIdFromSearch('?mm_ai_library_artifact=id1&foo=1')).toBe('id1');
        expect(stripLibraryArtifactQuery('/t/channels/c', '?mm_ai_library_artifact=id1&foo=1', '#h')).
            toBe('/t/channels/c?foo=1#h');
        expect(stripLibraryArtifactQuery('/t/channels/c', '?mm_ai_library_artifact=id1', '')).
            toBe('/t/channels/c');
    });

    test('syncLibraryArtifactBookmarkTitle updates when display name differs', async () => {
        mockGetBookmarks.mockResolvedValue([
            {
                id: 'bm1',
                type: 'link',
                channel_id: 'ch1',
                display_name: 'Old title',
                link_url: 'http://localhost:8065/t/channels/c?mm_ai_library_artifact=art1',
            },
        ]);
        mockUpdateBookmark.mockResolvedValue({
            updated: {
                id: 'bm1',
                type: 'link',
                channel_id: 'ch1',
                display_name: 'New title',
                link_url: 'http://localhost:8065/t/channels/c?mm_ai_library_artifact=art1',
            },
        });

        const dispatch = jest.fn();
        const state = {websocket: {connectionId: 'conn1'}} as any;
        const updated = await syncLibraryArtifactBookmarkTitle(state, 'ch1', 'art1', 'New title', dispatch);

        expect(updated).toBe(true);
        expect(mockUpdateBookmark).toHaveBeenCalledWith(
            'ch1',
            'bm1',
            {display_name: 'New title'},
            'conn1',
        );
        expect(dispatch).toHaveBeenCalledWith(expect.objectContaining({
            type: 'RECEIVED_BOOKMARK',
            data: expect.objectContaining({id: 'bm1', display_name: 'New title'}),
        }));
    });

    test('syncLibraryArtifactBookmarkTitle no-ops when title already matches', async () => {
        mockGetBookmarks.mockResolvedValue([
            {
                id: 'bm1',
                type: 'link',
                display_name: 'Same',
                link_url: 'http://localhost:8065/t/channels/c?mm_ai_library_artifact=art1',
            },
        ]);

        const updated = await syncLibraryArtifactBookmarkTitle({} as any, 'ch1', 'art1', 'Same');

        expect(updated).toBe(false);
        expect(mockUpdateBookmark).not.toHaveBeenCalled();
    });

    test('syncLibraryArtifactBookmarkTitle no-ops when no bookmark exists', async () => {
        mockGetBookmarks.mockResolvedValue([]);

        const updated = await syncLibraryArtifactBookmarkTitle({} as any, 'ch1', 'art1', 'Title');

        expect(updated).toBe(false);
        expect(mockUpdateBookmark).not.toHaveBeenCalled();
    });
});
