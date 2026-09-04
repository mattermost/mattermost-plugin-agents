// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {getAIBots, getPluginConfig, savePluginConfig} from '@/client';
import {FakeSetting} from '@/constants';

import Config from './config';
import type {LLMService} from './service';

type MockServicesProps = {
    services: LLMService[]
    persistedServiceIDs: ReadonlySet<string>
    onChange: (services: LLMService[]) => void
}

function mockServices(props: MockServicesProps) {
    const newService = testService({
        id: 'generated-id',
        name: 'New service',
        defaultModel: '',
    });

    return (
        <>
            <div data-testid='service-persistence'>
                {props.services.map((service) => `${service.id}:${props.persistedServiceIDs.has(service.id) ? 'persisted' : 'unsaved'}`).join(',')}
            </div>
            <button onClick={() => props.onChange([...props.services, newService])}>
                {'Add mock service'}
            </button>
        </>
    );
}

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
    return {
        ...actual,
        useIntl: () => intl,
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('@/client', () => ({
    getAIBots: jest.fn().mockResolvedValue({bots: []}),
    getPluginConfig: jest.fn(),
    savePluginConfig: jest.fn(() => Promise.resolve()),
}));

jest.mock('./services', () => ({
    __esModule: true,
    default: mockServices,
    firstNewService: {},
}));

jest.mock('./embedding_search/embedding_search_panel', () => ({
    __esModule: true,
    default: () => null,
}));

jest.mock('./web_search/web_search_panel', () => ({
    __esModule: true,
    default: () => null,
}));

jest.mock('./mcp_servers', () => ({
    __esModule: true,
    default: () => null,
}));

const mockGetAIBots = getAIBots as jest.MockedFunction<typeof getAIBots>;
const mockGetPluginConfig = getPluginConfig as jest.MockedFunction<typeof getPluginConfig>;
const mockSavePluginConfig = savePluginConfig as jest.MockedFunction<typeof savePluginConfig>;

function testService(overrides: Partial<LLMService> = {}): LLMService {
    return {
        id: 'existing-id',
        name: 'Existing service',
        type: 'anthropic',
        apiURL: '',
        apiKey: FakeSetting,
        orgId: '',
        defaultModel: 'model',
        tokenLimit: 0,
        streamingTimeoutSeconds: 0,
        outputTokenLimit: 0,
        useResponsesAPI: false,
        region: '',
        awsAccessKeyID: '',
        awsSecretAccessKey: '',
        vertexProjectID: '',
        vertexProjectNumber: '',
        vertexAuthCredentials: '',
        ...overrides,
    };
}

async function renderConfig() {
    const registeredActions: Array<() => Promise<{error?: {message?: string}}>> = [];
    render(
        <IntlProvider locale='en'>
            <Config
                id='config'
                label='Config'
                helpText={null}
                value={{} as never}
                disabled={false}
                config={{}}
                currentState={{}}
                license={{}}
                setByEnv={false}
                onChange={jest.fn()}
                setSaveNeeded={jest.fn()}
                registerSaveAction={(action) => registeredActions.push(action)}
                unRegisterSaveAction={jest.fn()}
            />
        </IntlProvider>,
    );
    await screen.findByText('AI Services');
    return registeredActions;
}

describe('Config saving', () => {
    it('preserves placeholders on save and only persists newly added services after success', async () => {
        mockGetAIBots.mockResolvedValue({bots: []});
        mockGetPluginConfig.mockResolvedValue({
            services: [testService({
                awsSecretAccessKey: FakeSetting,
                vertexAuthCredentials: FakeSetting,
            })],
        } as Awaited<ReturnType<typeof getPluginConfig>>);
        mockSavePluginConfig.mockImplementation(() => Promise.resolve());

        const registeredActions = await renderConfig();
        await waitFor(() => expect(registeredActions.length).toBeGreaterThan(1));
        expect(screen.getByTestId('service-persistence').textContent).toBe('existing-id:persisted');

        const save = registeredActions[registeredActions.length - 1];
        await act(async () => {
            await save();
        });
        expect(mockSavePluginConfig).toHaveBeenCalledWith(expect.objectContaining({
            services: [expect.objectContaining({
                apiKey: FakeSetting,
                awsSecretAccessKey: FakeSetting,
                vertexAuthCredentials: FakeSetting,
            })],
        }));

        const actionCountBeforeAdd = registeredActions.length;
        fireEvent.click(screen.getByText('Add mock service'));
        await waitFor(() => expect(screen.getByTestId('service-persistence').textContent).toContain('generated-id:unsaved'));
        await waitFor(() => expect(registeredActions.length).toBeGreaterThan(actionCountBeforeAdd));

        const saveAfterAdd = registeredActions[registeredActions.length - 1];
        mockSavePluginConfig.mockRejectedValueOnce(new Error('save failed'));
        let result: {error?: {message?: string}} = {};
        await act(async () => {
            result = await saveAfterAdd();
        });
        expect(result.error?.message).toBe('Failed to save configuration.');
        expect(screen.getByTestId('service-persistence').textContent).toBe('existing-id:persisted,generated-id:unsaved');

        mockSavePluginConfig.mockImplementation(() => Promise.resolve());
        await act(async () => {
            await saveAfterAdd();
        });
        await waitFor(() => expect(screen.getByTestId('service-persistence').textContent).toContain('generated-id:persisted'));
    });
});
