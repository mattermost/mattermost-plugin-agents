// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect, useState} from 'react';

import {getMySettingsAccess, type SettingsAccessLevel} from '@/client';

export type SettingsAccessState = {
    level: SettingsAccessLevel;

    // True until the first answer; the level reads 'none' meanwhile.
    loading: boolean;

    // True when the lookup failed. The level then reads 'none', but nothing
    // is known about the user's access.
    error: boolean;
};

// How much of the plugin configuration the current user may reach.
export const useSettingsAccess = (): SettingsAccessState => {
    const [state, setState] = useState<SettingsAccessState>({level: 'none', loading: true, error: false});

    useEffect(() => {
        let current = true;
        getMySettingsAccess().
            then((level) => {
                if (current) {
                    setState({level, loading: false, error: false});
                }
            }).
            catch(() => {
                if (current) {
                    setState({level: 'none', loading: false, error: true});
                }
            });
        return () => {
            current = false;
        };
    }, []);

    return state;
};
