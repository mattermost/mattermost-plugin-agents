// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

// Must match scopedsession.MinServerVersion on the server, which also
// enforces it on save.
const minScopedSessionServerVersion = [12, 2, 0];

// Parsed the way the server's semver library parses it: missing minor and
// patch default to 0, and build metadata is ignored.
const versionPattern = /^v?(\d+)(?:\.(\d+))?(?:\.(\d+))?(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$/;

export function serverSupportsScopedSessions(version: string | undefined): boolean {
    const match = versionPattern.exec(version ?? '');
    if (!match) {
        return false;
    }
    const parts = match.slice(1, 4).map((part) => parseInt(part ?? '0', 10));
    for (let i = 0; i < minScopedSessionServerVersion.length; i++) {
        if (parts[i] !== minScopedSessionServerVersion[i]) {
            return parts[i] > minScopedSessionServerVersion[i];
        }
    }

    // A prerelease of the minimum version predates it.
    return !match[4];
}

// useServerSupportsScopedSessions reports whether this Mattermost server can
// mint the scoped sessions scoped channel mode runs on.
export function useServerSupportsScopedSessions(): boolean {
    return useSelector((state: GlobalState) => serverSupportsScopedSessions(state.entities.general.config.Version));
}
