// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fileDownloadURL, type ArtifactKind} from '@/client';

import {getChannelLibraryClient} from '../channel_artifacts/library_client';

// Downloads the artifact's original file through the core files API, or the
// library download route for a saved channel artifact.
export function downloadArtifact(fileId: string, kind: ArtifactKind = 'file', version?: number) {
    const link = document.createElement('a');
    link.href = kind === 'library' ? getChannelLibraryClient().downloadURL(fileId, version) : fileDownloadURL(fileId);
    link.rel = 'noopener noreferrer';
    link.download = '';
    document.body.appendChild(link);
    link.click();
    link.remove();
}

// Long enough for a Save As picker left open while the viewer chooses a folder.
export const REVOKE_DOWNLOAD_URL_MS = 10 * 60 * 1000;

// Saves blob as filename from the host page. Call it while the viewer's
// activation (e.g. a click) is still current, or the browser may block it.
export function saveBlob(blob: Blob, filename: string) {
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = filename;
    link.rel = 'noopener';
    document.body.appendChild(link);
    try {
        link.click();
    } finally {
        link.remove();
        window.setTimeout(() => URL.revokeObjectURL(url), REVOKE_DOWNLOAD_URL_MS);
    }
}

const ARTIFACT_EXTENSIONS = new Set(['html', 'htm']);

export function isHTMLArtifactFile(file: {extension?: string; name?: string} | null | undefined): boolean {
    if (!file) {
        return false;
    }
    let ext = file.extension;
    if (!ext && file.name && file.name.includes('.')) {
        ext = file.name.split('.').pop();
    }
    return ARTIFACT_EXTENSIONS.has((ext ?? '').replace(/^\./, '').toLowerCase());
}
