// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {ChannelSearchOpts, ChannelWithTeamData} from '@mattermost/types/channels';
import type {OptsSignalExt} from '@mattermost/types/client4';

import type {ConversationResponse, Turn} from '@/types/conversation';

import manifest from './manifest';

import {
    doAskUserResponse,
    doLoopInAgent,
    getConversation,
    getConversationContext,
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

        // Mirrors the real ClientError just enough for callers that branch on
        // status_code (e.g. the ask-user 409 handling).
        ClientError: class extends Error {
            status_code?: number;
            url?: string;

            constructor(baseUrl: string, data: {message: string; status_code?: number; url?: string}) {
                super(data.message);
                this.status_code = data.status_code;
                this.url = data.url;
            }
        },
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

const siteURL = 'http://localhost:8065';

function okResponse(): Response {
    return {ok: true, status: 200, json: () => Promise.resolve({})} as unknown as Response;
}

function jsonResponse(body: unknown): Response {
    return {ok: true, status: 200, json: () => Promise.resolve(body)} as unknown as Response;
}

// Mattermost IDs are 26 characters of lowercase letters and digits.
const WELL_FORMED_ID = 'c7f2m9xq4v1b8n3k6t5w0hzjd2';

// These ids reach the client straight off free-form post props, so a caller can hand them anything.
const NOT_WELL_FORMED_IDS: Array<{name: string; id: string}> = [
    {name: 'empty', id: ''},
    {name: 'relative path segments', id: '../../some/other/route'},
    {name: 'right length but contains a separator', id: 'abcdefghijklmnopqrstuvwxy/'},
    {name: 'well-formed id with leading whitespace', id: ` ${WELL_FORMED_ID}`},
];

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

describe('doAskUserResponse', () => {
    test('posts the exact answer body to the ask_user_response route', async () => {
        mockFetch.mockResolvedValue(jsonResponse({status: 'answered'}));

        await expect(doAskUserResponse(WELL_FORMED_ID, {action: 'answer', selected: ['A'], free_form: ''})).
            resolves.toEqual({status: 'answered'});

        expect(mockFetch).toHaveBeenCalledTimes(1);
        const [url, options] = mockFetch.mock.calls[0];
        expect(url).toBe(`${siteURL}/plugins/${manifest.id}/post/${WELL_FORMED_ID}/ask_user_response`);
        expect(options).toEqual(expect.objectContaining({
            method: 'POST',
            body: JSON.stringify({action: 'answer', selected: ['A'], free_form: ''}),
        }));
    });

    test('serializes a decline action', async () => {
        mockFetch.mockResolvedValue(jsonResponse({status: 'declined'}));

        await expect(doAskUserResponse(WELL_FORMED_ID, {action: 'decline', selected: [], free_form: ''})).
            resolves.toEqual({status: 'declined'});

        const [, options] = mockFetch.mock.calls[0];
        expect(options).toEqual(expect.objectContaining({
            body: JSON.stringify({action: 'decline', selected: [], free_form: ''}),
        }));
    });

    test('rejects with the response status code on a non-OK response', async () => {
        mockFetch.mockResolvedValue({ok: false, status: 409} as unknown as Response);

        await expect(doAskUserResponse(WELL_FORMED_ID, {action: 'answer', selected: ['A'], free_form: ''})).
            rejects.toMatchObject({status_code: 409});
    });
});

describe('getConversation', () => {
    test('requests the conversation route for a well-formed id', async () => {
        await expect(getConversation(WELL_FORMED_ID)).resolves.toEqual({turns: []});

        expect(mockFetch).toHaveBeenCalledTimes(1);
        const [url, options] = mockFetch.mock.calls[0];
        expect(url).toBe(`${siteURL}/plugins/${manifest.id}/conversations/${WELL_FORMED_ID}`);
        expect(options).toEqual(expect.objectContaining({method: 'GET'}));
    });

    test.each(NOT_WELL_FORMED_IDS)('does not issue a request when the conversation id is not well-formed: $name', async ({id}) => {
        await expect(getConversation(id)).rejects.toThrow();

        expect(mockFetch).not.toHaveBeenCalled();
    });
});

describe('getConversationContext', () => {
    test('requests the conversation context route for a well-formed id', async () => {
        await expect(getConversationContext(WELL_FORMED_ID)).resolves.toEqual({});

        expect(mockFetch).toHaveBeenCalledTimes(1);
        const [url, options] = mockFetch.mock.calls[0];
        expect(url).toBe(`${siteURL}/plugins/${manifest.id}/conversations/${WELL_FORMED_ID}/context`);
        expect(options).toEqual(expect.objectContaining({method: 'GET'}));
    });

    test.each(NOT_WELL_FORMED_IDS)('does not issue a request when the conversation id is not well-formed: $name', async ({id}) => {
        await expect(getConversationContext(id)).rejects.toThrow();

        expect(mockFetch).not.toHaveBeenCalled();
    });
});
