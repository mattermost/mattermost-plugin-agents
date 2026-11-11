// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect, useState} from 'react';

import {getMySettingsAccess, type MySettingsAccess, type SettingsAccessLevel} from '@/client';

export type SettingsAccessState = {
    level: SettingsAccessLevel;

    // Whether the user may edit and delete every agent: the manage_others_agent
    // permission, or the agent managers list in the System Console.
    manageOthersAgents: boolean;

    // True until the first answer; nothing is granted meanwhile.
    loading: boolean;

    // True when the lookup failed. Nothing is granted then, but nothing is
    // known about the user's access either.
    error: boolean;
};

// The sidebar, the agents list and the agent editor all ask at once; they
// share one request while it is in flight. A later mount asks again, so a
// change to the lists shows without a reload.
let inFlight: Promise<MySettingsAccess> | null = null;

const fetchMySettingsAccess = (): Promise<MySettingsAccess> => {
    if (!inFlight) {
        const request = Promise.resolve().then(() => getMySettingsAccess());
        inFlight = request;
        const clear = () => {
            if (inFlight === request) {
                inFlight = null;
            }
        };
        request.then(clear, clear);
    }
    return inFlight;
};

// What the current user was delegated in the System Console. Fails closed:
// while loading, and when the lookup fails, nothing is granted.
export const useSettingsAccess = (): SettingsAccessState => {
    const [state, setState] = useState<SettingsAccessState>({level: 'none', manageOthersAgents: false, loading: true, error: false});

    useEffect(() => {
        let current = true;
        fetchMySettingsAccess().
            then(({level, manageOthersAgents}) => {
                if (current) {
                    setState({level, manageOthersAgents: manageOthersAgents === true, loading: false, error: false});
                }
            }).
            catch(() => {
                if (current) {
                    setState({level: 'none', manageOthersAgents: false, loading: false, error: true});
                }
            });
        return () => {
            current = false;
        };
    }, []);

    return state;
};
