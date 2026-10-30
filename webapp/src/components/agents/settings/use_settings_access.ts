// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect, useState} from 'react';

import {getMySettingsAccess, type SettingsAccessLevel} from '@/client';

export type SettingsAccessState = {
    level: SettingsAccessLevel;

    // True until the first answer; the level reads 'none' meanwhile.
    loading: boolean;
};

// How much of the plugin configuration the current user may reach. A failed
// lookup reads as no access: the server enforces the same rule anyway.
export const useSettingsAccess = (): SettingsAccessState => {
    const [state, setState] = useState<SettingsAccessState>({level: 'none', loading: true});

    useEffect(() => {
        let current = true;
        getMySettingsAccess().
            then((level) => {
                if (current) {
                    setState({level, loading: false});
                }
            }).
            catch(() => {
                if (current) {
                    setState({level: 'none', loading: false});
                }
            });
        return () => {
            current = false;
        };
    }, []);

    return state;
};
