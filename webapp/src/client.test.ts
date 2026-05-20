// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {ChannelSearchOpts, ChannelWithTeamData} from '@mattermost/types/channels';
import type {OptsSignalExt} from '@mattermost/types/client4';

import type {ConversationResponse, Turn} from '@/types/conversation';

import {normalizeConversationResponse, reindexClientError, searchAllChannels} from './client';

type SearchAllChannelsOpts = Omit<ChannelSearchOpts, 'page' | 'per_page'> & OptsSignalExt;

jest.mock('@mattermost/client', () => {
    const mockSearchAllChannels = jest.fn<
        Promise<ChannelWithTeamData[]>,
        [string, SearchAllChannelsOpts | undefined]
    >();

    return {

        // client.tsx constructs `new Client4()`; the mocked class exposes instance methods.
        Client4: class Client4 {
            searchAllChannels = mockSearchAllChannels;
        },

        // Mirror the real ClientError shape so reindexClientError's status_code
        // and message propagate the way the UI handlers expect.
        ClientError: class extends Error {
            url: string;
            status_code: number;
            constructor(baseUrl: string, opts: {message: string; status_code: number; url: string}) {
                super(opts.message);
                this.url = opts.url;
                this.status_code = opts.status_code;
            }
        },
        mockSearchAllChannels,
    };
});

const {mockSearchAllChannels} = jest.requireMock('@mattermost/client') as {
    mockSearchAllChannels: jest.MockedFunction<
        (term: string, opts?: SearchAllChannelsOpts) => Promise<ChannelWithTeamData[]>
    >;
};

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

// Helper for building Response objects without depending on jsdom's whatwg-fetch.
function makeResponse(status: number, statusText: string, body: string): Response {
    return {
        status,
        statusText,
        ok: status >= 200 && status < 300,
        text: () => Promise.resolve(body),
    } as Response;
}

describe('reindexClientError', () => {
    test('uses the server `error` field as the ClientError message', async () => {
        const resp = makeResponse(400, 'Bad Request', JSON.stringify({error: 'embedding search is not initialized'}));
        const err = await reindexClientError(resp, '/admin/reindex');

        expect(err).toBeInstanceOf(Error);
        expect(err.message).toBe('embedding search is not initialized');
        expect(err.status_code).toBe(400);
        expect(err.url).toBe('/admin/reindex');
    });

    test('attaches job_status when the 409 response includes one', async () => {
        const jobStatus = {job_id: 'abc', status: 'running', processed_rows: 17};
        const resp = makeResponse(
            409,
            'Conflict',
            JSON.stringify({error: 'A reindex job is already running.', job_status: jobStatus}),
        );
        const err = await reindexClientError(resp, '/admin/reindex') as Error & {
            status_code: number;
            job_status?: unknown;
        };

        expect(err.status_code).toBe(409);
        expect(err.message).toBe('A reindex job is already running.');
        expect(err.job_status).toEqual(jobStatus);
    });

    test('falls back to the HTTP status text when the body is not JSON', async () => {
        const resp = makeResponse(502, 'Bad Gateway', '<html>upstream error</html>');
        const err = await reindexClientError(resp, '/admin/reindex');

        expect(err.message).toBe('502 Bad Gateway');
        expect(err.status_code).toBe(502);
    });

    test('falls back to the HTTP status text when the body is empty', async () => {
        const resp = makeResponse(401, 'Unauthorized', '');
        const err = await reindexClientError(resp, '/admin/reindex');

        expect(err.message).toBe('401 Unauthorized');
        expect(err.status_code).toBe(401);
    });

    test('does not attach job_status when the response omits it', async () => {
        const resp = makeResponse(500, 'Internal Server Error', JSON.stringify({error: 'boom'}));
        const err = await reindexClientError(resp, '/admin/reindex') as Error & {job_status?: unknown};

        expect('job_status' in err).toBe(false);
    });

    // Regression: JSON.parse can return any valid JSON value, including
    // primitives. An earlier draft used `'job_status' in parsed`, which throws
    // TypeError when parsed is null/number/string.
    test.each([
        ['null', 'null'],
        ['a JSON number', '42'],
        ['a JSON string', '"oops"'],
        ['a JSON boolean', 'true'],
        ['a JSON array', '[1,2,3]'],
    ])('does not throw when the body is %s', async (_label, body) => {
        const resp = makeResponse(500, 'Internal Server Error', body);
        const err = await reindexClientError(resp, '/admin/reindex') as Error & {
            status_code: number;
            job_status?: unknown;
        };

        expect(err.status_code).toBe(500);
        expect(err.message).toBe('500 Internal Server Error');
        expect('job_status' in err).toBe(false);
    });
});
