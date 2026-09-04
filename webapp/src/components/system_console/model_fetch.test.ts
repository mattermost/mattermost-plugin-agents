// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fetchModels, fetchModelsForAgentService} from '@/client';
import {FakeSetting} from '@/constants';

import {fetchModelsForService, supportsModelFetching} from './model_fetch';

jest.mock('@/client', () => ({
    fetchModels: jest.fn(),
    fetchModelsForAgentService: jest.fn(),
}));

const mockFetchModels = fetchModels as jest.MockedFunction<typeof fetchModels>;
const mockFetchModelsForAgentService = fetchModelsForAgentService as jest.MockedFunction<typeof fetchModelsForAgentService>;

const signal = new AbortController().signal;

const baseService = {
    id: 'svc-1',
    type: 'anthropic',
    apiKey: 'test-key',
    apiURL: '',
    orgId: '',
    region: '',
    vertexProjectID: '',
    vertexProjectNumber: '',
    vertexAuthCredentials: '',
};

beforeEach(() => {
    mockFetchModels.mockReset();
    mockFetchModelsForAgentService.mockReset();
    mockFetchModels.mockResolvedValue([]);
    mockFetchModelsForAgentService.mockResolvedValue([]);
});

describe('supportsModelFetching', () => {
    it.each([
        ['anthropic', true],
        ['openai', true],
        ['vertex', true],
        ['bedrock', false],
    ] as const)('%s → %s', (type, expected) => {
        expect(supportsModelFetching(type)).toBe(expected);
    });
});

describe('fetchModelsForService', () => {
    it('uses stored credentials for a persisted placeholder', async () => {
        const models = await fetchModelsForService({...baseService, apiKey: FakeSetting}, true, signal);

        expect(models).toEqual([]);
        expect(mockFetchModelsForAgentService).toHaveBeenCalledWith(baseService.id, signal);
        expect(mockFetchModels).not.toHaveBeenCalled();
    });

    it('uses stored credentials for persisted Vertex auth represented by the placeholder', async () => {
        const service = {
            ...baseService,
            type: 'vertex',
            apiKey: '',
            region: 'us-central1',
            vertexProjectID: 'project-id',
            vertexAuthCredentials: FakeSetting,
        };

        await fetchModelsForService(service, true, signal);

        expect(mockFetchModelsForAgentService).toHaveBeenCalledWith(service.id, signal);
        expect(mockFetchModels).not.toHaveBeenCalled();
    });

    it('does not fetch for an unsaved placeholder', async () => {
        const models = await fetchModelsForService({...baseService, apiKey: FakeSetting}, false, signal);

        expect(models).toBeNull();
        expect(mockFetchModelsForAgentService).not.toHaveBeenCalled();
        expect(mockFetchModels).not.toHaveBeenCalled();
    });

    it('fetches directly when the service has a real credential', async () => {
        await fetchModelsForService(baseService, false, signal);

        expect(mockFetchModels).toHaveBeenCalledWith(
            baseService.type,
            baseService.apiKey,
            baseService.apiURL,
            baseService.orgId,
            expect.objectContaining({
                region: '',
                vertexProjectID: '',
                vertexProjectNumber: '',
                vertexAuthCredentials: '',
            }),
            signal,
        );
        expect(mockFetchModelsForAgentService).not.toHaveBeenCalled();
    });
});
