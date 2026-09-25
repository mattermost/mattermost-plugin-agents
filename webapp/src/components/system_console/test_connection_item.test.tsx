// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';

// Minimal react-intl shim — see mcp_tools_viewer.test.tsx for rationale.
jest.mock('react-intl', () => {
    const React = require('react'); // eslint-disable-line @typescript-eslint/no-shadow, no-shadow, global-require

    return {
        __esModule: true,
        IntlProvider: ({children}: {children: React.ReactNode}) => React.createElement(React.Fragment, null, children),
        FormattedMessage: ({defaultMessage}: {defaultMessage?: string}) =>
            React.createElement(React.Fragment, null, defaultMessage ?? ''),
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage?: string}) => defaultMessage ?? '',
        }),
    };
});

jest.mock('@/client', () => ({
    testService: jest.fn(),
}));

/* eslint-disable import/first */
import {testService} from '@/client';

import type {LLMService} from './service';
import {TestConnectionItem} from './test_connection_item';
/* eslint-enable import/first */

const service: LLMService = {
    id: 'svc-1',
    name: 'OpenAI',
    type: 'openai',
    apiURL: '',
    apiKey: 'key-1',
    orgId: '',
    defaultModel: 'gpt-5.2',
    tokenLimit: 0,
    streamingTimeoutSeconds: 0,
    outputTokenLimit: 0,
    useResponsesAPI: true,
    region: '',
    awsAccessKeyID: '',
    awsSecretAccessKey: '',
    vertexProjectID: '',
    vertexProjectNumber: '',
    vertexAuthCredentials: '',
};

describe('TestConnectionItem', () => {
    beforeEach(() => {
        jest.clearAllMocks();
    });

    it('reports success when the provider answers', async () => {
        (testService as jest.Mock).mockResolvedValue({ok: true});
        render(<TestConnectionItem service={service}/>);

        fireEvent.click(screen.getByText('Test connection'));

        expect(await screen.findByText('Connection successful')).toBeTruthy();
        expect(testService).toHaveBeenCalledWith(service);
    });

    it("shows the provider's own message when the probe fails", async () => {
        (testService as jest.Mock).mockResolvedValue({ok: false, error: 'invalid x-api-key'});
        render(<TestConnectionItem service={service}/>);

        fireEvent.click(screen.getByText('Test connection'));

        // The provider's wording is the point of the feature; a generic
        // "test failed" would not tell the admin what to fix.
        expect(await screen.findByText('invalid x-api-key')).toBeTruthy();
    });

    it('falls back to a generic message when a failure carries no detail', async () => {
        (testService as jest.Mock).mockResolvedValue({ok: false});
        render(<TestConnectionItem service={service}/>);

        fireEvent.click(screen.getByText('Test connection'));

        expect(await screen.findByText('The provider did not accept the request.')).toBeTruthy();
    });

    it('reports a transport failure distinctly from a provider rejection', async () => {
        (testService as jest.Mock).mockRejectedValue(new Error('500'));
        render(<TestConnectionItem service={service}/>);

        fireEvent.click(screen.getByText('Test connection'));

        expect(await screen.findByText('Could not reach the server to run the test.')).toBeTruthy();
    });

    it('clears a stale result when the credentials change', async () => {
        (testService as jest.Mock).mockResolvedValue({ok: true});
        const {rerender} = render(<TestConnectionItem service={service}/>);

        fireEvent.click(screen.getByText('Test connection'));
        expect(await screen.findByText('Connection successful')).toBeTruthy();

        // A success next to a key the admin has since edited would be a lie
        // about which configuration was verified.
        rerender(<TestConnectionItem service={{...service, apiKey: 'key-2'}}/>);

        await waitFor(() => {
            expect(screen.queryByText('Connection successful')).toBeNull();
        });
    });

    it('keeps a result when a field unrelated to reachability changes', async () => {
        (testService as jest.Mock).mockResolvedValue({ok: true});
        const {rerender} = render(<TestConnectionItem service={service}/>);

        fireEvent.click(screen.getByText('Test connection'));
        expect(await screen.findByText('Connection successful')).toBeTruthy();

        rerender(<TestConnectionItem service={{...service, name: 'Renamed'}}/>);

        expect(screen.getByText('Connection successful')).toBeTruthy();
    });

    it('discards a result that arrives after the credentials changed', async () => {
        let resolveTest!: (value: {ok: boolean}) => void;
        (testService as jest.Mock).mockReturnValue(new Promise<{ok: boolean}>((resolve) => {
            resolveTest = resolve;
        }));
        const {rerender} = render(<TestConnectionItem service={service}/>);

        fireEvent.click(screen.getByText('Test connection'));
        expect(await screen.findByText('Testing...')).toBeTruthy();

        // The admin fixes a typo while the probe is still out. Its answer
        // describes the old key, so reporting success here would vouch for a
        // configuration that was never tested.
        rerender(<TestConnectionItem service={{...service, apiKey: 'key-2'}}/>);
        resolveTest({ok: true});

        await waitFor(() => {
            expect(screen.getByText('Test connection')).toBeTruthy();
        });
        expect(screen.queryByText('Connection successful')).toBeNull();
    });

    it('discards a failure that arrives after the credentials changed', async () => {
        let rejectTest!: (reason: Error) => void;
        (testService as jest.Mock).mockReturnValue(new Promise<{ok: boolean}>((_resolve, reject) => {
            rejectTest = reject;
        }));
        const {rerender} = render(<TestConnectionItem service={service}/>);

        fireEvent.click(screen.getByText('Test connection'));
        expect(await screen.findByText('Testing...')).toBeTruthy();

        rerender(<TestConnectionItem service={{...service, apiKey: 'key-2'}}/>);
        rejectTest(new Error('500'));

        await waitFor(() => {
            expect(screen.getByText('Test connection')).toBeTruthy();
        });
        expect(screen.queryByText('Could not reach the server to run the test.')).toBeNull();
    });

    it('applies only the newest result when an older probe answers last', async () => {
        const resolvers: Array<(value: {ok: boolean; error?: string}) => void> = [];
        (testService as jest.Mock).mockImplementation(() => new Promise<{ok: boolean; error?: string}>((resolve) => {
            resolvers.push(resolve);
        }));
        const {rerender} = render(<TestConnectionItem service={service}/>);

        fireEvent.click(screen.getByText('Test connection'));
        expect(await screen.findByText('Testing...')).toBeTruthy();

        // Changing the key re-enables the button, so a second probe can be in
        // flight alongside the first.
        rerender(<TestConnectionItem service={{...service, apiKey: 'key-2'}}/>);
        fireEvent.click(await screen.findByText('Test connection'));

        resolvers[1]({ok: false, error: 'invalid x-api-key'});
        expect(await screen.findByText('invalid x-api-key')).toBeTruthy();

        // The first probe lands last and must not overwrite the newer answer.
        resolvers[0]({ok: true});

        await waitFor(() => {
            expect(screen.getByText('invalid x-api-key')).toBeTruthy();
        });
        expect(screen.queryByText('Connection successful')).toBeNull();
    });

    it('disables the button while a test is in flight', async () => {
        let resolveTest!: (value: {ok: boolean}) => void;
        (testService as jest.Mock).mockReturnValue(new Promise<{ok: boolean}>((resolve) => {
            resolveTest = resolve;
        }));
        render(<TestConnectionItem service={service}/>);

        fireEvent.click(screen.getByText('Test connection'));

        const button = await screen.findByText('Testing...');
        expect((button as HTMLButtonElement).disabled).toBe(true);

        resolveTest({ok: true});
        expect(await screen.findByText('Connection successful')).toBeTruthy();
    });
});
