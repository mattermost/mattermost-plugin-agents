// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {ComponentType} from 'react';

import type {ChannelArtifactEntry, ChannelArtifactFormat} from '@/client';

export type ArtifactPresentation = 'inline' | 'fullscreen';

export type ArtifactViewerProps = {
    entry: ChannelArtifactEntry;

    // 'fullscreen' covers the window; 'inline' fills the container that hosts
    // it, such as a sidebar tab, and never leaves it.
    presentation: ArtifactPresentation;
    canEdit: boolean;
    canDelete: boolean;

    // A trusted click of the viewer opened it: see ArtifactFrame.
    startedByClick: boolean;

    // The viewer asks to be closed: Escape or Close in fullscreen, Back inline.
    onClose: () => void;

    // Inline only: the viewer asks to switch to fullscreen. Without it the
    // viewer offers no such button.
    onPopOut?: () => void;
};

export type ArtifactViewerComponent = ComponentType<ArtifactViewerProps>;

type Registration = {viewer: ArtifactViewerComponent};

const registrations = new Map<ChannelArtifactFormat, Registration[]>();

/**
 * Makes viewer the viewer for a format, in place of the one it had. The
 * returned function withdraws this registration; the newest one still standing
 * serves the format.
 */
export function registerArtifactViewer(format: ChannelArtifactFormat, viewer: ArtifactViewerComponent): () => void {
    const registration = {viewer};
    registrations.set(format, [...(registrations.get(format) ?? []), registration]);
    return () => {
        registrations.set(format, (registrations.get(format) ?? []).filter((r) => r !== registration));
    };
}

export function getArtifactViewer(format: ChannelArtifactFormat): ArtifactViewerComponent | undefined {
    return registrations.get(format)?.at(-1)?.viewer;
}
