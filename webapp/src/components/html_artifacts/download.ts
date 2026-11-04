// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {channelArtifactDownloadURL, fileDownloadURL, type ArtifactKind} from '@/client';

// Downloads the artifact's original file through the core files API, or the
// library download route for a saved channel artifact.
export function downloadArtifact(fileId: string, kind: ArtifactKind = 'file', version?: number) {
    const link = document.createElement('a');
    link.href = kind === 'library' ? channelArtifactDownloadURL(fileId, version) : fileDownloadURL(fileId);
    link.rel = 'noopener noreferrer';
    link.download = '';
    document.body.appendChild(link);
    link.click();
    link.remove();
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
