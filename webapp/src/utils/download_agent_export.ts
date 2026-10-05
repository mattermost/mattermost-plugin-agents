// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {exportAgent} from '@/client';

import {saveBlobAsFile} from './download_blob';

/**
 * Fetches GET /agents/:id/export with the client's auth helpers and hands the
 * result to the browser as a file download. The filename comes from the
 * server's Content-Disposition header, falling back to `<username>.agent.json`.
 */
export async function downloadAgentExport(agentId: string, username: string): Promise<void> {
    const {blob, filename} = await exportAgent(agentId);
    saveBlobAsFile(blob, filename || `${username}.agent.json`);
}
