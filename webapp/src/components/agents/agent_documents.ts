// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {IntlShape} from 'react-intl';

import {
    AgentDocument,
    MaxAgentDocumentBytes,
    MaxAgentDocumentNameLength,
} from '@/types/agents';

// Extensions the backend accepts for reference documents.
export const AgentDocumentExtensions = ['.pdf', '.txt', '.md', '.markdown', '.csv', '.json'];
export const AgentDocumentAccept = AgentDocumentExtensions.join(',');

const KIB = 1024;
const MIB = KIB * 1024;

export function fileExtension(name: string): string {
    const dot = name.lastIndexOf('.');
    return dot < 0 ? '' : name.slice(dot).toLowerCase();
}

export function totalDocumentTextRunes(documents: Array<Pick<AgentDocument, 'textRunes'>>): number {
    return documents.reduce((sum, doc) => sum + (doc.textRunes || 0), 0);
}

export function totalDocumentBytes(documents: Array<{size: number}>): number {
    return documents.reduce((sum, doc) => sum + (doc.size || 0), 0);
}

export function formatDocumentSize(intl: IntlShape, bytes: number): string {
    const round = (value: number) => Math.round(value * 10) / 10;
    if (bytes >= MIB) {
        return intl.formatMessage({defaultMessage: '{size} MB'}, {size: intl.formatNumber(round(bytes / MIB))});
    }
    if (bytes >= KIB) {
        return intl.formatMessage({defaultMessage: '{size} KB'}, {size: intl.formatNumber(round(bytes / KIB))});
    }
    return intl.formatMessage({defaultMessage: '{size} B'}, {size: intl.formatNumber(bytes)});
}

export function documentTypeLabel(intl: IntlShape, doc: Pick<AgentDocument, 'name' | 'mimeType'>): string {
    switch (doc.mimeType) {
    case 'application/pdf':
        return intl.formatMessage({defaultMessage: 'PDF'});
    case 'text/plain':
        return intl.formatMessage({defaultMessage: 'Text'});
    case 'text/markdown':
        return intl.formatMessage({defaultMessage: 'Markdown'});
    case 'text/csv':
        return intl.formatMessage({defaultMessage: 'CSV'});
    case 'application/json':
        return intl.formatMessage({defaultMessage: 'JSON'});
    default: {
        const ext = fileExtension(doc.name).replace('.', '').toUpperCase();
        return ext || doc.mimeType;
    }
    }
}

/** Returns a user-facing reason a file cannot be uploaded, or '' when it passes the client-side pre-checks. */
export function documentFileError(intl: IntlShape, file: Pick<File, 'name' | 'size'>): string {
    if (!AgentDocumentExtensions.includes(fileExtension(file.name))) {
        return intl.formatMessage(
            {defaultMessage: 'Unsupported file type. Supported types: {types}.'},
            {types: AgentDocumentExtensions.join(', ')},
        );
    }
    if (file.size > MaxAgentDocumentBytes) {
        return intl.formatMessage(
            {defaultMessage: 'This file is too large. Files can be up to {max}.'},
            {max: formatDocumentSize(intl, MaxAgentDocumentBytes)},
        );
    }
    if (file.size === 0) {
        return intl.formatMessage({defaultMessage: 'This file is empty.'});
    }
    return '';
}

/** Returns a user-facing reason a document name is invalid, or '' when it is acceptable. */
export function documentNameError(intl: IntlShape, name: string): string {
    const trimmed = name.trim();
    if (!trimmed) {
        return intl.formatMessage({defaultMessage: 'A name is required.'});
    }
    if (Array.from(trimmed).length > MaxAgentDocumentNameLength) {
        return intl.formatMessage(
            {defaultMessage: 'Names must be {max} characters or fewer.'},
            {max: MaxAgentDocumentNameLength},
        );
    }
    if (trimmed.includes('/') || trimmed.includes('\\')) {
        return intl.formatMessage({defaultMessage: 'Names cannot contain slashes.'});
    }
    return '';
}
