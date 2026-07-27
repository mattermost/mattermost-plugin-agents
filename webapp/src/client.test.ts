// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {ChannelSearchOpts, ChannelWithTeamData} from '@mattermost/types/channels';
import type {OptsSignalExt} from '@mattermost/types/client4';

import type {ConversationResponse, Turn} from '@/types/conversation';

import manifest from './manifest';

import {
    deleteAgent,
    deleteCustomPrompt,
    doChannelAnalysis,
    doLoopInAgent,
    doReaction,
    getConversation,
    getConversationContext,
    getPost,
    normalizeConversationResponse,
    searchAllChannels,
    setSiteURL,
    updateRead,
} from './client';

type SearchAllChannelsOpts = Omit<ChannelSearchOpts, 'page' | 'per_page'> & OptsSignalExt;

jest.mock('@mattermost/client', () => {
    const mockSearchAllChannels = jest.fn<
        Promise<ChannelWithTeamData[]>,
        [string, SearchAllChannelsOpts | undefined]
    >();
    const mockUpdateThreadReadForUser = jest.fn();
    const mockGetPost = jest.fn();

    return {

        // client.tsx constructs `new Client4()`; the mocked class exposes instance methods.
        Client4: class Client4 {
            url = '';
            searchAllChannels = mockSearchAllChannels;
            updateThreadReadForUser = mockUpdateThreadReadForUser;
            getPost = mockGetPost;

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
        mockGetPost,
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

const {mockGetPost} = jest.requireMock('@mattermost/client') as {
    mockGetPost: jest.MockedFunction<(postId: string) => Promise<unknown>>;
};

const mockFetch = jest.fn<Promise<Response>, [string, RequestInit]>();
global.fetch = mockFetch as unknown as typeof fetch;

function okResponse(): Response {
    return {ok: true, status: 200} as unknown as Response;
}

function jsonResponse(body: unknown): Response {
    return {ok: true, status: 200, json: () => Promise.resolve(body)} as unknown as Response;
}

// Mattermost IDs are 26 characters of lowercase letters and digits.
const WELL_FORMED_ID = 'c7f2m9xq4v1b8n3k6t5w0hzjd2';

const NOT_WELL_FORMED_IDS: Array<{name: string; id: string}> = [
    {name: 'relative path segments', id: '../../some/other/route'},
    {name: 'relative path segments after a well-formed prefix', id: `${WELL_FORMED_ID}/../../../some/other/route`},
    {name: 'percent-encoded separators', id: '..%2f..%2fsome%2froute'},
    {name: 'right length but contains a separator', id: 'abcdefghijklmnopqrstuvwxy/'},
    {name: 'right length but contains query and fragment markers', id: 'abcdefghijklmnopqrstuvw?x#'},
    {name: 'too short', id: 'abc'},
    {name: 'too long', id: 'abcdefghijklmnopqrstuvwxyz7'},
    {name: 'right length but uppercase', id: 'ABCDEFGHIJKLMNOPQRSTUVWXYZ'},
    {name: 'right length but not ascii', id: 'abcdefghijklmnopqrstuvwxy\u00e9'},
    {name: 'well-formed id with leading whitespace', id: ` ${WELL_FORMED_ID}`},
];

// Resolves a request URL the way the browser would before it goes on the wire,
// so relative segments collapse before the assertion runs.
function requestedPathsOutside(prefix: string): string[] {
    return mockFetch.mock.calls.
        map(([url]) => new URL(url).pathname).
        filter((pathname) => !pathname.startsWith(prefix));
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

// Conversation IDs reach these readers straight off a post prop, so they are
// only as trustworthy as whoever authored the post.
describe('conversation reads', () => {
    const siteURL = 'http://localhost:8065';
    const conversationsPrefix = `/plugins/${manifest.id}/conversations/`;

    const readers: Array<{reader: string; read: (id: string) => Promise<unknown>; suffix: string}> = [
        {reader: 'getConversation', read: getConversation, suffix: ''},
        {reader: 'getConversationContext', read: getConversationContext, suffix: '/context'},
    ];

    const escapeCases = readers.flatMap(({reader, read}) =>
        NOT_WELL_FORMED_IDS.map(({name, id}) => ({reader, read, name, id})));

    beforeAll(() => {
        setSiteURL(siteURL);
    });

    beforeEach(() => {
        mockFetch.mockReset();
        mockFetch.mockResolvedValue(jsonResponse({turns: []}));
    });

    test.each(readers)('$reader reads the conversation route for a well-formed id', async ({read, suffix}) => {
        await read(WELL_FORMED_ID);

        expect(mockFetch).toHaveBeenCalledTimes(1);
        const [url, options] = mockFetch.mock.calls[0];
        expect(url).toBe(`${siteURL}${conversationsPrefix}${WELL_FORMED_ID}${suffix}`);
        expect(options).toEqual(expect.objectContaining({method: 'GET'}));
    });

    test.each(escapeCases)('$reader stays inside the conversation route when the id is not well-formed: $name', async ({read, id}) => {
        // Declining to send anything is also acceptable; what must never happen
        // is a request landing on some unrelated route.
        await didReject(read(id));

        expect(requestedPathsOutside(conversationsPrefix)).toEqual([]);
    });
});

// PostPreview feeds getPost an id taken from a post prop, and Client4 drops the
// id straight into the request path.
describe('getPost', () => {
    beforeEach(() => {
        mockGetPost.mockReset();
        mockGetPost.mockResolvedValue({id: WELL_FORMED_ID});
    });

    test('reads the post for a well-formed id', async () => {
        await expect(getPost(WELL_FORMED_ID)).resolves.toEqual({id: WELL_FORMED_ID});
        expect(mockGetPost).toHaveBeenCalledWith(WELL_FORMED_ID);
    });

    test.each(NOT_WELL_FORMED_IDS)('does not read a post by an id that is not well-formed: $name', async ({id}) => {
        try {
            await getPost(id);
        } catch {
            // Refusing the id is an acceptable outcome; the assertion below is
            // about what reached Client4.
        }

        expect(mockGetPost).not.toHaveBeenCalled();
    });
});

// The route builders are the last line for callers that hand them an id
// without checking it first.
describe('route builders', () => {
    const siteURL = 'http://localhost:8065';
    const traversingId = '../../../some/other/route';

    const routes: Array<{segment: string; request: (id: string) => Promise<unknown>}> = [
        {segment: 'post', request: (id) => doReaction(id)},
        {segment: 'channel', request: (id) => doChannelAnalysis(id, 'summarize_channel', 'matty')},
        {segment: 'agents', request: (id) => deleteAgent(id)},
        {segment: 'custom-prompts', request: (id) => deleteCustomPrompt(id)},
    ];

    beforeAll(() => {
        setSiteURL(siteURL);
    });

    beforeEach(() => {
        mockFetch.mockReset();
        mockFetch.mockResolvedValue(jsonResponse({}));
    });

    test.each(routes)('keeps the value inside its path segment: $segment', async ({segment, request}) => {
        await didReject(request(traversingId));

        expect(requestedPathsOutside(`/plugins/${manifest.id}/${segment}/`)).toEqual([]);
    });
});
