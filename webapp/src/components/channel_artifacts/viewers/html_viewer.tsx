// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';

import ArtifactFrame from '../../html_artifacts/artifact_frame';
import HTMLArtifactFullscreen from '../../html_artifacts/html_artifact_fullscreen';

import InlineViewerShell from './inline_viewer_shell';
import type {ArtifactViewerProps} from './registry';

const HTMLArtifactViewer = ({entry, presentation, canEdit, canDelete, startedByClick, onClose, onPopOut}: ArtifactViewerProps) => {
    if (presentation === 'fullscreen') {
        return (
            <HTMLArtifactFullscreen
                fileId={entry.id}
                fileName={entry.title}
                artifactKind='library'
                startedByClick={startedByClick}
                canEdit={canEdit}
                canDelete={canDelete}
                headVersion={entry.version}
                onClose={onClose}
            />
        );
    }

    return (
        <InlineViewerShell
            entry={entry}
            onClose={onClose}
            onPopOut={onPopOut}
        >
            <ArtifactFrame
                key={`${entry.id}:${entry.version}`}
                fileId={entry.id}
                fileName={entry.title}
                artifactKind='library'
                version={entry.version}
                displayMode='inline'
                startedByClick={startedByClick}
                onEscape={onClose}
            />
        </InlineViewerShell>
    );
};

export default HTMLArtifactViewer;
