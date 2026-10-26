// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {ChannelSearchOpts, ChannelWithTeamData} from '@mattermost/types/channels';
import type {OptsSignalExt} from '@mattermost/types/client4';

import type {ConversationResponse, Turn} from '@/types/conversation';

import manifest from './manifest';

import {
    artifactPath,
    artifactURL,
    doChannelAnalysis,
    doLoopInAgent,
    doRunSearch,
    doSetPostViewing,
    doThreadAnalysis,
    deleteAgentVersions,
    getAgentVersion,
    getAgentVersions,
    restoreAgentVersion,
    getArtifactToken,
    getChannelAutoReply,
    getChannelInterval,
    getConversation,
    getConversationContext,
    normalizeConversationResponse,
    renderCustomPrompt,
    searchAllChannels,
    setSiteURL,
    updateChannelAutoReply,
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

        // Carries status_code like the real ClientError so callers can assert on it.
        ClientError: class extends Error {
            status_code?: number;
            url?: string;

            constructor(baseUrl: string, data?: {message?: string; status_code?: number; url?: string}) {
                super(data?.message ?? '');
                this.status_code = data?.status_code;
                this.url = data?.url;
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

// Mattermost IDs are 26 characters of lowercase letters and digits.
const WELL_FORMED_ID = 'c7f2m9xq4v1b8n3k6t5w0hzjd2';
const BOT_ID = 'b7m2xq9d4wn1c6zr3t8kv5jhya';

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
        await expect(doLoopInAgent(WELL_FORMED_ID, BOT_ID)).resolves.toBeUndefined();

        expect(mockFetch).toHaveBeenCalledTimes(1);
        const [url, options] = mockFetch.mock.calls[0];
        expect(url).toBe(`${siteURL}/plugins/${manifest.id}/post/${WELL_FORMED_ID}/loop_in_agent?botId=${BOT_ID}`);
        expect(options).toEqual(expect.objectContaining({method: 'POST'}));
    });

    test('percent-encodes the bot id in the query string', async () => {
        await doLoopInAgent(WELL_FORMED_ID, 'agent bot&x=1');

        const [url] = mockFetch.mock.calls[0];
        expect(url).toBe(`${siteURL}/plugins/${manifest.id}/post/${WELL_FORMED_ID}/loop_in_agent?botId=agent%20bot%26x%3D1`);
    });

    test.each(NOT_WELL_FORMED_IDS)('does not issue a request when the post id is not well-formed: $name', async ({id}) => {
        await expect(doLoopInAgent(id, BOT_ID)).rejects.toThrow();

        expect(mockFetch).not.toHaveBeenCalled();
    });
});

describe('doSetPostViewing', () => {
    test.each([
        {name: 'viewing', viewing: true, keepalive: false},
        {name: 'no longer viewing while the page unloads', viewing: false, keepalive: true},
    ])('posts the viewing state: $name', async ({viewing, keepalive}) => {
        await expect(doSetPostViewing(WELL_FORMED_ID, viewing, keepalive)).resolves.toBeUndefined();

        expect(mockFetch).toHaveBeenCalledTimes(1);
        const [url, options] = mockFetch.mock.calls[0];
        expect(url).toBe(`${siteURL}/plugins/${manifest.id}/post/${WELL_FORMED_ID}/viewing`);
        expect(options).toEqual(expect.objectContaining({method: 'POST', keepalive}));
        expect(JSON.parse(String(options.body))).toEqual({viewing});
    });

    test('rejects when the server refuses the report', async () => {
        mockFetch.mockResolvedValue({ok: false, status: 400, json: jest.fn()} as unknown as Response);

        await expect(doSetPostViewing(WELL_FORMED_ID, true)).rejects.toMatchObject({status_code: 400});
    });
});

describe('agent selection by bot id', () => {
    const base = `${siteURL}/plugins/${manifest.id}`;
    const call = async (fn: () => Promise<unknown>) => {
        mockFetch.mockReset();
        mockFetch.mockResolvedValue(okResponse());
        await fn();
        expect(mockFetch).toHaveBeenCalledTimes(1);
        return mockFetch.mock.calls[0];
    };

    test.each([
        {name: 'doThreadAnalysis', run: () => doThreadAnalysis(WELL_FORMED_ID, 'summarize_thread', BOT_ID), path: `/post/${WELL_FORMED_ID}/analyze`},
        {name: 'doChannelAnalysis', run: () => doChannelAnalysis(WELL_FORMED_ID, 'summarize_channel', BOT_ID), path: `/channel/${WELL_FORMED_ID}/analyze`},
        {name: 'doRunSearch', run: () => doRunSearch('q', 'team', 'channel', BOT_ID), path: '/search/run'},
        {name: 'getChannelInterval', run: () => getChannelInterval(WELL_FORMED_ID, 1, 2, 'summarize_range', '', BOT_ID), path: `/channel/${WELL_FORMED_ID}/interval`},
    ])('$name sends botId and never botUsername', async ({run, path}) => {
        const [url] = await call(run);
        expect(url).toBe(`${base}${path}?botId=${BOT_ID}`);
        expect(url).not.toContain('botUsername');
    });

    test.each([
        {name: 'doThreadAnalysis', run: () => doThreadAnalysis(WELL_FORMED_ID, 'summarize_thread', ''), path: `/post/${WELL_FORMED_ID}/analyze`},
        {name: 'doChannelAnalysis', run: () => doChannelAnalysis(WELL_FORMED_ID, 'summarize_channel', ''), path: `/channel/${WELL_FORMED_ID}/analyze`},
        {name: 'doRunSearch', run: () => doRunSearch('q', 'team', 'channel'), path: '/search/run'},
        {name: 'getChannelInterval', run: () => getChannelInterval(WELL_FORMED_ID, 1, 2, 'summarize_range'), path: `/channel/${WELL_FORMED_ID}/interval`},
    ])('$name omits the agent query when no bot id is given', async ({run, path}) => {
        const [url] = await call(run);
        expect(url).toBe(`${base}${path}`);
    });

    test.each([
        {name: 'a typed username', botId: '', botUsername: 'matty', query: '?botUsername=matty'},
        {name: 'a typed username with reserved characters', botId: '', botUsername: 'a&b', query: '?botUsername=a%26b'},
        {name: 'a bot id over a typed username', botId: BOT_ID, botUsername: 'matty', query: `?botId=${BOT_ID}`},
    ])('getChannelInterval sends $name', async ({botId, botUsername, query}) => {
        const [url] = await call(() => getChannelInterval(WELL_FORMED_ID, 1, 2, 'summarize_range', '', botId, botUsername));
        expect(url).toBe(`${base}/channel/${WELL_FORMED_ID}/interval${query}`);
    });

    test('renderCustomPrompt sends bot_id and never bot_username', async () => {
        const [url, options] = await call(() => renderCustomPrompt('prompt1', WELL_FORMED_ID, BOT_ID));
        expect(url).toBe(`${base}/custom-prompts/prompt1/render`);
        expect(JSON.parse(options.body as string)).toEqual({channel_id: WELL_FORMED_ID, bot_id: BOT_ID});
    });
});

describe('getChannelAutoReply', () => {
    test('issues a GET to the channel autoreply route and returns the parsed body', async () => {
        const body = {bot_id: 'bot-1', mode: 'threads'};
        mockFetch.mockResolvedValue({ok: true, status: 200, json: () => Promise.resolve(body)} as unknown as Response);

        await expect(getChannelAutoReply('channel-1')).resolves.toEqual(body);

        expect(mockFetch).toHaveBeenCalledTimes(1);
        const [url, options] = mockFetch.mock.calls[0];
        expect(url).toBe(`${siteURL}/plugins/${manifest.id}/channel/channel-1/autoreply`);
        expect(options).toEqual(expect.objectContaining({method: 'GET'}));
    });

    test('percent-encodes the channel id so it occupies a single path segment', async () => {
        mockFetch.mockResolvedValue({ok: true, status: 200, json: () => Promise.resolve({bot_id: '', mode: 'off'})} as unknown as Response);

        await getChannelAutoReply('cha/nnel');

        const [url] = mockFetch.mock.calls[0];
        expect(url).toBe(`${siteURL}/plugins/${manifest.id}/channel/cha%2Fnnel/autoreply`);
    });

    test.each([403, 500])('throws an error carrying status %d on a non-ok response', async (status) => {
        mockFetch.mockResolvedValue({ok: false, status, json: jest.fn()} as unknown as Response);

        await expect(getChannelAutoReply('channel-1')).rejects.toMatchObject({status_code: status});
    });
});

describe('updateChannelAutoReply', () => {
    test('issues a PUT with exactly the settings payload and resolves without reading the body', async () => {
        const json = jest.fn();
        mockFetch.mockResolvedValue({ok: true, status: 200, json} as unknown as Response);

        await expect(updateChannelAutoReply('channel-1', {bot_id: 'bot-1', mode: 'root_posts'})).resolves.toBeUndefined();

        expect(mockFetch).toHaveBeenCalledTimes(1);
        const [url, options] = mockFetch.mock.calls[0];
        expect(url).toBe(`${siteURL}/plugins/${manifest.id}/channel/channel-1/autoreply`);
        expect(options).toEqual(expect.objectContaining({
            method: 'PUT',
            body: JSON.stringify({bot_id: 'bot-1', mode: 'root_posts'}),
        }));
        expect(json).not.toHaveBeenCalled();
    });

    test.each([403, 413, 500])('throws an error carrying status %d on a non-ok response', async (status) => {
        mockFetch.mockResolvedValue({ok: false, status, json: jest.fn()} as unknown as Response);

        await expect(updateChannelAutoReply('channel-1', {bot_id: '', mode: 'off'})).rejects.toMatchObject({status_code: status});
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

describe('artifact URLs', () => {
    const artifactsRoute = `${siteURL}/plugins/${manifest.id}/artifacts`;
    const nonce = 'AbC-d_0123456789xyzABC';

    test.each([
        {name: 'well-formed id', fileId: WELL_FORMED_ID, nonce, path: `${artifactsRoute}/${WELL_FORMED_ID}`},
        {name: 'id with a separator stays one path segment', fileId: 'ab/../cd', nonce, path: `${artifactsRoute}/ab%2F..%2Fcd`},
        {name: 'nonce with query syntax stays one parameter value', fileId: WELL_FORMED_ID, nonce: 'a&n=b#c', path: `${artifactsRoute}/${WELL_FORMED_ID}`},
    ])('$name', async ({fileId, nonce: n, path}) => {
        const encodedNonce = encodeURIComponent(n);
        expect(artifactPath(fileId)).toBe(path);
        expect(artifactURL(fileId, n)).toBe(`${path}?n=${encodedNonce}`);

        mockFetch.mockResolvedValue({ok: true, status: 200, json: () => Promise.resolve({token: 'tok', tooLarge: false})} as unknown as Response);
        await expect(getArtifactToken(fileId, n)).resolves.toEqual({token: 'tok', tooLarge: false});
        const [url, options] = mockFetch.mock.calls[0];
        expect(url).toBe(`${path}/token?n=${encodedNonce}`);
        expect(options).toEqual(expect.objectContaining({method: 'GET'}));
    });
});

describe('license denial errors', () => {
    test('403 responses surface the server error text', async () => {
        mockFetch.mockResolvedValue({
            ok: false,
            status: 403,
            json: () => Promise.resolve({
                error: 'Thread summarization is available on Professional plans and above.',
                license_required: 'professional',
            }),
        } as unknown as Response);

        await expect(doThreadAnalysis('post-1', 'summarize_thread', 'bot')).rejects.toMatchObject({
            status_code: 403,
            message: 'Thread summarization is available on Professional plans and above.',
        });
    });

    test('non-403 responses keep an empty message', async () => {
        mockFetch.mockResolvedValue({
            ok: false,
            status: 500,
            json: () => Promise.resolve({error: 'internal'}),
        } as unknown as Response);

        await expect(doThreadAnalysis('post-1', 'summarize_thread', 'bot')).rejects.toMatchObject({
            status_code: 500,
            message: '',
        });
    });
});

describe('agent versioning client', () => {
    const agentBase = `${siteURL}/plugins/${manifest.id}/agents`;

    function jsonResponse(body: unknown, init: {ok?: boolean; status?: number} = {}): Response {
        return {
            ok: init.ok ?? true,
            status: init.status ?? 200,
            json: () => Promise.resolve(body),
        } as unknown as Response;
    }

    test.each([
        {name: 'list versions', call: () => getAgentVersions('a1'), url: `${agentBase}/a1/versions`, method: 'GET'},
        {name: 'list older versions', call: () => getAgentVersions('a1', {before: 51}), url: `${agentBase}/a1/versions?before=51`, method: 'GET'},
        {name: 'list a page of versions', call: () => getAgentVersions('a1', {before: 51, limit: 20}), url: `${agentBase}/a1/versions?before=51&limit=20`, method: 'GET'},
        {name: 'get one version', call: () => getAgentVersion('a1', 3), url: `${agentBase}/a1/versions/3`, method: 'GET'},
        {name: 'restore a version', call: () => restoreAgentVersion('a1', 3), url: `${agentBase}/a1/versions/3/restore`, method: 'POST'},
        {name: 'delete versions', call: () => deleteAgentVersions('a1', [1, 2]), url: `${agentBase}/a1/versions`, method: 'DELETE'},
    ])('$name uses the contract route', async ({call, url, method}) => {
        mockFetch.mockResolvedValue(jsonResponse({}));

        await call();

        const [calledUrl, options] = mockFetch.mock.calls[0];
        expect(calledUrl).toBe(url);
        expect(options).toEqual(expect.objectContaining({method}));
    });

    test.each([
        {name: 'selected versions', versions: [1, 4] as number[] | 'all', body: {versions: [1, 4]}},
        {name: 'every previous version', versions: 'all' as number[] | 'all', body: {all: true}},
    ])('deleteAgentVersions sends $name and returns the count', async ({versions, body}) => {
        mockFetch.mockResolvedValue(jsonResponse({deleted: 2}));

        await expect(deleteAgentVersions('a1', versions)).resolves.toBe(2);
        expect(JSON.parse(mockFetch.mock.calls[0][1].body as string)).toEqual(body);
    });

    test.each([
        {name: 'restore', call: () => restoreAgentVersion('a1', 3)},
        {name: 'delete', call: () => deleteAgentVersions('a1', [3])},
        {name: 'list versions', call: () => getAgentVersions('a1')},
    ])('$name surfaces the server error message and status', async ({call}) => {
        mockFetch.mockResolvedValue(jsonResponse({error: 'the current version of an agent cannot be deleted'}, {ok: false, status: 400}));

        await expect(call()).rejects.toMatchObject({
            status_code: 400,
            message: 'the current version of an agent cannot be deleted',
        });
    });
});
