// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

// Must match scopedsession.MinServerVersion on the server, which also
// enforces it on save.
const minScopedSessionServerVersion = [12, 2, 0];

export function serverSupportsScopedSessions(version: string | undefined): boolean {
    const parts = (version ?? '').split('.').map((part) => parseInt(part, 10));
    if (parts.length < 3 || parts.some((part) => Number.isNaN(part))) {
        return false;
    }
    for (let i = 0; i < minScopedSessionServerVersion.length; i++) {
        if (parts[i] !== minScopedSessionServerVersion[i]) {
            return parts[i] > minScopedSessionServerVersion[i];
        }
    }
    return true;
}

// useServerSupportsScopedSessions reports whether this Mattermost server can
// mint the scoped sessions scoped channel mode runs on.
export function useServerSupportsScopedSessions(): boolean {
    return useSelector((state: GlobalState) => serverSupportsScopedSessions(state.entities.general.config.Version));
}
