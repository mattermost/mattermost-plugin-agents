// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {ServiceFields, type LLMService} from './service';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
        }),
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('../../client', () => ({
    fetchModels: jest.fn(),
}));

const {fetchModels} = jest.requireMock('../../client') as {
    fetchModels: jest.Mock;
};

const baseService: LLMService = {
    id: 'svc-1',
    name: 'Anthropic',
    type: 'anthropic',
    apiURL: '',
    apiKey: 'test-key',
    orgId: '',
    defaultModel: 'claude-sonnet-4-5',
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
};

function renderFields(service: LLMService = baseService) {
    const onChange = jest.fn();
    const result = render(
        <IntlProvider locale='en'>
            <ServiceFields
                service={service}
                onChange={onChange}
            />
        </IntlProvider>,
    );
    return {...result, onChange};
}

beforeEach(() => {
    fetchModels.mockReset();
});

describe('ServiceFields token-limit inputs', () => {
    it('persists the Bifrost-provided limits via onChange so the saved config carries them', async () => {
        fetchModels.mockResolvedValue([
            {
                id: 'claude-sonnet-4-5',
                displayName: 'Claude Sonnet 4.5',
                inputTokenLimit: 200000,
                outputTokenLimit: 8192,
            },
        ]);

        const {onChange} = renderFields({...baseService, tokenLimit: 50000});

        await waitFor(() => {
            const calls = onChange.mock.calls.map((c) => c[0]);
            expect(calls.some((s: LLMService) => s.tokenLimit === 200000)).toBe(true);
            expect(calls.some((s: LLMService) => s.outputTokenLimit === 8192)).toBe(true);
        });
    });

    it('disables and prefills both inputs when Bifrost reports limits for the selected model', async () => {
        fetchModels.mockResolvedValue([
            {
                id: 'claude-sonnet-4-5',
                displayName: 'Claude Sonnet 4.5',
                inputTokenLimit: 200000,
                outputTokenLimit: 8192,
            },
        ]);

        renderFields();

        const inputField = await waitFor(() => screen.getByDisplayValue('200000') as HTMLInputElement);
        expect(inputField.disabled).toBe(true);

        const outputField = screen.getByDisplayValue('8192') as HTMLInputElement;
        expect(outputField.disabled).toBe(true);
    });

    it('leaves the input editable when Bifrost has the model but no input-token limit', async () => {
        fetchModels.mockResolvedValue([
            {
                id: 'claude-sonnet-4-5',
                displayName: 'Claude Sonnet 4.5',
                outputTokenLimit: 8192,
                // inputTokenLimit missing — should stay editable.
            },
        ]);

        renderFields();

        // The output field is disabled (Bifrost-known) at 8192.
        await waitFor(() => expect((screen.getByDisplayValue('8192') as HTMLInputElement).disabled).toBe(true));

        // Input field falls back to the stored value (0) and stays editable.
        const inputField = screen.getByDisplayValue('0') as HTMLInputElement;
        expect(inputField.disabled).toBe(false);
    });

    it('restores the previously stored manual input when switching from a Bifrost-known model to an unknown one', async () => {
        fetchModels.mockResolvedValue([
            {
                id: 'claude-sonnet-4-5',
                displayName: 'Claude Sonnet 4.5',
                inputTokenLimit: 200000,
                outputTokenLimit: 8192,
            },
        ]);

        // Start with a Bifrost-known model AND a previously-stored manual value M=50000.
        const initialService = {...baseService, tokenLimit: 50000};
        const onChange = jest.fn();
        const {rerender} = render(
            <IntlProvider locale='en'>
                <ServiceFields
                    service={initialService}
                    onChange={onChange}
                />
            </IntlProvider>,
        );

        // Initial render: input disabled, prefilled with Bifrost's 200000.
        await waitFor(() => expect((screen.getByDisplayValue('200000') as HTMLInputElement).disabled).toBe(true));

        // Switch to a custom model not in the fetched list.
        rerender(
            <IntlProvider locale='en'>
                <ServiceFields
                    service={{...initialService, defaultModel: 'custom-unknown'}}
                    onChange={onChange}
                />
            </IntlProvider>,
        );

        // Manual value M=50000 must be restored, input editable.
        const restored = await waitFor(() => screen.getByDisplayValue('50000') as HTMLInputElement);
        expect(restored.disabled).toBe(false);
    });

    it('leaves both inputs editable when the selected model is not in the fetched list', async () => {
        fetchModels.mockResolvedValue([
            {
                id: 'some-other-model',
                displayName: 'Other',
                inputTokenLimit: 200000,
                outputTokenLimit: 8192,
            },
        ]);

        const service = {...baseService, defaultModel: 'custom-model-not-in-list', tokenLimit: 50000, outputTokenLimit: 4096};
        renderFields(service);

        await waitFor(() => expect(fetchModels).toHaveBeenCalled());

        const inputField = screen.getByDisplayValue('50000') as HTMLInputElement;
        expect(inputField.disabled).toBe(false);
        const outputField = screen.getByDisplayValue('4096') as HTMLInputElement;
        expect(outputField.disabled).toBe(false);
    });
});
