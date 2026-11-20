// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fetchAgentDocument} from '@/client';

import {downloadAgentDocument} from './download_agent_document';

jest.mock('@/client', () => ({
    fetchAgentDocument: jest.fn(),
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

    test('downloads the document bytes under the given name', async () => {
        mockFetchDocument.mockResolvedValue(new Blob(['pdf']));

        await downloadAgentDocument('agent_1', 'doc_1', 'handbook.pdf');

        expect(mockFetchDocument).toHaveBeenCalledWith('agent_1', 'doc_1');
        expect(clickedDownloads).toEqual([{download: 'handbook.pdf', href: 'blob:agent-document'}]);
    });

    test('does not start a download when the request fails', async () => {
        mockFetchDocument.mockRejectedValue(new Error('not found'));

        await expect(downloadAgentDocument('agent_1', 'doc_1', 'handbook.pdf')).rejects.toThrow('not found');

        expect(clickedDownloads).toHaveLength(0);
    });
});
