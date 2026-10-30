// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useSyncExternalStore} from 'react';

import {getUserToolPreferences} from '@/client';

export type ToolPreferencesStatus = 'loading' | 'ready' | 'error';

export interface ComposerToolPreferences {
    status: ToolPreferencesStatus;
    disabledServers: string[];
    setDisabledServers: (servers: string[]) => void;
    reload: () => void;
}

// The user's disabled tool providers, shared by every control that shows them: the RHS title's
// tools popover and the Agents chat header's. One copy, so a toggle in one shows in the other, and
// one load at a time however many are mounted.
interface Snapshot {
    status: ToolPreferencesStatus;
    disabledServers: string[];
}

const initial: Snapshot = {status: 'loading', disabledServers: []};
let snapshot: Snapshot = initial;

// Bumped by every load and every local change: a response only lands if nothing came after it.
let generation = 0;
let inFlight: {generation: number; promise: Promise<void>} | null = null;

const listeners = new Set<() => void>();
const set = (next: Snapshot) => {
    snapshot = next;
    Array.from(listeners).forEach((listener) => listener());
};

const subscribe = (listener: () => void) => {
    listeners.add(listener);
    return () => {
        listeners.delete(listener);
    };
};
const getSnapshot = () => snapshot;

// A toggle here, or one published by another control, supersedes a load still in flight, so its
// older response cannot undo the change.
const setDisabledServers = (servers: string[]) => {
    generation++;
    inFlight = null;
    set({status: 'ready', disabledServers: servers});
};

// Loads the preferences, joining a load already under way. Loaded preferences stay shown while
// they reload (a failed reload keeps them); before the first load succeeds, a failure is kept as
// an error, since the toggles would otherwise show every provider as enabled.
const load = (): Promise<void> => {
    if (inFlight && inFlight.generation === generation) {
        return inFlight.promise;
    }
    const id = ++generation;
    if (snapshot.status !== 'ready') {
        set({...snapshot, status: 'loading'});
    }
    const promise = (async () => {
        try {
            const prefs = await getUserToolPreferences();
            if (id === generation) {
                set({status: 'ready', disabledServers: prefs.disabled_servers || []});
            }
        } catch (e) {
            console.error('Failed to load tool preferences:', e); // eslint-disable-line no-console
            if (id === generation && snapshot.status !== 'ready') {
                set({...snapshot, status: 'error'});
            }
        } finally {
            if (inFlight?.generation === id) {
                inFlight = null;
            }
        }
    })();
    inFlight = {generation: id, promise};
    return promise;
};

// Tests only: forget the loaded preferences.
export const resetToolPreferences = () => {
    generation++;
    inFlight = null;
    snapshot = initial;
};

// The shared preferences, (re)loaded when a control mounts and whenever its agent changes (the
// agent decides which providers the control shows).
export function useComposerToolPreferences(agentId = ''): ComposerToolPreferences {
    const current = useSyncExternalStore(subscribe, getSnapshot);

    useEffect(() => {
        load();
    }, [agentId]);

    const reload = useCallback(() => {
        load();
    }, []);

    return {status: current.status, disabledServers: current.disabledServers, setDisabledServers, reload};
}
