// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {GlobalState} from '@mattermost/types/store';

import {
    PERMISSION_MANAGE_PRIVATE_CHANNEL_PROPERTIES,
    PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES,
    userHasChannelPermission,
} from './permissions';

const TEAM_ID = 'team1';
const CHANNEL_ID = 'chan1';

type StateShape = {
    userRoles?: string;
    teamRoles?: string;
    channelRoles?: Set<string>;
    roles?: Record<string, {permissions: string[]}>;
};

function makeState({userRoles, teamRoles, channelRoles, roles = {}}: StateShape): GlobalState {
    return {
        entities: {
            users: {
                currentUserId: 'me',
                // eslint-disable-next-line no-undefined
                profiles: userRoles === undefined ? {} : {me: {roles: userRoles}},
            },
            teams: {
                // eslint-disable-next-line no-undefined
                myMembers: teamRoles === undefined ? {} : {[TEAM_ID]: {roles: teamRoles}},
            },
            channels: {
                // eslint-disable-next-line no-undefined
                roles: channelRoles === undefined ? {} : {[CHANNEL_ID]: channelRoles},
            },
            roles: {roles},
        },
    } as unknown as GlobalState;
}

describe('userHasChannelPermission', () => {
    test('grants via a system role', () => {
        const state = makeState({
            userRoles: 'system_user system_admin',
            roles: {
                system_user: {permissions: ['create_post']},
                system_admin: {permissions: [PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES]},
            },
        });
        expect(userHasChannelPermission(state, TEAM_ID, CHANNEL_ID, PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES)).toBe(true);
    });

    test('grants via a team-member role', () => {
        const state = makeState({
            userRoles: 'system_user',
            teamRoles: 'team_user team_admin',
            roles: {
                system_user: {permissions: ['create_post']},
                team_user: {permissions: []},
                team_admin: {permissions: [PERMISSION_MANAGE_PRIVATE_CHANNEL_PROPERTIES]},
            },
        });
        expect(userHasChannelPermission(state, TEAM_ID, CHANNEL_ID, PERMISSION_MANAGE_PRIVATE_CHANNEL_PROPERTIES)).toBe(true);
    });

    test('grants via a channel-member role stored as a Set', () => {
        const state = makeState({
            userRoles: 'system_user',
            channelRoles: new Set(['channel_user', 'channel_admin']),
            roles: {
                system_user: {permissions: ['create_post']},
                channel_user: {permissions: []},
                channel_admin: {permissions: [PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES]},
            },
        });
        expect(userHasChannelPermission(state, TEAM_ID, CHANNEL_ID, PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES)).toBe(true);
    });

    test('denies when no role at any scope carries the permission', () => {
        const state = makeState({
            userRoles: 'system_user',
            teamRoles: 'team_user',
            channelRoles: new Set(['channel_user']),
            roles: {
                system_user: {permissions: ['create_post']},
                team_user: {permissions: ['view_team']},
                channel_user: {permissions: ['read_channel']},
            },
        });
        expect(userHasChannelPermission(state, TEAM_ID, CHANNEL_ID, PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES)).toBe(false);
    });

    test('denies when profile, team-member, and channel-member entries are all absent', () => {
        const state = makeState({
            roles: {
                system_admin: {permissions: [PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES]},
            },
        });
        expect(userHasChannelPermission(state, TEAM_ID, CHANNEL_ID, PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES)).toBe(false);
    });

    test('denies when a member role name has no entry in the roles map', () => {
        const state = makeState({
            userRoles: 'vanished_role',
            channelRoles: new Set(['also_vanished']),
            roles: {},
        });
        expect(userHasChannelPermission(state, TEAM_ID, CHANNEL_ID, PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES)).toBe(false);
    });

    test('does not grant a permission held only for a different channel or team', () => {
        const state = makeState({
            userRoles: 'system_user',
            teamRoles: 'team_admin',
            channelRoles: new Set(['channel_admin']),
            roles: {
                system_user: {permissions: []},
                team_admin: {permissions: [PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES]},
                channel_admin: {permissions: [PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES]},
            },
        });

        // Same state, but looked up for a team and channel the user has no membership rows for.
        expect(userHasChannelPermission(state, 'other-team', 'other-chan', PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES)).toBe(false);
    });
});
