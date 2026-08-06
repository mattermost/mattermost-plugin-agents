// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

/**
 * Returns true if the user's merged system roles include the given permission id
 * (e.g. manage_others_agent).
 */
export function userHasSystemPermission(state: GlobalState, userId: string, permissionId: string): boolean {
    const user = state.entities.users.profiles[userId];
    if (!user?.roles) {
        return false;
    }
    const roleNames = user.roles.trim().split(/\s+/).filter(Boolean);
    const rolesByName = state.entities.roles.roles;
    for (const name of roleNames) {
        const role = rolesByName[name];
        if (role?.permissions?.includes(permissionId)) {
            return true;
        }
    }
    return false;
}

/**
 * Selector hook: whether the current user's system roles include the permission id.
 */
export function useCurrentUserHasSystemPermission(permissionId: string): boolean {
    return useSelector((state: GlobalState) =>
        userHasSystemPermission(state, state.entities.users.currentUserId, permissionId));
}
