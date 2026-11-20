// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage} from 'react-intl';
import styled from 'styled-components';

import type {ChannelArtifactEntry} from '@/client';

import {entryFormat} from '../library_client';

import './default_viewers';
import {getArtifactViewer, type ArtifactPresentation} from './registry';

export type ArtifactOpenOptions = {
    presentation?: ArtifactPresentation;
    canEdit?: boolean;
    canDelete?: boolean;
    startedByClick?: boolean;
    onClose: () => void;
    onPopOut?: () => void;
};

type Props = ArtifactOpenOptions & {
    entry: ChannelArtifactEntry;
};

/**
 * Shows a saved artifact in the viewer registered for its format. Fullscreen
 * is the default; an inline viewer renders inside whatever container hosts it.
 */
export const ArtifactViewer = ({
    entry,
    presentation = 'fullscreen',
    canEdit = false,
    canDelete = false,
    startedByClick = false,
    onClose,
    onPopOut,
}: Props) => {
    const Viewer = getArtifactViewer(entryFormat(entry));
    if (!Viewer) {
        return (
            <Unsupported
                role='alert'
                data-testid='artifact-viewer-unsupported'
            >
                <FormattedMessage defaultMessage='This kind of artifact cannot be shown here. Download it instead.'/>
            </Unsupported>
        );
    }
    return (
        <Viewer
            entry={entry}
            presentation={presentation}
            canEdit={canEdit}
            canDelete={canDelete}
            startedByClick={startedByClick}
            onClose={onClose}
            onPopOut={onPopOut}
        />
    );
};

/**
 * The one way surfaces show an artifact, whatever its format: render what it
 * returns. A click that opens one passes startedByClick; a link or a restored
 * state must not.
 */
export function openArtifact(entry: ChannelArtifactEntry, options: ArtifactOpenOptions): React.ReactElement {
    return (
        <ArtifactViewer
            key={entry.id}
            entry={entry}
            {...options}
        />
    );
}

const Unsupported = styled.div`
    padding: var(--spacing-xl, 24px) var(--spacing-xs, 8px);
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 14px;
    line-height: 20px;
    text-align: center;
`;
