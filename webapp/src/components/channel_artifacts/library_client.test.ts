// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {
    createChannelArtifact,
    getChannelArtifactContent,
    listChannelArtifactVersions,
    type ChannelArtifactEntry,
    type ChannelArtifactVersionRecord,
} from '@/client';

import {
    entryFormat,
    getChannelLibraryClient,
    pluginChannelLibraryClient,
    setChannelLibraryClient,
    type ChannelLibraryClient,
} from './library_client';

jest.mock('@/client', () => ({
    createChannelArtifact: jest.fn(),
    getChannelArtifactContent: jest.fn(),
    listChannelArtifactVersions: jest.fn(),
}));

function record(version: number): ChannelArtifactVersionRecord {
    return {
        version,
        sha256: 'abc',
        size: 1,
        source_post_id: 'p',
        source_file_id: 'f',
        source_bot_user_id: 'b',
        created_by: 'u',
        create_at: version,
        head: false,
        available: true,
    };
}

describe('pluginChannelLibraryClient', () => {
    beforeEach(() => {
        jest.mocked(createChannelArtifact).mockReset();
        jest.mocked(getChannelArtifactContent).mockReset();
        jest.mocked(listChannelArtifactVersions).mockReset();
    });

    test('lists versions newest first', async () => {
        jest.mocked(listChannelArtifactVersions).mockResolvedValue({versions: [record(1), record(3), record(2)]});
        const versions = await pluginChannelLibraryClient.versions('id');
        expect(versions.map((v) => v.version)).toEqual([3, 2, 1]);
    });

    test('treats a missing version list as empty', async () => {
        jest.mocked(listChannelArtifactVersions).mockResolvedValue({} as never);
        expect(await pluginChannelLibraryClient.versions('id')).toEqual([]);
    });

    test.each([
        [
            'a file with every option',
            {channelId: 'c', fileId: 'f', postId: 'p', title: 'T', replaceId: 'r', format: 'markdown' as const},
            {channel_id: 'c', post_id: 'p', file_id: 'f', title: 'T', replace_id: 'r', format: 'markdown'},
        ],
        [
            'a file alone, leaving the post and format to the server',
            {channelId: 'c', fileId: 'f'},
            {channel_id: 'c', post_id: '', file_id: 'f'},
        ],
    ])('writes %s', async (_name, request, body) => {
        const entry = {id: 'a'} as ChannelArtifactEntry;
        jest.mocked(createChannelArtifact).mockResolvedValue(entry);
        await expect(pluginChannelLibraryClient.write(request)).resolves.toBe(entry);
        expect(createChannelArtifact).toHaveBeenCalledWith(body);
    });

    test('reads text through the content route', async () => {
        jest.mocked(getChannelArtifactContent).mockResolvedValue('# Hi');
        await expect(pluginChannelLibraryClient.readText('id', 2)).resolves.toBe('# Hi');
        expect(getChannelArtifactContent).toHaveBeenCalledWith('id', 2);
    });
});

describe('the installed library client', () => {
    test('a replacement serves every caller until it is put back', async () => {
        const replacement = {list: jest.fn().mockResolvedValue({entries: []})} as unknown as ChannelLibraryClient;
        const previous = setChannelLibraryClient(replacement);
        try {
            expect(previous).toBe(pluginChannelLibraryClient);
            expect(getChannelLibraryClient()).toBe(replacement);
        } finally {
            setChannelLibraryClient(previous);
        }
        expect(getChannelLibraryClient()).toBe(pluginChannelLibraryClient);
    });
});

describe('entryFormat', () => {
    test.each([
        [{}, 'html'],
        [{format: 'html' as const}, 'html'],
        [{format: 'markdown' as const}, 'markdown'],
    ])('%j is %s', (entry, want) => {
        expect(entryFormat(entry)).toBe(want);
    });
});
