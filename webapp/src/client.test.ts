// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {ChannelSearchOpts, ChannelWithTeamData} from '@mattermost/types/channels';
import type {OptsSignalExt} from '@mattermost/types/client4';

import type {AgentExportDocument} from '@/types/agents';
import type {ConversationResponse, Turn} from '@/types/conversation';

import manifest from './manifest';

import {
    agentDocumentUrl,
    doLoopInAgent,
    doThreadAnalysis,
    downloadAgentDocument,
    exportAgent,
    getAgentVersion,
    getAgentVersions,
    importAgent,
    parseContentDispositionFilename,
    previewAgentImport,
    restoreAgentVersion,
    uploadAgentDocument,
    getChannelAutoReply,
    getConversation,
    getConversationContext,
    normalizeConversationResponse,
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

describe('agent versioning and import/export client', () => {
    const agentBase = `${siteURL}/plugins/${manifest.id}/agents`;
    const document: AgentExportDocument = {
        kind: 'mattermost-agent',
        schemaVersion: 1,
        exportedAt: 1,
        agentVersion: 2,
        agent: {
            name: 'helper',
            displayName: 'Helper',
            customInstructions: '',
            disableTools: false,
            maxToolTurns: 30,
            mcpDynamicToolLoading: true,
            autoEnableNewMCPTools: false,
            mcpTools: [],
        },
    };

    function jsonResponse(body: unknown, init: {ok?: boolean; status?: number; headers?: Record<string, string>} = {}): Response {
        return {
            ok: init.ok ?? true,
            status: init.status ?? 200,
            json: () => Promise.resolve(body),
            blob: () => Promise.resolve(new Blob([JSON.stringify(body)])),
            headers: {get: (name: string) => init.headers?.[name] ?? null},
        } as unknown as Response;
    }

    test.each([
        {name: 'list versions', call: () => getAgentVersions('a1'), url: `${agentBase}/a1/versions`, method: 'GET'},
        {name: 'get one version', call: () => getAgentVersion('a1', 3), url: `${agentBase}/a1/versions/3`, method: 'GET'},
        {name: 'restore a version', call: () => restoreAgentVersion('a1', 3), url: `${agentBase}/a1/versions/3/restore`, method: 'POST'},
        {name: 'preview an import', call: () => previewAgentImport(document), url: `${agentBase}/import/preview`, method: 'POST'},
        {name: 'import an agent', call: () => importAgent({document, mode: 'update', agentID: 'a1', mcpServerMappings: []}), url: `${agentBase}/import`, method: 'POST'},
    ])('$name uses the contract route', async ({call, url, method}) => {
        mockFetch.mockResolvedValue(jsonResponse({}));

        await call();

        const [calledUrl, options] = mockFetch.mock.calls[0];
        expect(calledUrl).toBe(url);
        expect(options).toEqual(expect.objectContaining({method}));
    });

    test('preview wraps the document and import sends the request as-is', async () => {
        await previewAgentImport(document);
        expect(JSON.parse(mockFetch.mock.calls[0][1].body as string)).toEqual({document});

        const request = {
            document,
            mode: 'create' as const,
            username: 'helper',
            displayName: 'Helper',
            serviceID: 'svc',
            model: '',
            mcpServerMappings: [{sourceOrigin: 'https://a.example/mcp', targetOrigin: ''}],
        };
        await importAgent(request);
        expect(JSON.parse(mockFetch.mock.calls[1][1].body as string)).toEqual(request);
    });

    test.each([
        {name: 'restore', call: () => restoreAgentVersion('a1', 3)},
        {name: 'import', call: () => importAgent({document, mode: 'update', agentID: 'a1', mcpServerMappings: []})},
        {name: 'preview', call: () => previewAgentImport(document)},
        {name: 'export', call: () => exportAgent('a1')},
        {name: 'list versions', call: () => getAgentVersions('a1')},
    ])('$name surfaces the server error message and status', async ({call}) => {
        mockFetch.mockResolvedValue(jsonResponse({error: 'MCP server "Jira" must be mapped'}, {ok: false, status: 400}));

        await expect(call()).rejects.toMatchObject({
            status_code: 400,
            message: 'MCP server "Jira" must be mapped',
        });
    });

    test('exportAgent returns the blob and the Content-Disposition filename', async () => {
        mockFetch.mockResolvedValue(jsonResponse(document, {
            headers: {'Content-Disposition': 'attachment; filename="helper-v2.agent.json"'},
        }));

        const result = await exportAgent('a1');

        expect(mockFetch.mock.calls[0][0]).toBe(`${agentBase}/a1/export`);
        expect(result.filename).toBe('helper-v2.agent.json');
        expect(result.blob).toBeInstanceOf(Blob);
    });

    test.each([
        {header: null, expected: null},
        {header: 'attachment', expected: null},
        {header: 'attachment; filename="a-v1.agent.json"', expected: 'a-v1.agent.json'},
        {header: 'attachment; filename=a-v1.agent.json', expected: 'a-v1.agent.json'},
        {header: "attachment; filename*=UTF-8''caf%C3%A9.agent.json", expected: 'café.agent.json'},
    ])('parseContentDispositionFilename($header)', ({header, expected}) => {
        expect(parseContentDispositionFilename(header)).toBe(expected);
    });

    test('uploadAgentDocument posts the file as multipart form data without a JSON content type', async () => {
        const uploaded = {id: 'doc1', name: 'handbook.pdf', mimeType: 'application/pdf', size: 3, sha256: 'abc', textRunes: 10};
        mockFetch.mockResolvedValue(jsonResponse(uploaded, {status: 201}));
        const file = new File(['abc'], 'handbook.pdf', {type: 'application/pdf'});

        await expect(uploadAgentDocument(file)).resolves.toEqual(uploaded);

        const [calledUrl, options] = mockFetch.mock.calls[0];
        expect(calledUrl).toBe(`${agentBase}/documents`);
        expect(options.method).toBe('POST');
        expect(options.body).toBeInstanceOf(FormData);
        expect((options.body as FormData).get('file')).toBe(file);
        expect(options.headers).toEqual({'X-Requested-With': 'XMLHttpRequest'});
    });

    test('uploadAgentDocument surfaces the server error message and status', async () => {
        mockFetch.mockResolvedValue(jsonResponse({error: 'no extractable text found in "scan.pdf"'}, {ok: false, status: 400}));

        await expect(uploadAgentDocument(new File(['x'], 'scan.pdf'))).rejects.toMatchObject({
            status_code: 400,
            message: 'no extractable text found in "scan.pdf"',
        });
    });

    test('downloadAgentDocument returns the blob and Content-Disposition filename from the document route', async () => {
        mockFetch.mockResolvedValue(jsonResponse({}, {
            headers: {'Content-Disposition': "attachment; filename*=UTF-8''caf%C3%A9.pdf"},
        }));

        const result = await downloadAgentDocument('a1', 'doc1');

        expect(agentDocumentUrl('a1', 'doc1')).toBe(`${agentBase}/a1/documents/doc1`);
        expect(mockFetch.mock.calls[0][0]).toBe(`${agentBase}/a1/documents/doc1`);
        expect(result.filename).toBe('café.pdf');
        expect(result.blob).toBeInstanceOf(Blob);
    });
});
