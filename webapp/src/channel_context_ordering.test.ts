// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

/* eslint-disable no-await-in-loop -- scenarios run one at a time on shared module state */

import {combineReducers, createStore} from 'redux';

import {GlobalState} from '@mattermost/types/store';

import {ChannelContextPosts, getChannelContextPosts} from '@/client';
import manifest from '@/manifest';

import {
    channelContextPostsFromState,
    channelContextPostsReducer,
    fetchChannelContextPosts,
    handleChannelContextUpdated,
    mutateChannelContextPosts,
} from './channel_context';

jest.mock('@/client', () => ({
    getChannelContextPosts: jest.fn(),
}));

const mockedGet = getChannelContextPosts as jest.MockedFunction<typeof getChannelContextPosts>;

// Every pin or unpin and every refresh of one channel runs in some start order
// and settles in some order, each succeeding or failing. The server applies a
// mutation when it starts, and a refresh reads the server as of its start. The
// version of a response is the number of applied mutations it reflects, carried
// in max_posts so the store holds it. Whatever the interleaving, the pins
// shown at the end must be the newest successful response: a stale response
// never replaces a newer one, and a failure never hides a confirmed one.

const requests = ['M1', 'M2', 'R1', 'R2'];

function permutations<T>(items: T[]): T[][] {
    if (items.length <= 1) {
        return [items];
    }
    return items.flatMap((item, i) => permutations([...items.slice(0, i), ...items.slice(i + 1)]).map((rest) => [item, ...rest]));
}

const versionOf = (version: number): ChannelContextPosts => ({posts: [], max_posts: version});

type Deferred<T> = {promise: Promise<T>; resolve: (value: T) => void; reject: (reason: unknown) => void};

function deferred<T>(): Deferred<T> {
    let resolve!: (value: T) => void;
    let reject!: (reason: unknown) => void;
    const promise = new Promise<T>((res, rej) => {
        resolve = res;
        reject = rej;
    });
    return {promise, resolve, reject};
}

const flush = async () => {
    for (let i = 0; i < 10; i++) {
        await Promise.resolve();
    }
};

type Scenario = {start: string[]; settle: string[]; failing: string[]};

const scenarios: Scenario[] = [];
for (const start of permutations(requests)) {
    for (const settle of permutations(requests)) {
        for (let mask = 0; mask < 16; mask++) {
            scenarios.push({start, settle, failing: requests.filter((_, i) => mask & (1 << i))});
        }
    }
}

let channelCounter = 0;

async function run({start, settle, failing}: Scenario) {
    channelCounter++;
    const channelId = `chan${channelCounter}`;
    const store = createStore(combineReducers({
        ['plugins-' + manifest.id]: combineReducers({channelContextPosts: channelContextPostsReducer}),
    }));
    const stateOf = () => channelContextPostsFromState(store.getState() as unknown as GlobalState, channelId);

    const pendingGets: Array<Deferred<ChannelContextPosts>> = [];
    mockedGet.mockReset();
    mockedGet.mockImplementationOnce(() => Promise.resolve(versionOf(0)));
    mockedGet.mockImplementation(() => {
        const d = deferred<ChannelContextPosts>();
        pendingGets.push(d);
        return d.promise;
    });
    await fetchChannelContextPosts(store.dispatch, channelId);

    let serverVersion = 0;
    type Settle = () => Promise<void>;
    const settlers = new Map<string, Settle>();
    const succeeded: number[] = [0];
    const outcomes = new Map<string, number>();

    for (const name of start) {
        const ok = !failing.includes(name);
        if (name.startsWith('M')) {
            if (ok) {
                serverVersion++;
            }
            const version = serverVersion;
            const d = deferred<ChannelContextPosts>();
            const done = mutateChannelContextPosts(store.dispatch, channelId, () => d.promise).catch(() => null);
            settlers.set(name, async () => {
                if (ok) {
                    d.resolve(versionOf(version));
                    outcomes.set(name, version);
                } else {
                    d.reject(new Error('mutation failed'));
                }
                await done;
            });
        } else {
            const version = serverVersion;
            const before = pendingGets.length;
            handleChannelContextUpdated(store as never, {channel_id: channelId});
            const d = pendingGets[before];
            settlers.set(name, async () => {
                if (ok) {
                    d.resolve(versionOf(version));
                    outcomes.set(name, version);
                } else {
                    d.reject(new Error('refresh failed'));
                }
                await flush();
            });
        }
    }
    for (const name of settle) {
        await settlers.get(name)!();
    }
    await flush();
    succeeded.push(...outcomes.values());

    return {shown: stateOf()?.max_posts, expected: Math.max(...succeeded)};
}

describe('channel context mutation and refresh ordering', () => {
    test('the pins shown are always the newest successful response, in every ordering', async () => {
        const failures: string[] = [];
        for (const scenario of scenarios) {
            const {shown, expected} = await run(scenario);
            if (shown !== expected) {
                failures.push(`start ${scenario.start.join('>')} settle ${scenario.settle.join('>')} failing [${scenario.failing.join(',')}]: showed v${shown}, want v${expected}`);
            }
        }

        expect(failures.slice(0, 10)).toEqual([]);
        expect(failures).toHaveLength(0);
    }, 120000);
});
