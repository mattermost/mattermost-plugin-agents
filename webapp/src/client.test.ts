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

const siteURL = 'http://localhost:8065';

function okResponse(): Response {
    return {ok: true, status: 200, json: () => Promise.resolve({})} as unknown as Response;
}

// Mattermost IDs are 26 characters of lowercase letters and digits.
const WELL_FORMED_ID = 'c7f2m9xq4v1b8n3k6t5w0hzjd2';

// Ids reach these functions straight off a post prop, so a caller can hand them anything.
const NOT_WELL_FORMED_IDS: Array<{name: string; id: string}> = [
    {name: 'empty', id: ''},
    {name: 'relative path segments', id: '../../some/other/route'},
    {name: 'right length but contains a separator', id: 'abcdefghijklmnopqrstuvwxy/'},
    {name: 'well-formed id with leading whitespace', id: ` ${WELL_FORMED_ID}`},
];

// Resolves request URLs the way the browser would, so relative segments collapse first.
function requestedPathsOutside(prefix: string): string[] {
    return mockFetch.mock.calls.
        map(([url]) => new URL(url).pathname).
        filter((pathname) => !pathname.startsWith(prefix));
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

beforeAll(() => {
    setSiteURL(siteURL);
});

beforeEach(() => {
    mockFetch.mockReset();
    mockFetch.mockResolvedValue(okResponse());
});

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
    test('posts to the loop-in route for a well-formed post id', async () => {
        await expect(doLoopInAgent(WELL_FORMED_ID, 'matty')).resolves.toBeUndefined();

        expect(mockFetch).toHaveBeenCalledTimes(1);
        const [url, options] = mockFetch.mock.calls[0];
        expect(url).toBe(`${siteURL}/plugins/${manifest.id}/post/${WELL_FORMED_ID}/loop_in_agent?botUsername=matty`);
        expect(options).toEqual(expect.objectContaining({method: 'POST'}));
    });

    test('percent-encodes the bot username in the query string', async () => {
        await doLoopInAgent(WELL_FORMED_ID, 'agent bot&x=1');

        const [url] = mockFetch.mock.calls[0];
        expect(url).toBe(`${siteURL}/plugins/${manifest.id}/post/${WELL_FORMED_ID}/loop_in_agent?botUsername=agent%20bot%26x%3D1`);
    });

    test.each(NOT_WELL_FORMED_IDS)('does not issue a request when the post id is not well-formed: $name', async ({id}) => {
        await expect(doLoopInAgent(id, 'matty')).rejects.toThrow();

        expect(mockFetch).not.toHaveBeenCalled();
    });
});

describe('conversation reads', () => {
    const conversationsPrefix = `/plugins/${manifest.id}/conversations/`;

    const readers: Array<{reader: string; read: (id: string) => Promise<unknown>; suffix: string}> = [
        {reader: 'getConversation', read: getConversation, suffix: ''},
        {reader: 'getConversationContext', read: getConversationContext, suffix: '/context'},
    ];

    const notWellFormedCases = readers.flatMap(({reader, read}) =>
        NOT_WELL_FORMED_IDS.map(({name, id}) => ({reader, read, name, id})));

    test.each(readers)('$reader reads the conversation route for a well-formed id', async ({read, suffix}) => {
        await read(WELL_FORMED_ID);

        expect(mockFetch).toHaveBeenCalledTimes(1);
        const [url, options] = mockFetch.mock.calls[0];
        expect(url).toBe(`${siteURL}${conversationsPrefix}${WELL_FORMED_ID}${suffix}`);
        expect(options).toEqual(expect.objectContaining({method: 'GET'}));
    });

    // Declining to send anything is fine; landing on an unrelated route is not.
    test.each(notWellFormedCases)('$reader stays inside the conversation route when the id is not well-formed: $name', async ({read, id}) => {
        await read(id).catch(() => null);

        expect(requestedPathsOutside(conversationsPrefix)).toEqual([]);
    });
});

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
        await expect(getPost(id)).rejects.toThrow();

        expect(mockGetPost).not.toHaveBeenCalled();
    });
});

describe('route builders', () => {
    const traversingId = '../../../some/other/route';

    const routes: Array<{segment: string; request: (id: string) => Promise<unknown>}> = [
        {segment: 'post', request: (id) => doReaction(id)},
        {segment: 'channel', request: (id) => doChannelAnalysis(id, 'summarize_channel', 'matty')},
        {segment: 'agents', request: (id) => deleteAgent(id)},
        {segment: 'custom-prompts', request: (id) => deleteCustomPrompt(id)},
    ];

    test.each(routes)('keeps the value inside its path segment: $segment', async ({segment, request}) => {
        await request(traversingId).catch(() => null);

        expect(requestedPathsOutside(`/plugins/${manifest.id}/${segment}/`)).toEqual([]);
    });
});
