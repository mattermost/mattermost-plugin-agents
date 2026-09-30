// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {LLMService} from './service';

import {connectionFingerprint, servicesNeedingConnectionTest} from './service_connection';

const baseService: LLMService = {
    id: 'svc-1',
    name: 'OpenAI',
    type: 'openai',
    apiURL: '',
    apiKey: 'key-1',
    orgId: '',
    defaultModel: 'gpt-5.2',
    tokenLimit: 128000,
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

describe('servicesNeedingConnectionTest', () => {
    it.each([
        {
            name: 'unchanged service is skipped',
            service: baseService,
            want: false,
        },
        {
            name: 'changed API key is retested',
            service: {...baseService, apiKey: 'key-2'},
            want: true,
        },
        {
            name: 'changed default model is retested',
            service: {...baseService, defaultModel: 'gpt-5.2-mini'},
            want: true,
        },
        {
            name: 'changed API URL is retested',
            service: {...baseService, apiURL: 'https://proxy.example.com'},
            want: true,
        },
        {
            name: 'renaming alone is skipped',
            service: {...baseService, name: 'Renamed'},
            want: false,
        },
        {
            name: 'token limit alone is skipped',
            service: {...baseService, tokenLimit: 64000},
            want: false,
        },
        {
            name: 'unsaved service with no ID is always tested',
            service: {...baseService, id: ''},
            want: true,
        },
    ])('$name', ({service, want}) => {
        const verified = {'svc-1': connectionFingerprint(baseService)};

        const needing = servicesNeedingConnectionTest([service], verified);

        expect(needing.length === 1).toBe(want);
    });

    it('tests a service that has never been verified', () => {
        const needing = servicesNeedingConnectionTest([baseService], {});

        expect(needing).toEqual([baseService]);
    });

    it('returns only the services that changed', () => {
        const other: LLMService = {...baseService, id: 'svc-2', apiKey: 'key-2'};
        const verified = {
            'svc-1': connectionFingerprint(baseService),
            'svc-2': connectionFingerprint(other),
        };

        const needing = servicesNeedingConnectionTest(
            [baseService, {...other, apiKey: 'key-3'}],
            verified,
        );

        expect(needing.map((s) => s.id)).toEqual(['svc-2']);
    });
});
