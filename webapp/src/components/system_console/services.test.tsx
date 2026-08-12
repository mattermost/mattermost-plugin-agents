// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import Services from './services';
import {type LLMService} from './service';
import {LLMBotConfig} from './bot';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string | number>) => {
                if (!values) {
                    return defaultMessage;
                }
                return Object.entries(values).reduce(
                    (message, [key, value]) => message.replace(`{${key}}`, String(value)),
                    defaultMessage,
                );
            },
        }),
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

// The heavy per-service editor is not under test; expose identity and the
// delete hook so list-level behavior is observable.
jest.mock('./service', () => ({
    __esModule: true,
    default: ({service, onDelete}: {service: {id: string; name: string}; onDelete: () => void}) => (
        <div>
            <span>{`service:${service.name}:${service.id || 'unsaved'}`}</span>
            <button onClick={onDelete}>{`delete-${service.name}`}</button>
        </div>
    ),
}));

const persistedService: LLMService = {
    id: 'serviceidaaaaaaaaaaaaaaaaa',
    name: 'Persisted',
    type: 'openai',
    apiKey: '',
    apiURL: '',
    orgId: '',
    defaultModel: '',
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
    fallbackServiceID: '',
};

function renderServices(services: LLMService[], bots: LLMBotConfig[] = []) {
    const onChange = jest.fn();
    render(
        <IntlProvider locale='en'>
            <Services
                services={services}
                bots={bots}
                onChange={onChange}
            />
        </IntlProvider>,
    );
    return {onChange};
}

describe('Services', () => {
    it('adds the first service without a client-side id', () => {
        const {onChange} = renderServices([]);

        fireEvent.click(screen.getByText('Add an AI Service'));

        expect(onChange).toHaveBeenCalledTimes(1);
        const added = onChange.mock.calls[0][0] as LLMService[];
        expect(added).toHaveLength(1);
        expect(added[0].name).toBe('OpenAI Service');
        expect(added[0].id).toBe('');
    });

    it('appends subsequent services without client-side ids', () => {
        const {onChange} = renderServices([persistedService]);

        fireEvent.click(screen.getByText('Add an AI Service'));

        const next = onChange.mock.calls[0][0] as LLMService[];
        expect(next).toHaveLength(2);
        expect(next[0]).toBe(persistedService);
        expect(next[1].id).toBe('');
    });

    it('deletes unsaved entries by position, not by (empty) id', () => {
        const unsavedA = {...persistedService, id: '', name: 'Unsaved A'};
        const unsavedB = {...persistedService, id: '', name: 'Unsaved B'};
        const {onChange} = renderServices([unsavedA, unsavedB]);

        fireEvent.click(screen.getByText('delete-Unsaved B'));

        expect(onChange).toHaveBeenCalledWith([unsavedA]);
    });

    it('blocks deleting a persisted service that a bot uses', () => {
        const bot = {id: 'bot1', name: 'bot', displayName: 'My Bot', serviceID: persistedService.id} as unknown as LLMBotConfig;
        const {onChange} = renderServices([persistedService], [bot]);

        fireEvent.click(screen.getByText('delete-Persisted'));

        expect(onChange).not.toHaveBeenCalled();
        expect(screen.getByText('Cannot delete this service because it is being used by the following bot(s): My Bot')).toBeTruthy();
    });

    it('clears dangling fallback links when deleting a persisted service', () => {
        const dependent = {...persistedService, id: 'serviceidbbbbbbbbbbbbbbbbb', name: 'Dependent', fallbackServiceID: persistedService.id};
        const {onChange} = renderServices([persistedService, dependent]);

        fireEvent.click(screen.getByText('delete-Persisted'));

        expect(onChange).toHaveBeenCalledWith([{...dependent, fallbackServiceID: ''}]);
    });
});
