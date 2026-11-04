// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Client4} from '@/client';

import {
    artifactIdFromBookmarkUrl,
    buildLibraryArtifactBookmarkUrl,
    canAddChannelBookmarks,
    findLibraryArtifactBookmark,
    parseLibraryArtifactIdFromSearch,
    pinLibraryArtifactBookmark,
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
const mockCreateBookmark = Client4.createChannelBookmark as jest.Mock;
const mockDeleteBookmark = Client4.deleteChannelBookmark as jest.Mock;

describe('channel artifact bookmark helpers', () => {
    beforeEach(() => {
        mockGetBookmarks.mockReset();
        mockUpdateBookmark.mockReset();
        mockCreateBookmark.mockReset();
        mockDeleteBookmark.mockReset();
    });

    test('canAddChannelBookmarks is false for direct and group channels', () => {
        const state = {
            entities: {
                general: {license: {IsLicensed: 'true'}},
                users: {currentUserId: 'me', profiles: {me: {roles: 'system_user'}}},
                channels: {
                    channels: {
                        dm: {id: 'dm', type: 'D', delete_at: 0, name: 'a__b', team_id: ''},
                        gm: {id: 'gm', type: 'G', delete_at: 0, name: 'group', team_id: ''},
                    },
                    myMembers: {
                        dm: {channel_id: 'dm'},
                        gm: {channel_id: 'gm'},
                    },
                },
            },
        } as any;

        expect(canAddChannelBookmarks(state, 'dm')).toBe(false);
        expect(canAddChannelBookmarks(state, 'gm')).toBe(false);
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
        expect(findLibraryArtifactBookmark([
            {id: '5', type: 'link', link_url: '\\\\foreign.example/a?mm_ai_library_artifact=want'} as any,
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

    test('syncLibraryArtifactBookmarkTitle rejects when the lookup fails', async () => {
        mockGetBookmarks.mockRejectedValue(new Error('bookmarks unavailable'));

        await expect(syncLibraryArtifactBookmarkTitle({} as any, 'ch1', 'art1', 'Title')).rejects.toThrow('bookmarks unavailable');
    });

    test('pinLibraryArtifactBookmark keeps the earliest bookmark when a race creates two', async () => {
        const link = 'http://localhost:8065/test/channels/town?mm_ai_library_artifact=art1';
        const older = {id: 'older', type: 'link', channel_id: 'ch1', create_at: 1, display_name: 'A', link_url: link};
        const newer = {id: 'newer', type: 'link', channel_id: 'ch1', create_at: 2, display_name: 'A', link_url: link};
        mockGetBookmarks.mockResolvedValueOnce([]).mockResolvedValueOnce([newer, older]);
        mockCreateBookmark.mockResolvedValue(newer);
        mockDeleteBookmark.mockResolvedValue({});

        const state = {
            entities: {
                general: {license: {IsLicensed: 'true'}},
                users: {currentUserId: 'me', profiles: {me: {roles: 'system_user'}}},
                roles: {roles: {channel_user: {permissions: ['add_bookmark_public_channel']}}},
                teams: {teams: {team1: {name: 'test'}}, myMembers: {}},
                channels: {
                    channels: {ch1: {id: 'ch1', name: 'town', type: 'O', delete_at: 0, team_id: 'team1'}},
                    myMembers: {},
                    roles: {ch1: new Set(['channel_user'])},
                },
            },
            websocket: {connectionId: 'conn1'},
        } as any;
        const dispatch = jest.fn();

        const kept = await pinLibraryArtifactBookmark(state, 'ch1', 'art1', 'A', dispatch);

        expect(kept?.id).toBe('older');
        expect(mockDeleteBookmark).toHaveBeenCalledWith('ch1', 'newer', 'conn1');
        expect(dispatch).toHaveBeenCalledWith(expect.objectContaining({
            type: 'BOOKMARK_DELETED',
            data: expect.objectContaining({id: 'newer'}),
        }));
        expect(dispatch).toHaveBeenCalledWith(expect.objectContaining({
            type: 'RECEIVED_BOOKMARK',
            data: expect.objectContaining({id: 'older'}),
        }));
    });
});
