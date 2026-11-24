// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor, within} from '@testing-library/react';

import {AgentDocument} from '@/types/agents';
import {downloadAgentDocument} from '@/utils/download_agent_document';

import AgentDocumentsSection from './agent_documents_section';
import type {DocumentUploadItem} from './use_agent_document_uploads';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');

    // Real ICU formatting with ids derived from defaultMessage, as the babel plugin does at build time.
    const real = actual.createIntl({locale: 'en', defaultLocale: 'en', onError: () => null});
    const intl = {
        ...real,
        formatMessage: (descriptor: {id?: string; defaultMessage: string}, values?: Record<string, string | number>) =>
            real.formatMessage({id: descriptor.id ?? descriptor.defaultMessage, ...descriptor}, values),
    };
    return {
        ...actual,
        useIntl: () => intl,
        FormattedMessage: ({defaultMessage, values}: {defaultMessage: string; values?: Record<string, string | number>}) =>
            real.formatMessage({id: defaultMessage, defaultMessage}, values),
    };
});

jest.mock('@/utils/download_agent_document', () => ({
    downloadAgentDocument: jest.fn(),
}));

const mockDownload = downloadAgentDocument as jest.MockedFunction<typeof downloadAgentDocument>;

const handbook: AgentDocument = {id: 'doc_1', name: 'handbook.pdf', mimeType: 'application/pdf', size: 2.5 * 1024 * 1024, sha256: 'a', textRunes: 12345};
const faq: AgentDocument = {id: 'doc_2', name: 'faq.txt', mimeType: 'text/plain', size: 512, sha256: 'b', textRunes: 1};

type Overrides = Partial<React.ComponentProps<typeof AgentDocumentsSection>>;

function renderSection(overrides: Overrides = {}) {
    const props = {
        documents: [handbook, faq],
        uploads: [] as DocumentUploadItem[],
        agentId: 'agent_1',
        savedDocumentIds: ['doc_1', 'doc_2'],
        onChange: jest.fn(),
        onUpload: jest.fn(),
        onDismissUpload: jest.fn(),
        ...overrides,
    };
    render(<AgentDocumentsSection {...props}/>);
    return props;
}

beforeEach(() => {
    jest.clearAllMocks();
});

describe('AgentDocumentsSection', () => {
    test('lists each document with its type, size, and extracted characters', () => {
        renderSection();

        const rows = within(screen.getByRole('list', {name: 'Reference documents'})).getAllByRole('listitem');
        expect(rows).toHaveLength(2);
        expect(within(rows[0]).getByText('handbook.pdf')).not.toBeNull();
        expect(within(rows[0]).getByText('PDF · 2.5 MB · 12,345 characters extracted')).not.toBeNull();
        expect(within(rows[1]).getByText('Text · 512 B · 1 character extracted')).not.toBeNull();
    });

    test('shows the usage line and explains that changes apply on save', () => {
        renderSection();

        expect(screen.getByTestId('agent-documents-usage').textContent).toBe('12,346 of 100,000 characters of extracted text used');
        expect(screen.getByText(/added to the agent's context on every request/)).not.toBeNull();
        expect(screen.getByText(/Changes take effect when you save and create a new version/)).not.toBeNull();
    });

    test('flags the usage line when over the extracted text budget', () => {
        const over = {...handbook, textRunes: 100001};
        renderSection({documents: [over]});

        expect(screen.getByTestId('agent-documents-usage').textContent).toBe('100,001 of 100,000 characters of extracted text used');
        expect(getComputedStyle(screen.getByTestId('agent-documents-usage')).fontWeight).toBe('600');
    });

    test('shows a validation error passed by the editor', () => {
        renderSection({error: 'Reference documents contain too much text.'});

        expect(screen.getByRole('alert').textContent).toBe('Reference documents contain too much text.');
    });

    test('Remove drops only that document', () => {
        const {onChange} = renderSection();

        fireEvent.click(screen.getByRole('button', {name: 'Remove handbook.pdf'}));

        expect(onChange).toHaveBeenCalledWith([faq]);
    });

    test('Rename edits the name inline and keeps the rest of the document', () => {
        const {onChange} = renderSection();

        fireEvent.click(screen.getByRole('button', {name: 'Rename faq.txt'}));
        const input = screen.getByLabelText('Document name');
        fireEvent.change(input, {target: {value: '  Frequently asked.txt '}});
        fireEvent.keyDown(input, {key: 'Enter'});

        expect(onChange).toHaveBeenCalledWith([handbook, {...faq, name: 'Frequently asked.txt'}]);
        expect(screen.queryByLabelText('Document name')).toBeNull();
    });

    test('Escape cancels a rename without changing anything', () => {
        const {onChange} = renderSection();

        fireEvent.click(screen.getByRole('button', {name: 'Rename faq.txt'}));
        fireEvent.change(screen.getByLabelText('Document name'), {target: {value: 'other.txt'}});
        fireEvent.keyDown(screen.getByLabelText('Document name'), {key: 'Escape'});

        expect(onChange).not.toHaveBeenCalled();
        expect(screen.queryByLabelText('Document name')).toBeNull();
        expect(screen.getByText('faq.txt')).not.toBeNull();
    });

    test.each([
        {name: 'blank', value: '   ', error: 'A name is required.'},
        {name: 'with a slash', value: 'a/b.txt', error: 'Names cannot contain slashes.'},
        {name: 'with a backslash', value: 'a\\b.txt', error: 'Names cannot contain slashes.'},
        {name: 'too long', value: 'x'.repeat(257), error: 'Names must be 256 characters or fewer.'},
    ])('rejects a rename that is $name', ({value, error}) => {
        const {onChange} = renderSection();

        fireEvent.click(screen.getByRole('button', {name: 'Rename faq.txt'}));
        fireEvent.change(screen.getByLabelText('Document name'), {target: {value}});
        fireEvent.click(screen.getByRole('button', {name: 'Done'}));

        expect(screen.getByText(error)).not.toBeNull();
        expect(onChange).not.toHaveBeenCalled();
        expect(screen.getByLabelText('Document name')).not.toBeNull();
    });

    test('Download is offered only for saved documents of an existing agent', () => {
        const onlyHandbookSaved = renderSection({savedDocumentIds: ['doc_1']});

        expect(screen.getByRole('button', {name: 'Download handbook.pdf'})).not.toBeNull();
        expect(screen.queryByRole('button', {name: 'Download faq.txt'})).toBeNull();
        expect(onlyHandbookSaved.onChange).not.toHaveBeenCalled();
    });

    test('Download is not offered when creating an agent', () => {
        renderSection({agentId: '', savedDocumentIds: []});

        expect(screen.queryByRole('button', {name: /^Download/})).toBeNull();
    });

    test('Download fetches the document through the helper and surfaces failures', async () => {
        mockDownload.mockRejectedValueOnce({message: 'document not found'});
        renderSection();

        fireEvent.click(screen.getByRole('button', {name: 'Download handbook.pdf'}));

        expect(mockDownload).toHaveBeenCalledWith('agent_1', 'doc_1', 'handbook.pdf');
        expect(await screen.findByText('document not found')).not.toBeNull();
    });

    test('choosing files hands every selected file to the uploader', () => {
        const {onUpload} = renderSection();
        const input = screen.getByTestId('agent-documents-input') as HTMLInputElement;
        const files = [new File(['a'], 'a.txt'), new File(['b'], 'b.md')];

        expect(input.multiple).toBe(true);
        expect(input.accept).toBe('.pdf,.txt,.md,.markdown,.csv,.json');
        fireEvent.change(input, {target: {files}});

        expect(onUpload).toHaveBeenCalledWith(files);
    });

    test('shows per-file uploading state and server errors, and lets errors be dismissed', async () => {
        const uploads: DocumentUploadItem[] = [
            {key: 'u1', name: 'big.pdf', status: 'uploading'},
            {key: 'u2', name: 'scan.pdf', status: 'error', error: 'no extractable text found in "scan.pdf"'},
        ];
        const {onDismissUpload} = renderSection({uploads});

        const list = screen.getByRole('list', {name: 'Document uploads'});
        expect(within(list).getByText('Uploading...')).not.toBeNull();
        expect(within(list).getByText('no extractable text found in "scan.pdf"')).not.toBeNull();
        expect(within(list).queryByRole('button', {name: 'Dismiss error for big.pdf'})).toBeNull();

        fireEvent.click(within(list).getByRole('button', {name: 'Dismiss error for scan.pdf'}));
        await waitFor(() => expect(onDismissUpload).toHaveBeenCalledWith('u2'));
    });
});
