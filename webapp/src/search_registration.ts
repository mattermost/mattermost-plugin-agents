// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Store, UnknownAction} from 'redux';
import {GlobalState} from '@mattermost/types/store';

import manifest from '@/manifest';

import {licenseAllows} from './license';

// Above the host default of 0, so Agents is shown first and selected by default.
// Hosts that predate search priority ignore it and keep Agents after Files.
export const AGENTS_SEARCH_PRIORITY = 100;

export const isAgentsSearchAvailable = (state: GlobalState): boolean => {
    const pluginState = (state as any)['plugins-' + manifest.id];
    return licenseAllows(state, 'semantic_search') &&
        Boolean(pluginState?.searchEnabled) &&
        (pluginState?.bots?.length ?? 0) > 0;
};

// Keeps the Agents search type registered only while it is usable, so the
// host search falls back to plain keyword search otherwise. Returns a function
// that stops syncing.
export function syncSearchRegistration(
    store: Store<GlobalState, UnknownAction>,
    register: () => string,
    unregister: (id: string) => void,
): () => void {
    let registered = false;
    let registrationId = '';

    // register/unregister dispatch to this store, which re-enters sync, so the
    // state flips before calling out.
    const sync = () => {
        const available = isAgentsSearchAvailable(store.getState());
        if (available && !registered) {
            registered = true;
            registrationId = register();
        } else if (!available && registered && registrationId) {
            registered = false;
            const id = registrationId;
            registrationId = '';
            unregister(id);
        }
    };

    sync();
    return store.subscribe(sync);
}
