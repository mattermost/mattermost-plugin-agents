// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect} from 'react';
import {useDispatch, useSelector} from 'react-redux';

import {fetchAndStoreBots} from '@/bots';
import {getScopedSessionsSupported} from '@/selectors';

// useServerSupportsScopedSessions reports whether this Mattermost server can
// create the scoped sessions scoped channel mode runs on, as the plugin found
// when it started. It is null until the plugin has said.
export function useServerSupportsScopedSessions(): boolean | null {
    const supported = useSelector(getScopedSessionsSupported);
    const dispatch = useDispatch();

    useEffect(() => {
        if (supported === null) {
            fetchAndStoreBots(dispatch).catch(() => { /* best effort */ });
        }
    }, [supported, dispatch]);

    return supported;
}
