// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {getChannelContext, saveChannelContext, uploadChannelKnowledgeFiles} from './client';

jest.mock('./manifest', () => ({id: 'mattermost-ai'}), {virtual: true});

jest.mock('@mattermost/client', () => {
    const mockGetOptions = jest.fn((options: RequestInit) => options);
    const mockUploadFile = jest.fn();
    return {
        Client4: class Client4 {
            url = 'https://mattermost.example.com';
            getOptions = mockGetOptions;
            uploadFile = mockUploadFile;
        },
        ClientError: class ClientError extends Error {
            constructor(_baseUrl: string, data: {message: string}) {
                super(data.message);
            }
        },
        mockGetOptions,
        mockUploadFile,
    };
});

const {mockUploadFile} = jest.requireMock('@mattermost/client') as {
    mockUploadFile: jest.Mock;
};

describe('channel context client', () => {
    const originalFetch = global.fetch;

    beforeEach(() => {
        jest.clearAllMocks();
        global.fetch = jest.fn();
    });

    afterAll(() => {
        global.fetch = originalFetch;
    });

    test('loads and saves channel context through the plugin API', async () => {
        const state = {
            customInstructions: 'Use the glossary.',
            files: [{id: 'file-1', name: 'guide.pdf', mimeType: 'application/pdf', size: 20}],
        };
        const fetchMock = global.fetch as jest.MockedFunction<typeof fetch>;
        fetchMock.mockResolvedValueOnce({
            ok: true,
            json: async () => state,
        } as Response);
        fetchMock.mockResolvedValueOnce({
            ok: true,
            json: async () => state,
        } as Response);

        await expect(getChannelContext('channel-1')).resolves.toEqual(state);
        await expect(saveChannelContext('channel-1', {
            customInstructions: state.customInstructions,
            fileIDs: ['file-1'],
        })).resolves.toEqual(state);

        expect(fetchMock).toHaveBeenNthCalledWith(
            1,
            'https://mattermost.example.com/plugins/mattermost-ai/channel/channel-1/context',
            expect.objectContaining({method: 'GET'}),
        );
        expect(fetchMock).toHaveBeenNthCalledWith(
            2,
            'https://mattermost.example.com/plugins/mattermost-ai/channel/channel-1/context',
            expect.objectContaining({
                method: 'PUT',
                body: JSON.stringify({
                    customInstructions: state.customInstructions,
                    fileIDs: ['file-1'],
                }),
            }),
        );
    });

    test('surfaces the server error message', async () => {
        (global.fetch as jest.MockedFunction<typeof fetch>).mockResolvedValue({
            ok: false,
            status: 400,
            json: async () => ({error: 'unsupported file'}),
        } as Response);

        await expect(getChannelContext('channel-1')).rejects.toThrow('unsupported file');
    });

    test('uploads files to the core channel file endpoint', async () => {
        mockUploadFile.mockResolvedValue({
            file_infos: [{
                id: 'file-1',
                name: 'guide.pdf',
                mime_type: 'application/pdf',
                size: 20,
            }],
        });
        const file = new File(['guide'], 'guide.pdf', {type: 'application/pdf'});

        await expect(uploadChannelKnowledgeFiles('channel-1', [file])).resolves.toEqual([{
            id: 'file-1',
            name: 'guide.pdf',
            mimeType: 'application/pdf',
            size: 20,
        }]);

        const formData = mockUploadFile.mock.calls[0][0] as FormData;
        expect(formData.get('channel_id')).toBe('channel-1');
        expect(formData.getAll('files')).toEqual([file]);
    });
});
