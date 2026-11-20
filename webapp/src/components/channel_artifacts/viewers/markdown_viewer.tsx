// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';

import HTMLArtifactFullscreen from '../../html_artifacts/html_artifact_fullscreen';

import InlineViewerShell from './inline_viewer_shell';
import MarkdownArtifactBody from './markdown_artifact_body';
import type {ArtifactViewerProps} from './registry';

const MarkdownArtifactViewer = ({entry, presentation, canEdit, canDelete, startedByClick, onClose, onPopOut}: ArtifactViewerProps) => {
    if (presentation === 'fullscreen') {
        return (
            <HTMLArtifactFullscreen
                fileId={entry.id}
                fileName={entry.title}
                artifactKind='library'
                renderDocument={(version, revision) => (
                    <MarkdownArtifactBody
                        entry={entry}
                        version={version}
                        revision={`${entry.sha256}:${revision}`}
                    />
                )}
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
            <MarkdownArtifactBody
                entry={entry}
                version={entry.version}
                revision={entry.sha256}
            />
        </InlineViewerShell>
    );
};

export default MarkdownArtifactViewer;
