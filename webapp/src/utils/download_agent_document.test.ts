// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {downloadAgentDocument as fetchAgentDocument} from '@/client';

import {downloadAgentDocument} from './download_agent_document';

jest.mock('@/client', () => ({
    downloadAgentDocument: jest.fn(),
}));

const mockFetchDocument = fetchAgentDocument as jest.MockedFunction<typeof fetchAgentDocument>;

describe('downloadAgentDocument', () => {
    let clickedDownloads: Array<{download: string; href: string}>;

    beforeEach(() => {
        jest.useFakeTimers();
        Object.assign(URL, {createObjectURL: jest.fn(() => 'blob:agent-document'), revokeObjectURL: jest.fn()});

        clickedDownloads = [];
        jest.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function click(this: HTMLAnchorElement) {
            clickedDownloads.push({download: this.download, href: this.href});
        });
    });

    afterEach(() => {
        jest.useRealTimers();
        jest.restoreAllMocks();
    });

    test.each([
        {name: 'uses the server-provided filename', filename: 'Handbook v2.pdf', expected: 'Handbook v2.pdf'},
        {name: 'falls back to the document name without Content-Disposition', filename: null, expected: 'handbook.pdf'},
    ])('$name', async ({filename, expected}) => {
        mockFetchDocument.mockResolvedValue({blob: new Blob(['pdf']), filename});

        await downloadAgentDocument('agent_1', 'doc_1', 'handbook.pdf');

        expect(mockFetchDocument).toHaveBeenCalledWith('agent_1', 'doc_1');
        expect(clickedDownloads).toEqual([{download: expected, href: 'blob:agent-document'}]);
    });

    test('does not start a download when the request fails', async () => {
        mockFetchDocument.mockRejectedValue(new Error('not found'));

        await expect(downloadAgentDocument('agent_1', 'doc_1', 'handbook.pdf')).rejects.toThrow('not found');

        expect(clickedDownloads).toHaveLength(0);
    });
});
