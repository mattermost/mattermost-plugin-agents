// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {exportAgent} from '@/client';

/**
 * Fetches GET /agents/:id/export with the client's auth helpers and hands the
 * result to the browser as a file download. The filename comes from the
 * server's Content-Disposition header, falling back to `<username>.agent.json`.
 */
export async function downloadAgentExport(agentId: string, username: string): Promise<void> {
    const {blob, filename} = await exportAgent(agentId);
    const url = URL.createObjectURL(blob);
    try {
        const link = document.createElement('a');
        link.href = url;
        link.download = filename || `${username}.agent.json`;
        link.hidden = true;
        document.body.appendChild(link);
        link.click();
        document.body.removeChild(link);
    } finally {
        // Defer revocation so the browser has started the download.
        window.setTimeout(() => URL.revokeObjectURL(url), 0);
    }
}
