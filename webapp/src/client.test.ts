// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {ChannelSearchOpts, ChannelWithTeamData} from '@mattermost/types/channels';
import type {OptsSignalExt} from '@mattermost/types/client4';

import type {ConversationResponse, Turn} from '@/types/conversation';
import type {CreateAgentRequest, UpdateAgentRequest} from '@/types/agents';

import {createAgent, normalizeConversationResponse, searchAllChannels, setSiteURL, updateAgent} from './client';

type SearchAllChannelsOpts = Omit<ChannelSearchOpts, 'page' | 'per_page'> & OptsSignalExt;

jest.mock('@mattermost/client', () => {
    const mockSearchAllChannels = jest.fn<
        Promise<ChannelWithTeamData[]>,
        [string, SearchAllChannelsOpts | undefined]
    >();

    class MockClientError extends Error {
        status_code?: number;
        url?: string;

        constructor(_baseUrl: string, details: {message?: string; status_code?: number; url?: string}) {
            super(details.message || '');
            this.name = 'ClientError';
            this.status_code = details.status_code;
            this.url = details.url;
        }
    }

    return {

        // client.tsx constructs `new Client4()`; the mocked class exposes instance methods.
        Client4: class Client4 {
            setUrl = jest.fn();
            searchAllChannels = mockSearchAllChannels;
            getOptions = (options: RequestInit) => options;
            url = 'http://localhost';
        },
        ClientError: MockClientError,
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

describe('agent save errors', () => {
    const mockFetch = jest.fn();
    const createPayload: CreateAgentRequest = {
        displayName: 'Agent',
        username: 'agent',
        serviceID: 'svc-1',
        autoEnableNewMCPTools: true,
    };
    const updatePayload: UpdateAgentRequest = {
        displayName: 'Agent',
        username: 'agent',
        serviceID: 'svc-1',
        autoEnableNewMCPTools: true,
    };

    beforeEach(() => {
        mockSearchAllChannels.mockReset();
        mockFetch.mockReset();
        global.fetch = mockFetch as unknown as typeof fetch;
        setSiteURL('http://localhost');
    });

    test('createAgent preserves JSON error messages for actionable failures', async () => {
        mockFetch.mockResolvedValue({
            ok: false,
            status: 400,
            text: jest.fn().mockResolvedValue(JSON.stringify({error: 'service "deleted" not found in configuration'})),
        });

        await expect(createAgent(createPayload)).rejects.toMatchObject({
            message: 'service "deleted" not found in configuration',
            status_code: 400,
        });
    });

    test('updateAgent preserves plain-text error messages when JSON is unavailable', async () => {
        mockFetch.mockResolvedValue({
            ok: false,
            status: 403,
            text: jest.fn().mockResolvedValue('creating more than 1 self-service agent(s) requires an E20 or Enterprise license'),
        });

        await expect(updateAgent('agent-id', updatePayload)).rejects.toMatchObject({
            message: 'creating more than 1 self-service agent(s) requires an E20 or Enterprise license',
            status_code: 403,
        });
    });
});
