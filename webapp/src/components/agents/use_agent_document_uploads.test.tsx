// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook, waitFor} from '@testing-library/react';

import {uploadAgentDocument} from '@/client';
import {AgentDocument, MaxAgentDocumentBytes, MaxAgentDocuments} from '@/types/agents';

import {useAgentDocumentUploads} from './use_agent_document_uploads';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');

    // Real ICU formatting with ids derived from defaultMessage, as the babel plugin does at build time.
    const real = actual.createIntl({locale: 'en', defaultLocale: 'en', onError: () => null});
    const intl = {
        ...real,
        formatMessage: (descriptor: {id?: string; defaultMessage: string}, values?: Record<string, string | number>) =>
            real.formatMessage({id: descriptor.id ?? descriptor.defaultMessage, ...descriptor}, values),
    };
    return {...actual, useIntl: () => intl};
});

jest.mock('@/client', () => ({
    uploadAgentDocument: jest.fn(),
}));

const mockUpload = uploadAgentDocument as jest.MockedFunction<typeof uploadAgentDocument>;

function makeDocument(id: string, name: string): AgentDocument {
    return {id, name, mimeType: 'text/plain', size: 10, sha256: id, textRunes: 10};
}

function sizedFile(name: string, size: number): File {
    const file = new File(['x'], name);
    Object.defineProperty(file, 'size', {value: size});
    return file;
}

function renderUploads(documents: AgentDocument[] = []) {
    const onUploaded = jest.fn();
    const hook = renderHook(
        ({docs}: {docs: AgentDocument[]}) => useAgentDocumentUploads(docs, onUploaded),
        {initialProps: {docs: documents}},
    );
    return {...hook, onUploaded};
}

beforeEach(() => {
    jest.clearAllMocks();
});

describe('useAgentDocumentUploads', () => {
    test('uploads files in the order chosen and reports each document', async () => {
        mockUpload.mockImplementation(async (file) => makeDocument(`id-${file.name}`, file.name));
        const {result, onUploaded} = renderUploads();

        act(() => {
            result.current.upload([new File(['a'], 'a.txt'), new File(['b'], 'b.txt')]);
        });
        expect(result.current.uploading).toBe(true);
        expect(result.current.uploads.map((u) => u.status)).toEqual(['uploading', 'uploading']);

        await waitFor(() => expect(onUploaded).toHaveBeenCalledTimes(2));
        expect(onUploaded.mock.calls.map(([doc]) => doc.name)).toEqual(['a.txt', 'b.txt']);
        expect(result.current.uploads).toEqual([]);
        expect(result.current.uploading).toBe(false);
    });

    test.each([
        {
            name: 'an unsupported extension',
            file: () => new File(['x'], 'photo.png'),
            message: 'Unsupported file type. Supported types: .pdf, .txt, .md, .markdown, .csv, .json.',
        },
        {
            name: 'a file over 10 MiB',
            file: () => sizedFile('huge.pdf', MaxAgentDocumentBytes + 1),
            message: 'This file is too large. Files can be up to 10 MB.',
        },
        {
            name: 'an empty file',
            file: () => sizedFile('empty.txt', 0),
            message: 'This file is empty.',
        },
    ])('rejects $name before calling the server', ({file, message}) => {
        const {result, onUploaded} = renderUploads();

        act(() => {
            result.current.upload([file()]);
        });

        expect(mockUpload).not.toHaveBeenCalled();
        expect(onUploaded).not.toHaveBeenCalled();
        expect(result.current.uploads).toHaveLength(1);
        expect(result.current.uploads[0]).toMatchObject({status: 'error', error: message});
        expect(result.current.uploading).toBe(false);
    });

    test('accepts a file of exactly 10 MiB', async () => {
        mockUpload.mockResolvedValue(makeDocument('id1', 'edge.pdf'));
        const {result, onUploaded} = renderUploads();

        act(() => {
            result.current.upload([sizedFile('edge.pdf', MaxAgentDocumentBytes)]);
        });

        await waitFor(() => expect(onUploaded).toHaveBeenCalledTimes(1));
    });

    test('rejects files beyond the document count limit, counting existing documents', async () => {
        mockUpload.mockImplementation(async (file) => makeDocument(`id-${file.name}`, file.name));
        const existing = Array.from({length: MaxAgentDocuments - 1}, (_, i) => makeDocument(`e${i}`, `e${i}.txt`));
        const {result, onUploaded} = renderUploads(existing);

        act(() => {
            result.current.upload([new File(['a'], 'a.txt'), new File(['b'], 'b.txt')]);
        });

        await waitFor(() => expect(onUploaded).toHaveBeenCalledTimes(1));
        expect(mockUpload).toHaveBeenCalledTimes(1);
        expect(result.current.uploads).toEqual([
            expect.objectContaining({name: 'b.txt', status: 'error', error: 'An agent can have at most 20 documents.'}),
        ]);
    });

    test('keeps a failed upload with the server error until dismissed', async () => {
        mockUpload.mockRejectedValue({message: 'unsupported file type'});
        const {result, onUploaded} = renderUploads();

        act(() => {
            result.current.upload([new File(['x'], 'bad.pdf')]);
        });

        await waitFor(() => expect(result.current.uploads[0]?.status).toBe('error'));
        expect(result.current.uploads[0].error).toBe('unsupported file type');
        expect(onUploaded).not.toHaveBeenCalled();

        const key = result.current.uploads[0].key;
        act(() => result.current.dismiss(key));
        expect(result.current.uploads).toEqual([]);
    });

    test('falls back to a generic message when the server gives none', async () => {
        mockUpload.mockRejectedValue(new Error(''));
        const {result} = renderUploads();

        act(() => {
            result.current.upload([new File(['x'], 'bad.pdf')]);
        });

        await waitFor(() => expect(result.current.uploads[0]?.error).toBe('Failed to upload this file. Please try again.'));
    });

    test('does not attach the same document twice when the server returns an existing id', async () => {
        const existing = makeDocument('same', 'handbook.txt');
        mockUpload.mockResolvedValue(existing);
        const {result, onUploaded} = renderUploads([existing]);

        act(() => {
            result.current.upload([new File(['x'], 'handbook-copy.txt')]);
        });

        await waitFor(() => expect(result.current.uploads[0]?.status).toBe('error'));
        expect(result.current.uploads[0].error).toBe('This file is already attached to the agent.');
        expect(onUploaded).not.toHaveBeenCalled();
    });
});
