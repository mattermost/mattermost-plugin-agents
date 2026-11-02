// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect, useSyncExternalStore} from 'react';

import {getUserToolPreferences, updateUserToolPreferences} from '@/client';

export type ToolPreferencesStatus = 'loading' | 'ready' | 'error';

export interface ToolPreferences {
    status: ToolPreferencesStatus;
    disabledServers: string[];

    // Enables or disables one provider, shown at once. Resolves false, with the saved list shown
    // again, when the save fails or the preferences are not loaded yet.
    toggle: (serverOrigin: string, enabled: boolean) => Promise<boolean>;

    // Loads them again (the error state's retry).
    reload: () => void;
}

// The user's disabled tool providers, shared by every control that shows them (each chat title's
// tools popover). One copy, so a toggle in one shows in the other, and one load at a time however
// many are mounted.
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

// The list the server last confirmed, which a failed save shows again.
let saved: string[] = [];

// One save at a time, each sending the latest list. A toggle made during a save rides on the
// next one; a failure fails every toggle not yet saved, so the shown list always matches the
// server's.
let saving: Promise<boolean> | null = null;
let pending: Promise<boolean> | null = null;

const saveLatest = async (): Promise<boolean> => {
    const servers = snapshot.disabledServers;
    try {
        await updateUserToolPreferences({disabled_servers: servers});
        saved = servers;
        return true;
    } catch (e) {
        console.error('Failed to save tool preferences:', e); // eslint-disable-line no-console
        pending = null;
        set({status: 'ready', disabledServers: saved});
        return false;
    }
};

const save = (): Promise<boolean> => {
    if (!saving) {
        saving = saveLatest().finally(() => {
            saving = null;
        });
        return saving;
    }
    if (!pending) {
        pending = saving.then((ok) => {
            if (!ok || !pending) {
                return false;
            }
            pending = null;
            return save();
        });
    }
    return pending;
};

// A toggle supersedes a load still in flight, so its older response cannot undo the change. The
// saved list is replaced as a whole, so a toggle before it loaded would overwrite it with just
// that one provider: refused until the preferences are ready.
const toggle = (serverOrigin: string, enabled: boolean): Promise<boolean> => {
    if (snapshot.status !== 'ready') {
        return Promise.resolve(false);
    }
    const current = snapshot.disabledServers;
    generation++;
    inFlight = null;
    set({status: 'ready', disabledServers: enabled ? current.filter((s) => s !== serverOrigin) : [...current.filter((s) => s !== serverOrigin), serverOrigin]});
    return save();
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

            // A save still in flight may postdate what the server answered; its result decides.
            if (id === generation && !saving) {
                saved = prefs.disabled_servers || [];
                set({status: 'ready', disabledServers: saved});
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
    saved = [];
    saving = null;
    pending = null;
};

// The shared preferences, (re)loaded when a control mounts and whenever its agent changes (the
// agent decides which providers the control shows).

export function useToolPreferences(agentId = ''): ToolPreferences {
    const current = useSyncExternalStore(subscribe, getSnapshot);

    useEffect(() => {
        load();
    }, [agentId]);

    return {status: current.status, disabledServers: current.disabledServers, toggle, reload: load};
}
