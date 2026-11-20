// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {getArtifactViewer, registerArtifactViewer, type ArtifactViewerComponent} from './registry';

const viewerA = (() => null) as ArtifactViewerComponent;
const viewerB = (() => null) as ArtifactViewerComponent;

describe('artifact viewer registry', () => {
    test('a format has no viewer until one is registered', () => {
        expect(getArtifactViewer('markdown')).toBeUndefined();
        const unregister = registerArtifactViewer('markdown', viewerA);
        expect(getArtifactViewer('markdown')).toBe(viewerA);
        unregister();
        expect(getArtifactViewer('markdown')).toBeUndefined();
    });

    test('a replacement can be undone, putting the first viewer back', () => {
        const unregisterA = registerArtifactViewer('html', viewerA);
        const unregisterB = registerArtifactViewer('html', viewerB);
        expect(getArtifactViewer('html')).toBe(viewerB);
        unregisterB();
        expect(getArtifactViewer('html')).toBe(viewerA);
        unregisterA();
        expect(getArtifactViewer('html')).toBeUndefined();
    });

    test('withdrawing a replaced registration leaves the current viewer alone, and a withdrawn one is not revived', () => {
        const unregisterA = registerArtifactViewer('html', viewerA);
        const unregisterB = registerArtifactViewer('html', viewerB);
        unregisterA();
        expect(getArtifactViewer('html')).toBe(viewerB);
        unregisterB();
        expect(getArtifactViewer('html')).toBeUndefined();
    });
});
