// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Channel} from '@mattermost/types/channels';
import type {GlobalState} from '@mattermost/types/store';

import {canManageChannelContext} from './permissions';

const userID = 'user-1';
const teamID = 'team-1';
const channelID = 'channel-1';

function makeChannel(type = 'O'): Channel {
    return {
        id: channelID,
        team_id: teamID,
        type,
    } as Channel;
}

function makeState({
    systemRoles = '',
    teamRoles = '',
    channelRoles = [],
    permissions = {},
}: {
    systemRoles?: string;
    teamRoles?: string;
    channelRoles?: string[];
    permissions?: Record<string, string[]>;
} = {}): GlobalState {
    const roles = Object.fromEntries(Object.entries(permissions).map(([name, rolePermissions]) => [
        name,
        {name, permissions: rolePermissions},
    ]));

    return {
        entities: {
            users: {
                currentUserId: userID,
                profiles: {[userID]: {id: userID, roles: systemRoles}},
            },
            teams: {
                myMembers: {[teamID]: {roles: teamRoles}},
            },
            channels: {
                roles: {[channelID]: new Set(channelRoles)},
            },
            roles: {roles},
        },
    } as unknown as GlobalState;
}

describe('canManageChannelContext', () => {
    test.each([
        {
            name: 'system role grants public permission',
            channel: makeChannel('O'),
            state: makeState({
                systemRoles: 'system-manager',
                permissions: {'system-manager': ['manage_public_channel_properties']},
            }),
            expected: true,
        },
        {
            name: 'team role grants public permission',
            channel: makeChannel('O'),
            state: makeState({
                teamRoles: 'team-manager',
                permissions: {'team-manager': ['manage_public_channel_properties']},
            }),
            expected: true,
        },
        {
            name: 'channel role grants private permission',
            channel: makeChannel('P'),
            state: makeState({
                channelRoles: ['channel-manager'],
                permissions: {'channel-manager': ['manage_private_channel_properties']},
            }),
            expected: true,
        },
        {
            name: 'wrong channel permission is denied',
            channel: makeChannel('P'),
            state: makeState({
                systemRoles: 'public-manager',
                permissions: {'public-manager': ['manage_public_channel_properties']},
            }),
            expected: false,
        },
        {
            name: 'missing role data fails closed',
            channel: makeChannel('O'),
            state: makeState(),
            expected: false,
        },
        {
            name: 'direct channels are not supported',
            channel: makeChannel('D'),
            state: makeState({
                systemRoles: 'manager',
                permissions: {manager: ['manage_public_channel_properties', 'manage_private_channel_properties']},
            }),
            expected: false,
        },
    ])('$name', ({channel, state, expected}) => {
        expect(canManageChannelContext(state, channel)).toBe(expected);
    });
});
