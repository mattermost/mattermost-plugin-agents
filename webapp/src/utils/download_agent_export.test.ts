// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {exportAgent} from '@/client';

import {downloadAgentExport} from './download_agent_export';

jest.mock('@/client', () => ({
    exportAgent: jest.fn(),
}));

const mockExportAgent = exportAgent as jest.MockedFunction<typeof exportAgent>;

describe('downloadAgentExport', () => {
    let createObjectURL: jest.Mock;
    let revokeObjectURL: jest.Mock;
    let clickedDownloads: Array<{download: string; href: string}>;

    beforeEach(() => {
        jest.useFakeTimers();
        createObjectURL = jest.fn(() => 'blob:agent-export');
        revokeObjectURL = jest.fn();
        Object.assign(URL, {createObjectURL, revokeObjectURL});

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
        {name: 'uses the server-provided filename', filename: 'release-helper-v7.agent.json', expected: 'release-helper-v7.agent.json'},
        {name: 'falls back to <username>.agent.json without Content-Disposition', filename: null, expected: 'release-helper.agent.json'},
    ])('$name', async ({filename, expected}) => {
        const blob = new Blob(['{}'], {type: 'application/json'});
        mockExportAgent.mockResolvedValue({blob, filename});

        await downloadAgentExport('agent_1', 'release-helper');

        expect(mockExportAgent).toHaveBeenCalledWith('agent_1');
        expect(createObjectURL).toHaveBeenCalledWith(blob);
        expect(clickedDownloads).toEqual([{download: expected, href: 'blob:agent-export'}]);
        expect(document.querySelector('a[download]')).toBeNull();

        jest.runAllTimers();
        expect(revokeObjectURL).toHaveBeenCalledWith('blob:agent-export');
    });

    test('does not start a download when the export request fails', async () => {
        mockExportAgent.mockRejectedValue(new Error('forbidden'));

        await expect(downloadAgentExport('agent_1', 'release-helper')).rejects.toThrow('forbidden');

        expect(createObjectURL).not.toHaveBeenCalled();
        expect(clickedDownloads).toHaveLength(0);
    });
});
