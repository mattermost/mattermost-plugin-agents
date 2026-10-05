// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {downloadAgentDocument as fetchAgentDocument} from '@/client';

import {saveBlobAsFile} from './download_blob';

/**
 * Fetches GET /agents/:id/documents/:documentid with the client's auth helpers and
 * hands the original bytes to the browser as a file download. The filename comes
 * from the server's Content-Disposition header, falling back to `fallbackName`.
 */
export async function downloadAgentDocument(agentId: string, documentId: string, fallbackName: string): Promise<void> {
    const {blob, filename} = await fetchAgentDocument(agentId, documentId);
    saveBlobAsFile(blob, filename || fallbackName);
}
