// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {PolicyResourceType} from '@/types/access_control';

import manifest from '@/manifest';

import {
    checkAccessControlExpression,
    getAccessControlVisualAST,
    testAccessControlExpression,
} from './access_control';

jest.mock('@mattermost/client', () => ({

    // client.tsx constructs `new Client4()`; the mocked class exposes instance methods.
    Client4: class Client4 {
        url = '';

        setUrl(url: string) {
            this.url = url;
        }

        getOptions(options: Record<string, unknown>) {
            return {...options, headers: {'X-Requested-With': 'XMLHttpRequest'}};
        }
    },
    ClientError: class extends Error {},
}));

const mockFetch = jest.fn<Promise<Response>, [string, RequestInit]>();
global.fetch = mockFetch as unknown as typeof fetch;

function okResponse(): Response {
    return {ok: true, status: 200, json: () => Promise.resolve({})} as unknown as Response;
}

// requestBody parses the body the client actually put on the wire.
function requestBody(): Record<string, unknown> {
    expect(mockFetch).toHaveBeenCalledTimes(1);
    const [, options] = mockFetch.mock.calls[0];
    return JSON.parse(options.body as string);
}

beforeEach(() => {
    mockFetch.mockReset();
    mockFetch.mockResolvedValue(okResponse());
});

// The server keys plugin-owned policy types "<pluginID>:<resourceType>" and
// rejects anything else, so these strings are a wire contract: every CEL route
// that carries one is asserted at the transport level.
describe('CEL clients send colon-keyed plugin-owned resource types', () => {
    const resourceTypes: Array<{resourceType: PolicyResourceType; want: string}> = [
        {resourceType: 'agent', want: `${manifest.id}:agent`},
        {resourceType: 'service', want: `${manifest.id}:service`},
        {resourceType: 'mcp', want: `${manifest.id}:mcp`},
    ];

    const callers: Array<{
        name: string;
        call: (resourceType: PolicyResourceType) => Promise<unknown>;
    }> = [
        {
            name: 'checkAccessControlExpression',
            call: (resourceType) => checkAccessControlExpression(resourceType, 'true'),
        },
        {
            name: 'testAccessControlExpression',
            call: (resourceType) => testAccessControlExpression(resourceType, 'true', '', '', 10),
        },
        {
            name: 'getAccessControlVisualAST',
            call: (resourceType) => getAccessControlVisualAST(resourceType, 'true'),
        },
    ];

    describe.each(callers)('$name', ({call}) => {
        test.each(resourceTypes)('sends $want for the $resourceType resource type', async ({resourceType, want}) => {
            await call(resourceType);

            expect(requestBody().resource_type).toBe(want);
        });
    });

    // Pin the literal too: composing from the manifest is only correct while the
    // manifest id is the plugin id the server checks ownership against.
    test('the composed prefix is the mattermost-ai plugin id', () => {
        expect(manifest.id).toBe('mattermost-ai');
    });
});

describe('testAccessControlExpression', () => {
    test('normalizes null users to an empty array', async () => {
        mockFetch.mockResolvedValue({
            ok: true,
            status: 200,
            json: () => Promise.resolve({users: null, total: 0}),
        } as unknown as Response);

        await expect(testAccessControlExpression('service', 'true', '', '', 10)).resolves.toEqual({
            users: [],
            total: 0,
        });
    });
});
