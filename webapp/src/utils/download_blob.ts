// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

/**
 * Hands a Blob to the browser as a file download using a temporary object URL.
 */
export function saveBlobAsFile(blob: Blob, filename: string): void {
    const url = URL.createObjectURL(blob);
    try {
        const link = document.createElement('a');
        link.href = url;
        link.download = filename;
        link.hidden = true;
        document.body.appendChild(link);
        link.click();
        document.body.removeChild(link);
    } finally {
        // Defer revocation so the browser has started the download.
        window.setTimeout(() => URL.revokeObjectURL(url), 0);
    }
}
