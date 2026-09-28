// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

export const PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES = 'manage_public_channel_properties';
export const PERMISSION_MANAGE_PRIVATE_CHANNEL_PROPERTIES = 'manage_private_channel_properties';

function roleGrantsPermission(state: GlobalState, roleName: string, permissionId: string): boolean {
    return Boolean(state.entities.roles.roles[roleName]?.permissions?.includes(permissionId));
}

function splitRoleNames(roles: string | undefined): string[] {
    if (!roles) {
        return [];
    }
    return roles.trim().split(/\s+/).filter(Boolean);
}

/**
 * Returns true if the user's merged system roles include the given permission id
 * (e.g. manage_others_agent).
 */
export function userHasSystemPermission(state: GlobalState, userId: string, permissionId: string): boolean {
    const user = state.entities.users.profiles[userId];
    if (!user?.roles) {
        return false;
    }
    for (const name of splitRoleNames(user.roles)) {
        if (roleGrantsPermission(state, name, permissionId)) {
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

/**
 * Mirrors mattermost-redux haveIChannelPermission: the permission is granted if
 * any of the current user's system, team-member, or channel-member roles
 * includes it. Plain function (no memoization) — role sets are small and this
 * runs inside the host's channel-settings selectors.
 */
export function userHasChannelPermission(state: GlobalState, teamId: string, channelId: string, permissionId: string): boolean {
    const currentUserId = state.entities.users.currentUserId;
    const systemRoles = splitRoleNames(state.entities.users.profiles[currentUserId]?.roles);
    for (const name of systemRoles) {
        if (roleGrantsPermission(state, name, permissionId)) {
            return true;
        }
    }

    const teamRoles = splitRoleNames(state.entities.teams.myMembers[teamId]?.roles);
    for (const name of teamRoles) {
        if (roleGrantsPermission(state, name, permissionId)) {
            return true;
        }
    }

    // Channel-member roles are a Set<string> per @mattermost/types ChannelsState.
    const channelRoles = state.entities.channels.roles[channelId];
    if (channelRoles) {
        for (const name of channelRoles) {
            if (roleGrantsPermission(state, name, permissionId)) {
                return true;
            }
        }
    }

    return false;
}
