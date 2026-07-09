// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Channel} from '@mattermost/types/channels';
import type {GlobalState} from '@mattermost/types/store';

const MANAGE_PUBLIC_CHANNEL_PROPERTIES = 'manage_public_channel_properties';
const MANAGE_PRIVATE_CHANNEL_PROPERTIES = 'manage_private_channel_properties';

function splitRoles(roles?: string): string[] {
    return roles?.trim().split(/\s+/).filter(Boolean) ?? [];
}

function rolesHavePermission(state: GlobalState, roleNames: Iterable<string>, permissionId: string): boolean {
    const rolesByName = state.entities.roles.roles;
    for (const name of roleNames) {
        if (rolesByName[name]?.permissions?.includes(permissionId)) {
            return true;
        }
    }
    return false;
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
    return rolesHavePermission(state, splitRoles(user.roles), permissionId);
}

export function userHasChannelPermission(state: GlobalState, channel: Channel, permissionId: string): boolean {
    const currentUserId = state.entities.users.currentUserId;
    const roleNames = new Set(splitRoles(state.entities.users.profiles[currentUserId]?.roles));

    for (const roleName of splitRoles(state.entities.teams.myMembers[channel.team_id]?.roles)) {
        roleNames.add(roleName);
    }
    for (const roleName of state.entities.channels.roles[channel.id] ?? []) {
        roleNames.add(roleName);
    }

    return rolesHavePermission(state, roleNames, permissionId);
}

export function canManageChannelContext(state: GlobalState, channel: Channel): boolean {
    switch (channel.type) {
    case 'O':
        return userHasChannelPermission(state, channel, MANAGE_PUBLIC_CHANNEL_PROPERTIES);
    case 'P':
        return userHasChannelPermission(state, channel, MANAGE_PRIVATE_CHANNEL_PROPERTIES);
    default:
        return false;
    }
}
