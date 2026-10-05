// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, within} from '@testing-library/react';

import {AgentDocument, ServiceInfo, UserAgent} from '@/types/agents';
import {downloadAgentDocument} from '@/utils/download_agent_document';

import VersionSnapshot from './version_snapshot';

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
    agentDocumentUrl: (agentId: string, documentId: string) => `/plugins/mattermost-ai/agents/${agentId}/documents/${documentId}`,
}));

jest.mock('@/utils/download_agent_document', () => ({
    downloadAgentDocument: jest.fn(),
}));

jest.mock('@/components/system_console/bot', () => ({
    ChannelAccessLevel: {All: 0, Allow: 1, Block: 2, None: 3},
    UserAccessLevel: {All: 0, Allow: 1, Block: 2, None: 3, AttributeBased: 4},
}));

const mockDownload = downloadAgentDocument as jest.MockedFunction<typeof downloadAgentDocument>;

const services: ServiceInfo[] = [];

const handbook: AgentDocument = {id: 'doc_1', name: 'handbook.pdf', mimeType: 'application/pdf', size: 2 * 1024 * 1024, sha256: 'a', textRunes: 100};
const faq: AgentDocument = {id: 'doc_2', name: 'faq.txt', mimeType: 'text/plain', size: 300, sha256: 'b', textRunes: 10};

function renderSnapshot(config: Partial<UserAgent>, agentId?: string) {
    render(
        <VersionSnapshot
            config={config}
            services={services}
            agentId={agentId}
        />,
    );
}

function documentsRow(): HTMLElement {
    const label = screen.getByText('Reference documents');
    return label.nextElementSibling as HTMLElement;
}

beforeEach(() => {
    jest.clearAllMocks();
});

describe('VersionSnapshot reference documents', () => {
    test('lists each document with its size and a download link to the document route', () => {
        renderSnapshot({documents: [handbook, faq]}, 'agent_1');

        const links = within(documentsRow()).getAllByRole('link');
        expect(links.map((l) => l.textContent)).toEqual(['handbook.pdf', 'faq.txt']);
        expect(links.map((l) => l.getAttribute('href'))).toEqual([
            '/plugins/mattermost-ai/agents/agent_1/documents/doc_1',
            '/plugins/mattermost-ai/agents/agent_1/documents/doc_2',
        ]);
        expect(within(documentsRow()).getByText('2 MB')).not.toBeNull();
        expect(within(documentsRow()).getByText('300 B')).not.toBeNull();
    });

    test('clicking a link downloads through the authenticated helper instead of navigating', () => {
        mockDownload.mockResolvedValue();
        renderSnapshot({documents: [handbook]}, 'agent_1');

        const notPrevented = fireEvent.click(screen.getByRole('link', {name: 'handbook.pdf'}));

        expect(notPrevented).toBe(false);
        expect(mockDownload).toHaveBeenCalledWith('agent_1', 'doc_1', 'handbook.pdf');
    });

    test('shows the server error when a download fails', async () => {
        mockDownload.mockRejectedValue({message: 'document not found'});
        renderSnapshot({documents: [handbook]}, 'agent_1');

        fireEvent.click(screen.getByRole('link', {name: 'handbook.pdf'}));

        expect(await screen.findByText('document not found')).not.toBeNull();
    });

    test.each([
        {name: 'documents is missing (older snapshot)', config: {}},
        {name: 'documents is null', config: {documents: null}},
        {name: 'documents is empty', config: {documents: []}},
    ])('shows None when $name', ({config}) => {
        renderSnapshot(config, 'agent_1');

        expect(documentsRow().textContent).toBe('None');
        expect(screen.queryByRole('link')).toBeNull();
    });

    test('lists names without links when the agent is unknown', () => {
        renderSnapshot({documents: [faq]});

        expect(within(documentsRow()).getByText(/faq\.txt/)).not.toBeNull();
        expect(screen.queryByRole('link')).toBeNull();
    });
});
