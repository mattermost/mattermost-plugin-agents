// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {setChannelLibraryClient, type ChannelLibraryClient} from '../channel_artifacts/library_client';

import {downloadArtifact} from './download';

jest.mock('@/client', () => ({
    channelArtifactDownloadURL: (id: string) => `/plugin/channel-artifacts/${id}/download`,
    fileDownloadURL: (id: string) => `/api/v4/files/${id}?download=1`,
}));

describe('downloadArtifact', () => {
    let clicked: string[];
    let click: jest.SpyInstance;

    beforeEach(() => {
        clicked = [];
        click = jest.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function click(this: HTMLAnchorElement) {
            clicked.push(this.getAttribute('href') ?? '');
        });
    });

    afterEach(() => {
        click.mockRestore();
    });

    test('a library artifact is downloaded from the installed library client', () => {
        const replacement = {downloadURL: jest.fn((id: string, version?: number) => `/other/${id}?v=${version}`)} as unknown as ChannelLibraryClient;
        const previous = setChannelLibraryClient(replacement);
        try {
            downloadArtifact('artifactid012345678901234', 'library', 3);
        } finally {
            setChannelLibraryClient(previous);
        }
        expect(clicked).toEqual(['/other/artifactid012345678901234?v=3']);
    });

    test('a library artifact defaults to the plugin routes', () => {
        downloadArtifact('artifactid012345678901234', 'library');
        expect(clicked).toEqual(['/plugin/channel-artifacts/artifactid012345678901234/download']);
    });

    test('a post file is downloaded through the files API', () => {
        downloadArtifact('fileid01234567890123456789');
        expect(clicked).toEqual(['/api/v4/files/fileid01234567890123456789?download=1']);
    });
});
