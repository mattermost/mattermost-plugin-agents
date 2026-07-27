// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {ChannelSearchOpts, ChannelWithTeamData} from '@mattermost/types/channels';
import type {OptsSignalExt} from '@mattermost/types/client4';

import type {ConversationResponse, Turn} from '@/types/conversation';

import manifest from './manifest';

import {doLoopInAgent, normalizeConversationResponse, searchAllChannels, setSiteURL, updateRead} from './client';

type SearchAllChannelsOpts = Omit<ChannelSearchOpts, 'page' | 'per_page'> & OptsSignalExt;

jest.mock('@mattermost/client', () => {
    const mockSearchAllChannels = jest.fn<
        Promise<ChannelWithTeamData[]>,
        [string, SearchAllChannelsOpts | undefined]
    >();
    const mockUpdateThreadReadForUser = jest.fn();

    return {

        // client.tsx constructs `new Client4()`; the mocked class exposes instance methods.
        Client4: class Client4 {
            url = '';
            searchAllChannels = mockSearchAllChannels;
            updateThreadReadForUser = mockUpdateThreadReadForUser;

            setUrl(url: string) {
                this.url = url;
            }

            getOptions(options: Record<string, unknown>) {
                return {...options, headers: {'X-Requested-With': 'XMLHttpRequest'}};
            }
        },
        ClientError: class extends Error {},
        mockSearchAllChannels,
        mockUpdateThreadReadForUser,
    };
});

const {mockSearchAllChannels} = jest.requireMock('@mattermost/client') as {
    mockSearchAllChannels: jest.MockedFunction<
        (term: string, opts?: SearchAllChannelsOpts) => Promise<ChannelWithTeamData[]>
    >;
};

const {mockUpdateThreadReadForUser} = jest.requireMock('@mattermost/client') as {
    mockUpdateThreadReadForUser: jest.MockedFunction<
        (userId: string, teamId: string, postId: string, timestamp: number) => Promise<void>
    >;
};

const mockFetch = jest.fn<Promise<Response>, [string, RequestInit]>();
global.fetch = mockFetch as unknown as typeof fetch;

function okResponse(): Response {
    return {ok: true, status: 200} as unknown as Response;
}

// Reports rejection without constraining the error type the caller throws.
async function didReject(promise: Promise<unknown>): Promise<boolean> {
    try {
        await promise;
        return false;
    } catch {
        return true;
    }
}

function makeTurn(overrides: Partial<Turn> = {}): Turn {
    return {
        id: 't',
        post_id: 'p',
        role: 'assistant',
        content: [],
        tokens_in: 0,
        tokens_out: 0,
        sequence: 1,
        ...overrides,
    };
}

function makeConv(overrides: Partial<ConversationResponse> = {}): ConversationResponse {
    return {
        id: 'c',
        user_id: 'u',
        bot_id: 'b',
        channel_id: null,
        root_post_id: null,
        title: '',
        operation: 'conversation',
        turns: [],
        ...overrides,
    };
}

describe('normalizeConversationResponse', () => {
    beforeEach(() => {
        mockSearchAllChannels.mockReset();
    });

    test('replaces null turn content with an empty array', () => {
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        const raw = makeConv({turns: [makeTurn({content: null as any})]});
        const normalized = normalizeConversationResponse(raw);
        expect(normalized.turns[0].content).toEqual([]);
    });

    test('preserves populated content blocks', () => {
        const raw = makeConv({
            turns: [makeTurn({content: [{type: 'text', text: 'hi'}]})],
        });
        const normalized = normalizeConversationResponse(raw);
        expect(normalized.turns[0].content).toEqual([{type: 'text', text: 'hi'}]);
    });

    test('handles a missing turns array', () => {
        // eslint-disable-next-line @typescript-eslint/no-explicit-any, no-undefined
        const raw = makeConv({turns: undefined as any});
        const normalized = normalizeConversationResponse(raw);
        expect(normalized.turns).toEqual([]);
    });

    test('normalizes every turn independently', () => {
        const raw = makeConv({
            turns: [
                makeTurn({id: 't1', sequence: 1, content: [{type: 'text', text: 'a'}]}),
                // eslint-disable-next-line @typescript-eslint/no-explicit-any
                makeTurn({id: 't2', sequence: 2, content: null as any}),
                makeTurn({id: 't3', sequence: 3, content: []}),
            ],
        });
        const normalized = normalizeConversationResponse(raw);
        expect(normalized.turns[0].content).toHaveLength(1);
        expect(normalized.turns[1].content).toEqual([]);
        expect(normalized.turns[2].content).toEqual([]);
    });
});

describe('searchAllChannels', () => {
    beforeEach(() => {
        mockSearchAllChannels.mockReset();
    });

    test('uses the non-admin search path for channel scoping', async () => {
        const channels = [{id: 'channel-id'} as ChannelWithTeamData];
        mockSearchAllChannels.mockResolvedValue(channels);

        await expect(searchAllChannels('town')).resolves.toEqual(channels);
        expect(mockSearchAllChannels).toHaveBeenCalledWith('town', {
            nonAdminSearch: true,
            public: true,
            private: true,
            include_deleted: false,
            deleted: false,
        });
    });
});

describe('updateRead', () => {
    beforeEach(() => {
        mockUpdateThreadReadForUser.mockReset();
    });

    test('returns the updateThreadReadForUser promise', async () => {
        const readPromise = Promise.resolve();
        mockUpdateThreadReadForUser.mockReturnValue(readPromise);

        const result = updateRead('user-id', 'team-id', 'post-id', 123);

        expect(result).toBe(readPromise);
        await expect(result).resolves.toBeUndefined();
        expect(mockUpdateThreadReadForUser).toHaveBeenCalledWith('user-id', 'team-id', 'post-id', 123);
    });

    test('propagates updateThreadReadForUser rejection', async () => {
        const error = new Error('User thread membership doesn\'t exist');
        mockUpdateThreadReadForUser.mockRejectedValue(error);

        await expect(updateRead('user-id', 'team-id', 'post-id', 123)).rejects.toBe(error);
    });
});

describe('doLoopInAgent', () => {
    const siteURL = 'http://localhost:8065';

    // Mattermost IDs are 26 characters of lowercase letters and digits.
    const wellFormedPostId = 'ehz9k3wqr7t1a5m2xd8pnb4jsc';

    const notWellFormedPostIds: Array<{name: string; postId: string}> = [
        {name: 'relative path segments', postId: '../../some/other/route'},
        {name: 'percent-encoded separators', postId: '..%2f..%2fsome%2froute'},
        {name: 'right length but contains a separator', postId: 'abcdefghijklmnopqrstuvwxy/'},
        {name: 'right length but contains query and fragment markers', postId: 'abcdefghijklmnopqrstuvw?x#'},
        {name: 'too short', postId: 'abc'},
        {name: 'too long', postId: 'abcdefghijklmnopqrstuvwxyz7'},
        {name: 'right length but uppercase', postId: 'ABCDEFGHIJKLMNOPQRSTUVWXYZ'},
        {name: 'right length but not ascii', postId: 'abcdefghijklmnopqrstuvwxy\u00e9'},
        {name: 'empty', postId: ''},
        {name: 'well-formed id with leading whitespace', postId: ` ${wellFormedPostId}`},
    ];

    beforeAll(() => {
        setSiteURL(siteURL);
    });

    beforeEach(() => {
        mockFetch.mockReset();
        mockFetch.mockResolvedValue(okResponse());
    });

    test('posts to the loop-in route for a well-formed post id', async () => {
        await expect(doLoopInAgent(wellFormedPostId, 'matty')).resolves.toBeUndefined();

        expect(mockFetch).toHaveBeenCalledTimes(1);
        const [url, options] = mockFetch.mock.calls[0];
        expect(url).toBe(`${siteURL}/plugins/${manifest.id}/post/${wellFormedPostId}/loop_in_agent?botUsername=matty`);
        expect(options).toEqual(expect.objectContaining({method: 'POST'}));
    });

    test('percent-encodes the bot username in the query string', async () => {
        await doLoopInAgent(wellFormedPostId, 'agent bot&x=1');

        const [url] = mockFetch.mock.calls[0];
        expect(url).toBe(`${siteURL}/plugins/${manifest.id}/post/${wellFormedPostId}/loop_in_agent?botUsername=agent%20bot%26x%3D1`);
    });

    test.each(notWellFormedPostIds)('does not issue a request when the post id is not well-formed: $name', async ({postId}) => {
        const rejected = await didReject(doLoopInAgent(postId, 'matty'));

        expect(mockFetch).not.toHaveBeenCalled();
        expect(rejected).toBe(true);
    });
});
