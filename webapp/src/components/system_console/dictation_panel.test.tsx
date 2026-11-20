// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';
import {IntlProvider, IntlShape} from 'react-intl';

import {chooseOption, getSelect, optionLabels} from '../../../tests/compass_select';

import DictationPanel, {DictationConfig, defaultDictationConfig, dictationServices, validateDictationConfig} from './dictation_panel';
import {LLMService} from './service';

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

jest.mock('@/license', () => ({
    useIsLicensedFor: jest.fn(() => true),
    useLicenseLevelName: jest.fn(() => () => 'Enterprise'),
    requiredLevelFor: jest.fn(() => 2),
}));

const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};

const intl = {formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage} as unknown as IntlShape;

const service = (overrides: Partial<LLMService>): LLMService => ({
    id: '',
    name: '',
    type: 'openai',
    apiURL: '',
    apiKey: '',
    orgId: '',
    defaultModel: '',
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
});

const anthropic = service({id: 'svc-anthropic', name: 'Claude', type: 'anthropic'});
const openai = service({id: 'svc-openai', name: 'OpenAI dictation', type: 'openai', apiKey: '********************************'});
const azure = service({id: 'svc-azure', name: 'Azure', type: 'azure'});
const compatible = service({id: 'svc-compat', name: 'Compatible', type: 'openaicompatible'});
const unsavedOpenAI = service({id: '', name: 'New OpenAI', type: 'openai'});

const enabled: DictationConfig = {...defaultDictationConfig, enabled: true, serviceID: openai.id};

function renderPanel(value: DictationConfig, services: LLMService[], onChange = jest.fn()) {
    render(
        <IntlProvider locale='en'>
            <DictationPanel
                value={value}
                services={services}
                errors={validateDictationConfig(value, services, intl)}
                onChange={onChange}
            />
        </IntlProvider>,
    );
    return onChange;
}

describe('dictationServices', () => {
    test('lists only saved OpenAI services', () => {
        expect(dictationServices([anthropic, openai, azure, compatible, unsavedOpenAI])).toEqual([openai]);
    });
});

describe('validateDictationConfig', () => {
    test.each([
        {name: 'disabled is always valid', config: {...defaultDictationConfig, serviceID: '', model: ''}, want: {}},
        {name: 'valid', config: enabled, want: {}},
        {name: 'no service', config: {...enabled, serviceID: ''}, want: {serviceID: 'Select the service that transcribes dictation.'}},
        {name: 'deleted service', config: {...enabled, serviceID: 'gone'}, want: {serviceID: expect.stringContaining('no longer exists')}},
        {name: 'wrong service type', config: {...enabled, serviceID: anthropic.id}, want: {serviceID: expect.stringContaining('not an OpenAI service')}},
        {name: 'empty model', config: {...enabled, model: '  '}, want: {model: 'Enter the transcription model.'}},
    ])('$name', ({config, want}) => {
        expect(validateDictationConfig(config, [anthropic, openai], intl)).toEqual(want);
    });

    test.each([
        {name: 'no API key', apiKey: '', apiURL: '', invalid: true},
        {name: 'plain API URL', apiKey: 'sk', apiURL: 'https://proxy.example.com/openai', invalid: false},
        {name: 'credentials in the API URL', apiKey: 'sk', apiURL: 'https://user:pass@proxy.example.com', invalid: true},
        {name: 'query in the API URL', apiKey: 'sk', apiURL: 'https://proxy.example.com/?token=x', invalid: true},
        {name: 'fragment in the API URL', apiKey: 'sk', apiURL: 'https://proxy.example.com/#x', invalid: true},
        {name: 'relative API URL', apiKey: 'sk', apiURL: 'proxy.example.com', invalid: true},
        {name: 'protocol-relative API URL', apiKey: 'sk', apiURL: '//proxy.example.com', invalid: true},
        {name: 'ftp API URL', apiKey: 'sk', apiURL: 'ftp://proxy.example.com', invalid: true},
        {name: 'websocket API URL', apiKey: 'sk', apiURL: 'wss://proxy.example.com', invalid: true},
        {name: 'javascript API URL', apiKey: 'sk', apiURL: 'javascript://proxy.example.com/x', invalid: true}, // eslint-disable-line no-script-url -- the validator must reject it
        {name: 'plain http API URL for a local proxy', apiKey: 'sk', apiURL: 'http://realtime:8095', invalid: false},
    ])('service with $name: invalid=$invalid', ({apiKey, apiURL, invalid}) => {
        const svc = {...openai, apiKey, apiURL};
        expect(Boolean(validateDictationConfig(enabled, [svc], intl).serviceID)).toBe(invalid);
    });
});

describe('DictationPanel', () => {
    beforeEach(() => {
        useIsLicensedFor.mockReturnValue(true);
    });

    test('offers only OpenAI services, independent of the services agents use', () => {
        const onChange = renderPanel(enabled, [anthropic, openai, compatible]);
        const select = getSelect('Transcription service');
        expect(optionLabels(select)).toEqual(['OpenAI dictation']);

        chooseOption(select, 'OpenAI dictation');
        expect(onChange).toHaveBeenCalledWith(expect.objectContaining({serviceID: openai.id}));
    });

    test.each([
        {name: 'no OpenAI service', services: [anthropic], description: 'Add an OpenAI service under AI Services and save. It doesn\'t need an agent.'},
        {name: 'an unsaved OpenAI service', services: [anthropic, unsavedOpenAI], description: 'Save the configuration to use the OpenAI service you added.'},
    ])('shows an empty state with $name', ({services, description}) => {
        renderPanel(enabled, services);
        expect(screen.getByText('No OpenAI service is available for dictation')).toBeTruthy();
        expect(screen.getByText(description)).toBeTruthy();
        expect(screen.queryByRole('combobox', {name: 'Transcription service'})).toBeNull();
    });

    test('shows the validation error on the service field, marked invalid and described by it', () => {
        renderPanel({...enabled, serviceID: anthropic.id}, [anthropic, openai]);
        const select = screen.getByRole('combobox', {name: 'Transcription service'});
        expect(select.getAttribute('aria-invalid')).toBe('true');
        expect(document.getElementById(select.getAttribute('aria-describedby') ?? '')?.textContent).toMatch(/not an OpenAI service/);
    });

    test('marks a missing model invalid and describes it with the error', () => {
        renderPanel({...enabled, model: ''}, [openai]);
        const model = screen.getByRole('combobox', {name: 'Transcription model'});
        expect(model.getAttribute('aria-invalid')).toBe('true');
        expect(document.getElementById(model.getAttribute('aria-describedby') ?? '')?.textContent).toBe('Enter the transcription model.');
    });

    test('marks the enable toggle as experimental', () => {
        renderPanel(defaultDictationConfig, [openai]);
        const group = screen.getByRole('radiogroup', {name: /^Enable dictation/});
        expect(group.getAttribute('aria-labelledby')).toBeTruthy();
        const label = document.getElementById(group.getAttribute('aria-labelledby')!);
        expect(label?.textContent).toBe('Enable dictationEXPERIMENTAL');
    });

    test('pre-fills gpt-live-transcribe as the model', () => {
        renderPanel(defaultDictationConfig, [openai]);
        expect(screen.getByDisplayValue('gpt-live-transcribe')).toBeTruthy();
    });

    test('suggests only the live model, not the deprecated ones', () => {
        renderPanel(defaultDictationConfig, [openai]);
        const input = screen.getByRole('combobox', {name: 'Transcription model'});
        fireEvent.focus(input);
        fireEvent.keyDown(input, {key: 'ArrowDown'});
        const options = screen.getAllByRole('option').map((el) => el.textContent);
        expect(options).toEqual(['gpt-live-transcribe']);
    });

    test('trims the language hint', () => {
        const onChange = renderPanel(enabled, [openai]);
        fireEvent.change(screen.getByPlaceholderText('en'), {target: {value: ' de '}});
        expect(onChange).toHaveBeenCalledWith(expect.objectContaining({language: 'de'}));
    });

    test.each([
        {name: 'unset', config: {}, wantOn: true},
        {name: 'on', config: {useChannelContext: true}, wantOn: true},
        {name: 'off', config: {useChannelContext: false}, wantOn: false},
    ])('channel context $name shows on=$wantOn', ({config, wantOn}) => {
        renderPanel({...enabled, ...config}, [openai]);
        const group = screen.getByRole('radiogroup', {name: 'Use channel context to improve accuracy'});
        const [trueRadio, falseRadio] = Array.from(group.querySelectorAll('input')) as HTMLInputElement[];
        expect(trueRadio.checked).toBe(wantOn);
        expect(falseRadio.checked).toBe(!wantOn);
    });

    test('turning channel context off saves false', () => {
        const onChange = renderPanel(enabled, [openai]);
        const group = screen.getByRole('radiogroup', {name: 'Use channel context to improve accuracy'});
        fireEvent.click(group.querySelectorAll('input')[1]);
        expect(onChange).toHaveBeenCalledWith(expect.objectContaining({useChannelContext: false}));
    });

    test.each([
        {licensed: true, enableDisabled: false},
        {licensed: false, enableDisabled: true},
    ])('licensed=$licensed: enabling disabled=$enableDisabled', ({licensed, enableDisabled}) => {
        useIsLicensedFor.mockReturnValue(licensed);
        renderPanel(defaultDictationConfig, [openai]);
        expect(useIsLicensedFor).toHaveBeenCalledWith('dictation');
        const trueRadio = screen.getAllByDisplayValue('true')[0] as HTMLInputElement;
        expect(trueRadio.disabled).toBe(enableDisabled);
    });
});
