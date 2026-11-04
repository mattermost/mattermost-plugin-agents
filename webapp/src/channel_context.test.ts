// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {combineReducers, createStore} from 'redux';

import {Channel} from '@mattermost/types/channels';
import {GlobalState} from '@mattermost/types/store';

import {ChannelContextPosts, getChannelContextPosts} from '@/client';
import manifest from '@/manifest';
import {PERMISSION_MANAGE_PRIVATE_CHANNEL_PROPERTIES, PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES} from '@/utils/permissions';

import {
    ChannelContextPostsCleared,
    canManageChannelContext,
    channelContextPostsFromState,
    channelContextPostsReducer,
    fetchChannelContextPosts,
    handleChannelContextUpdated,
    isChannelContextPinned,
} from './channel_context';

jest.mock('@/client', () => ({
    getChannelContextPosts: jest.fn(),
}));

const mockedGetChannelContextPosts = getChannelContextPosts as jest.MockedFunction<typeof getChannelContextPosts>;

const pluginKey = 'plugins-' + manifest.id;
const pins: ChannelContextPosts = {
    posts: [{post_id: 'p1', user_id: 'u', root_id: '', message: 'm', create_at: 1, pinned_by: 'u', pinned_at: 1}],
    max_posts: 10,
};

function makeStore(opts: {sku?: string; channelPermissions?: string[]} = {}) {
    const entities = {
        general: {config: {}, license: {SkuShortName: opts.sku ?? 'advanced'}},
        users: {currentUserId: 'me', profiles: {me: {roles: 'system_user'}}},
        teams: {myMembers: {team1: {roles: 'team_user'}}},
        channels: {channels: {}, roles: {chan1: new Set(['channel_role'])}},
        roles: {
            roles: {
                system_user: {permissions: []},
                team_user: {permissions: []},
                channel_role: {permissions: opts.channelPermissions ?? []},
            },
        },
    };
    return createStore(combineReducers({
        entities: (state = entities) => state,
        [pluginKey]: combineReducers({channelContextPosts: channelContextPostsReducer}),
    }));
}

function channel(type: string): Channel {
    return {id: 'chan1', team_id: 'team1', type} as Channel;
}

const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

beforeEach(() => {
    mockedGetChannelContextPosts.mockReset();
});

describe('fetchChannelContextPosts', () => {
    test('concurrent requests for a channel share one fetch and store the result', async () => {
        mockedGetChannelContextPosts.mockResolvedValue(pins);
        const store = makeStore();

        await Promise.all([
            fetchChannelContextPosts(store.dispatch, 'chan1'),
            fetchChannelContextPosts(store.dispatch, 'chan1'),
        ]);

        expect(mockedGetChannelContextPosts).toHaveBeenCalledTimes(1);
        expect(channelContextPostsFromState(store.getState() as unknown as GlobalState, 'chan1')).toEqual(pins);
    });

    test('a failed fetch is recorded as null so it is not retried on every render', async () => {
        mockedGetChannelContextPosts.mockRejectedValue(new Error('403'));
        const store = makeStore();

        await fetchChannelContextPosts(store.dispatch, 'chan1');

        expect(channelContextPostsFromState(store.getState() as unknown as GlobalState, 'chan1')).toBeNull();
    });

    test('clearing drops every loaded channel', async () => {
        mockedGetChannelContextPosts.mockResolvedValue(pins);
        const store = makeStore();
        await fetchChannelContextPosts(store.dispatch, 'chan1');

        store.dispatch({type: ChannelContextPostsCleared});

        expect(channelContextPostsFromState(store.getState() as unknown as GlobalState, 'chan1')).toBeUndefined();
    });
});

describe('handleChannelContextUpdated', () => {
    test('re-fetches a channel this client has loaded', async () => {
        mockedGetChannelContextPosts.mockResolvedValue({posts: [], max_posts: 10});
        const store = makeStore();
        await fetchChannelContextPosts(store.dispatch, 'chan1');
        mockedGetChannelContextPosts.mockResolvedValue(pins);

        handleChannelContextUpdated(store as never, {channel_id: 'chan1'});
        await flush();

        expect(mockedGetChannelContextPosts).toHaveBeenCalledTimes(2);
        expect(channelContextPostsFromState(store.getState() as unknown as GlobalState, 'chan1')).toEqual(pins);
    });

    test.each([
        {name: 'a channel this client never loaded', event: {channel_id: 'other'}},
        {name: 'an event without a channel', event: {}},
    ])('ignores $name', ({event}) => {
        const store = makeStore();

        handleChannelContextUpdated(store as never, event);

        expect(mockedGetChannelContextPosts).not.toHaveBeenCalled();
    });
});

describe('canManageChannelContext', () => {
    test.each([
        {name: 'public channel with the public manage permission', type: 'O', perms: [PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES], want: true},
        {name: 'private channel with the private manage permission', type: 'P', perms: [PERMISSION_MANAGE_PRIVATE_CHANNEL_PROPERTIES], want: true},
        {name: 'public channel with only the private manage permission', type: 'O', perms: [PERMISSION_MANAGE_PRIVATE_CHANNEL_PROPERTIES], want: false},
        {name: 'public channel without permissions', type: 'O', perms: [], want: false},
        {name: 'direct message', type: 'D', perms: [PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES], want: false},
        {name: 'group message', type: 'G', perms: [PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES], want: false},
        {name: 'below Enterprise Advanced', type: 'O', perms: [PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES], sku: 'enterprise', want: false},
    ])('$name -> $want', ({type, perms, sku, want}: {type: string; perms: string[]; sku?: string; want: boolean}) => {
        const store = makeStore({sku, channelPermissions: perms});

        expect(canManageChannelContext(store.getState() as unknown as GlobalState, channel(type))).toBe(want);
    });

    test('an unknown channel cannot be managed', () => {
        expect(canManageChannelContext(makeStore().getState() as unknown as GlobalState, undefined)).toBe(false); // eslint-disable-line no-undefined
    });
});

describe('isChannelContextPinned', () => {
    test.each([
        {name: 'pinned post', data: pins, postId: 'p1', want: true},
        {name: 'unpinned post', data: pins, postId: 'p2', want: false},
        {name: 'pins not loaded', data: undefined, postId: 'p1', want: false}, // eslint-disable-line no-undefined
        {name: 'pins failed to load', data: null, postId: 'p1', want: false},
    ])('$name -> $want', ({data, postId, want}) => {
        expect(isChannelContextPinned(data, postId)).toBe(want);
    });
});
