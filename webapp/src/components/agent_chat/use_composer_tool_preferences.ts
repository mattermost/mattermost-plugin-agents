// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useRef, useState} from 'react';

import {getUserToolPreferences} from '@/client';

export type ToolPreferencesStatus = 'loading' | 'ready' | 'error';

export interface ComposerToolPreferences {
    status: ToolPreferencesStatus;
    disabledServers: string[];
    setDisabledServers: (servers: string[]) => void;
    reload: () => void;
}

// The user's disabled tool providers, as the RHS and the Agents header load
// them for their tools popover. Unlike those, a failed load is kept as an
// error: the toggles would otherwise show every provider as enabled.
export function useComposerToolPreferences(): ComposerToolPreferences {
    const [status, setStatus] = useState<ToolPreferencesStatus>('loading');
    const [disabledServers, setServers] = useState<string[]>([]);
    const latest = useRef(0);

    // A toggle here or one published by another control supersedes a load
    // still in flight, so its older response cannot undo the change.
    const setDisabledServers = useCallback((servers: string[]) => {
        latest.current++;
        setServers(servers);
        setStatus('ready');
    }, []);

    const reload = useCallback(async () => {
        const id = ++latest.current;
        setStatus('loading');
        try {
            const prefs = await getUserToolPreferences();
            if (id === latest.current) {
                setServers(prefs.disabled_servers || []);
                setStatus('ready');
            }
        } catch (e) {
            console.error('Failed to load tool preferences:', e); // eslint-disable-line no-console
            if (id === latest.current) {
                setStatus('error');
            }
        }
    }, []);

    useEffect(() => {
        const requests = latest;
        reload();
        return () => {
            // Ignore a response that lands after unmount.
            requests.current++;
        };
    }, [reload]);

    return {status, disabledServers, setDisabledServers, reload};
}
