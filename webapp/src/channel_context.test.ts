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
    mutateChannelContextPosts,
    canManageChannelContext,
    channelContextPostsFromState,
    channelContextPostsReducer,
    fetchChannelContextPosts,
    handleChannelContextUpdated,
    isChannelContextPinned,
    resetChannelContextRequests,
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

function deferred<T>() {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((res) => {
        resolve = res;
    });
    return {promise, resolve};
}

const stateOf = (store: ReturnType<typeof makeStore>) => channelContextPostsFromState(store.getState() as unknown as GlobalState, 'chan1');
const empty: ChannelContextPosts = {posts: [], max_posts: 10};

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

describe('superseded fetches', () => {
    test('an update arriving during the initial load triggers a fresh fetch whose result wins', async () => {
        const first = deferred<ChannelContextPosts>();
        mockedGetChannelContextPosts.mockReturnValueOnce(first.promise).mockResolvedValueOnce(pins);
        const store = makeStore();

        fetchChannelContextPosts(store.dispatch, 'chan1');
        handleChannelContextUpdated(store as never, {channel_id: 'chan1'});
        await flush();
        first.resolve(empty);
        await flush();

        expect(mockedGetChannelContextPosts).toHaveBeenCalledTimes(2);
        expect(stateOf(store)).toEqual(pins);
    });

    test('a fetch pending across a reset cannot repopulate the cleared state', async () => {
        const stale = deferred<ChannelContextPosts>();
        mockedGetChannelContextPosts.mockReturnValueOnce(stale.promise).mockResolvedValueOnce(pins);
        const store = makeStore();

        fetchChannelContextPosts(store.dispatch, 'chan1');
        resetChannelContextRequests();
        store.dispatch({type: ChannelContextPostsCleared});
        stale.resolve(empty);
        await flush();
        expect(stateOf(store)).toBeUndefined();

        await fetchChannelContextPosts(store.dispatch, 'chan1');
        expect(mockedGetChannelContextPosts).toHaveBeenCalledTimes(2);
        expect(stateOf(store)).toEqual(pins);
    });

    test('a pending initial fetch cannot overwrite pins returned by a later write', async () => {
        const initial = deferred<ChannelContextPosts>();
        mockedGetChannelContextPosts.mockReturnValueOnce(initial.promise);
        const store = makeStore();

        fetchChannelContextPosts(store.dispatch, 'chan1');
        await mutateChannelContextPosts(store.dispatch, 'chan1', () => Promise.resolve(pins));
        initial.resolve(empty);
        await flush();

        expect(stateOf(store)).toEqual(pins);
    });
});

describe('failed and overlapping requests', () => {
    test('a failed websocket refresh keeps the pins already loaded', async () => {
        mockedGetChannelContextPosts.mockResolvedValueOnce(pins).mockRejectedValueOnce(new Error('500'));
        const store = makeStore();
        await fetchChannelContextPosts(store.dispatch, 'chan1');

        handleChannelContextUpdated(store as never, {channel_id: 'chan1'});
        await flush();

        expect(stateOf(store)).toEqual(pins);
    });

    test('a failed first load is still recorded as null', async () => {
        mockedGetChannelContextPosts.mockRejectedValueOnce(new Error('500'));
        const store = makeStore();

        await fetchChannelContextPosts(store.dispatch, 'chan1');

        expect(stateOf(store)).toBeNull();
    });

    test('a mutation response superseded by a newer mutation is dropped', async () => {
        const store = makeStore();
        const older = deferred<ChannelContextPosts>();

        const first = mutateChannelContextPosts(store.dispatch, 'chan1', () => older.promise);
        await mutateChannelContextPosts(store.dispatch, 'chan1', () => Promise.resolve(empty));
        older.resolve(pins);
        await first;

        expect(stateOf(store)).toEqual(empty);
    });

    test('a mutation response superseded by a websocket refresh is dropped', async () => {
        mockedGetChannelContextPosts.mockResolvedValueOnce(empty).mockResolvedValueOnce(empty);
        const store = makeStore();
        await fetchChannelContextPosts(store.dispatch, 'chan1');
        const slow = deferred<ChannelContextPosts>();

        const mutation = mutateChannelContextPosts(store.dispatch, 'chan1', () => slow.promise);
        handleChannelContextUpdated(store as never, {channel_id: 'chan1'});
        await flush();
        slow.resolve(pins);
        await mutation;

        expect(stateOf(store)).toEqual(empty);
    });
});

describe('a refresh that fails after superseding a mutation', () => {
    test('keeps the mutation response that landed while the refresh was pending', async () => {
        const refresh = deferred<ChannelContextPosts>();
        mockedGetChannelContextPosts.mockResolvedValueOnce(pins).mockReturnValueOnce(refresh.promise);
        const store = makeStore();
        await fetchChannelContextPosts(store.dispatch, 'chan1');
        const unpin = deferred<ChannelContextPosts>();

        const mutation = mutateChannelContextPosts(store.dispatch, 'chan1', () => unpin.promise);
        handleChannelContextUpdated(store as never, {channel_id: 'chan1'});
        unpin.resolve(empty);
        await mutation;
        expect(stateOf(store)).toEqual(pins);

        refresh.resolve(Promise.reject(new Error('500')) as never);
        await flush();

        expect(stateOf(store)).toEqual(empty);
    });

    test('keeps the mutation response that lands after the refresh failed', async () => {
        mockedGetChannelContextPosts.mockResolvedValueOnce(pins).mockRejectedValueOnce(new Error('500'));
        const store = makeStore();
        await fetchChannelContextPosts(store.dispatch, 'chan1');
        const unpin = deferred<ChannelContextPosts>();

        const mutation = mutateChannelContextPosts(store.dispatch, 'chan1', () => unpin.promise);
        handleChannelContextUpdated(store as never, {channel_id: 'chan1'});
        await flush();
        unpin.resolve(empty);
        await mutation;

        expect(stateOf(store)).toEqual(empty);
    });
});

describe('mutation ownership across refreshes', () => {
    const justB: ChannelContextPosts = {posts: [pins.posts[0]], max_posts: 10};

    test('a confirmed mutation survives a replacement refresh when both refreshes fail', async () => {
        const first = deferred<ChannelContextPosts>();
        const second = deferred<ChannelContextPosts>();
        mockedGetChannelContextPosts.mockResolvedValueOnce(pins).mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
        const store = makeStore();
        await fetchChannelContextPosts(store.dispatch, 'chan1');
        const unpin = deferred<ChannelContextPosts>();

        const mutation = mutateChannelContextPosts(store.dispatch, 'chan1', () => unpin.promise);
        handleChannelContextUpdated(store as never, {channel_id: 'chan1'});
        unpin.resolve(empty);
        await mutation;
        handleChannelContextUpdated(store as never, {channel_id: 'chan1'});
        first.resolve(Promise.reject(new Error('500')) as never);
        second.resolve(Promise.reject(new Error('500')) as never);
        await flush();

        expect(stateOf(store)).toEqual(empty);
    });

    test('an older mutation response cannot win after a newer one, even when a refresh fails', async () => {
        mockedGetChannelContextPosts.mockResolvedValueOnce(pins).mockRejectedValueOnce(new Error('500'));
        const store = makeStore();
        await fetchChannelContextPosts(store.dispatch, 'chan1');
        const olderUnpin = deferred<ChannelContextPosts>();

        const older = mutateChannelContextPosts(store.dispatch, 'chan1', () => olderUnpin.promise);
        await mutateChannelContextPosts(store.dispatch, 'chan1', () => Promise.resolve(empty));
        handleChannelContextUpdated(store as never, {channel_id: 'chan1'});
        await flush();
        olderUnpin.resolve(justB);
        await older;

        expect(stateOf(store)).toEqual(empty);
    });

    test('a mutation response arriving after a successful refresh is dropped', async () => {
        const pending = deferred<ChannelContextPosts>();
        mockedGetChannelContextPosts.mockResolvedValueOnce(pins).mockResolvedValueOnce(justB);
        const store = makeStore();
        await fetchChannelContextPosts(store.dispatch, 'chan1');

        const mutation = mutateChannelContextPosts(store.dispatch, 'chan1', () => pending.promise);
        handleChannelContextUpdated(store as never, {channel_id: 'chan1'});
        await flush();
        pending.resolve(empty);
        await mutation;

        expect(stateOf(store)).toEqual(justB);
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
