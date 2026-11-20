// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fetchAgentDocument} from '@/client';

import {saveBlobAsFile} from './download_blob';

/**
 * Fetches GET /agents/:id/documents/:documentid with the client's auth helpers and
 * hands the original bytes to the browser as a file download.
 */
export async function downloadAgentDocument(agentId: string, documentId: string, filename: string): Promise<void> {
    saveBlobAsFile(await fetchAgentDocument(agentId, documentId), filename);
}
