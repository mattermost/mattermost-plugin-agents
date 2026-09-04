// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fetchModels, fetchModelsForAgentService} from '@/client';
import {FakeSetting} from '@/constants';

export type ModelInfo = {
    id: string
    displayName: string
    inputTokenLimit?: number
    outputTokenLimit?: number
    contextLength?: number
}

type ModelFetchService = {
    id: string
    type: string
    apiKey: string
    apiURL: string
    orgId: string
    region: string
    vertexProjectID: string
    vertexProjectNumber: string
    vertexAuthCredentials: string
}

const modelFetchingProviders = new Set(['anthropic', 'openai', 'azure', 'openaicompatible', 'gemini', 'vertex']);

export function supportsModelFetching(serviceType: string): boolean {
    return modelFetchingProviders.has(serviceType);
}

function hasRequiredCredentials(service: ModelFetchService): boolean {
    switch (service.type) {
    case 'openaicompatible':
        return Boolean(service.apiKey || service.apiURL);
    case 'vertex':
        return Boolean(service.vertexProjectID && service.region);
    default:
        return Boolean(service.apiKey);
    }
}

function hasCredentialPlaceholder(service: ModelFetchService): boolean {
    return service.apiKey === FakeSetting ||
        (service.type === 'vertex' && service.vertexAuthCredentials === FakeSetting);
}

async function fetchModelsDirectly(service: ModelFetchService, signal: AbortSignal): Promise<ModelInfo[]> {
    const models: ModelInfo[] = await fetchModels(
        service.type,
        service.apiKey,
        service.apiURL || '',
        service.orgId || '',
        {
            region: service.region || '',
            vertexProjectID: service.vertexProjectID || '',
            vertexProjectNumber: service.vertexProjectNumber || '',
            vertexAuthCredentials: service.type === 'vertex' ? (service.vertexAuthCredentials || '') : '',
        },
        signal,
    );
    return models;
}

export async function fetchModelsForService(service: ModelFetchService, isPersisted: boolean, signal: AbortSignal): Promise<ModelInfo[] | null> {
    if (!supportsModelFetching(service.type) || !hasRequiredCredentials(service)) {
        return null;
    }

    if (hasCredentialPlaceholder(service)) {
        if (!isPersisted) {
            return null;
        }
        return fetchModelsForAgentService(service.id, signal);
    }

    return fetchModelsDirectly(service, signal);
}
