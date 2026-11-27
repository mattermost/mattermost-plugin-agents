// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage, IntlShape, useIntl} from 'react-intl';
import styled from 'styled-components';

import {AdminPanel} from '@mattermost/compass-ui/components/admin-panel';
import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';
import {Tag} from '@mattermost/compass-ui/components/tag';

import {useIsLicensedFor} from '@/license';

import {BooleanItem, ComboboxItem, ItemList, SelectionItem, TextItem} from './item';
import {LicenseChip} from './enterprise_chip';
import {LLMService} from './service';

export type DictationConfig = {
    enabled: boolean;

    // The service's ID, never its name.
    serviceID: string;

    // OpenAI model name, or Azure deployment name.
    model: string;
    language?: string;
};

export const DEFAULT_DICTATION_MODEL = 'gpt-live-transcribe';

export const defaultDictationConfig: DictationConfig = {
    enabled: false,
    serviceID: '',
    model: DEFAULT_DICTATION_MODEL,
    language: '',
};

// OpenAI has deprecated its earlier transcription models (gpt-4o-transcribe,
// gpt-4o-mini-transcribe, whisper-1), so only the live model is suggested.
const MODEL_SUGGESTIONS = [DEFAULT_DICTATION_MODEL];

// Service types that can mint a browser transcription session. Azure joins
// once its WebRTC transcription path is verified.
const DICTATION_SERVICE_TYPES = ['openai'];

// dictationServices lists the saved services dictation can use. A service
// added on this page has no ID until the configuration is saved.
export function dictationServices(services: LLMService[]): LLMService[] {
    return services.filter((s) => Boolean(s.id) && DICTATION_SERVICE_TYPES.includes(s.type));
}

// The API URL may hold only an http(s) scheme, a host and a path.
function isPlainAPIURL(apiURL: string): boolean {
    let url: URL;
    try {
        url = new URL(apiURL);
    } catch {
        return false;
    }
    return (url.protocol === 'https:' || url.protocol === 'http:') && Boolean(url.host) && !url.username && !url.password && !url.search && !url.hash;
}

export type DictationConfigErrors = {
    serviceID?: string;
    model?: string;
};

// validateDictationConfig mirrors the server's save validation so the admin
// sees which field to fix.
export function validateDictationConfig(config: DictationConfig, services: LLMService[], intl: IntlShape): DictationConfigErrors {
    if (!config.enabled) {
        return {};
    }
    const errors: DictationConfigErrors = {};
    const service = dictationServices(services).find((s) => s.id === config.serviceID);
    if (!config.serviceID) {
        errors.serviceID = intl.formatMessage({defaultMessage: 'Select the service that transcribes dictation.'});
    } else if (!service) {
        errors.serviceID = intl.formatMessage({defaultMessage: 'The selected service no longer exists or is not an OpenAI service. Select another one.'});
    } else if (!service.apiKey) {
        errors.serviceID = intl.formatMessage({defaultMessage: 'The selected service has no API key. Add one under AI Services.'});
    } else if (service.apiURL && !isPlainAPIURL(service.apiURL)) {
        errors.serviceID = intl.formatMessage({defaultMessage: 'The selected service\'s API URL must have only a scheme, host and path, with no credentials, query or fragment.'});
    }
    if (!config.model.trim()) {
        errors.model = intl.formatMessage({defaultMessage: 'Enter the transcription model.'});
    }
    return errors;
}

type Props = {
    value: DictationConfig;
    services: LLMService[];
    errors: DictationConfigErrors;

    // The server's reason for refusing the last save.
    saveError?: string;
    onChange: (config: DictationConfig) => void;
};

const DictationPanel = ({value, services, errors, saveError, onChange}: Props) => {
    const intl = useIntl();
    const licensed = useIsLicensedFor('dictation');
    const eligible = dictationServices(services);
    const unsavedEligible = services.some((s) => !s.id && DICTATION_SERVICE_TYPES.includes(s.type));

    const update = (patch: Partial<DictationConfig>) => {
        onChange({...value, ...patch});
    };

    return (
        <AdminPanel
            title={<FormattedMessage defaultMessage='Dictation'/>}
            subtitle={intl.formatMessage({defaultMessage: 'Let users dictate messages with a microphone button in the message box.'})}
            headerActions={licensed ? undefined : <LicenseChip capability='dictation'/>} // eslint-disable-line no-undefined
        >
            <ItemList>
                {saveError && (
                    <SectionNotice
                        type='danger'
                        title={intl.formatMessage({defaultMessage: 'The Dictation settings were not saved'})}
                        description={saveError}
                    />
                )}
                <BooleanItem
                    label={
                        <ExperimentalLabel>
                            <FormattedMessage defaultMessage='Enable dictation'/>
                            <Tag
                                type='info'
                                label={<FormattedMessage defaultMessage='EXPERIMENTAL'/>}
                            />
                        </ExperimentalLabel>
                    }
                    value={value.enabled}
                    disableTrue={!licensed}
                    extra={!licensed && <LicenseChip capability='dictation'/>}
                    onChange={(enabled) => update({enabled})}
                    helpText={intl.formatMessage({defaultMessage: 'Browsers send microphone audio directly to the selected service, not through Mattermost. Users\' browsers must be able to reach the service over HTTPS and WebRTC. Audio and transcripts are processed under your organization\'s agreement with the provider. Mattermost doesn\'t store audio; dictated text is saved like typed text, including in drafts. The provider bills each dictation session to the service\'s API key.'})}
                />
                {eligible.length === 0 ? (
                    <SectionNotice
                        type='info'
                        title={intl.formatMessage({defaultMessage: 'No OpenAI service is available for dictation'})}
                        description={unsavedEligible ? intl.formatMessage({defaultMessage: 'Save the configuration to use the OpenAI service you added.'}) : intl.formatMessage({defaultMessage: 'Add an OpenAI service under AI Services and save. It doesn\'t need an agent.'})}
                    />
                ) : (
                    <SelectionItem
                        label={intl.formatMessage({defaultMessage: 'Transcription service'})}
                        value={value.serviceID}
                        options={eligible.map((s) => ({value: s.id, label: s.name}))}
                        onChange={(serviceID) => update({serviceID})}
                        disabled={!value.enabled}
                        error={errors.serviceID}
                        helptext={intl.formatMessage({defaultMessage: 'Dictation uses this service independently of the services your agents use. Only OpenAI services are supported.'})}
                    />
                )}
                <ComboboxItem
                    label={intl.formatMessage({defaultMessage: 'Transcription model'})}
                    value={value.model}
                    options={MODEL_SUGGESTIONS.map((model) => ({id: model, displayName: model}))}
                    onChange={(model) => update({model})}
                    isClearable={false}
                    error={errors.model}
                    helptext={intl.formatMessage({defaultMessage: 'The OpenAI transcription model. gpt-live-transcribe shows text while the user speaks.'})}
                />
                <TextItem
                    label={intl.formatMessage({defaultMessage: 'Language'})}
                    value={value.language ?? ''}
                    placeholder={'en'}
                    maxLength={8}
                    onChange={(e) => update({language: e.target.value.trim()})}
                    disabled={!value.enabled}
                    helptext={intl.formatMessage({defaultMessage: 'Optional ISO-639-1 code of the language users speak, such as en or de. Leave empty to detect it automatically.'})}
                />
            </ItemList>
        </AdminPanel>
    );
};

export default DictationPanel;

const ExperimentalLabel = styled.div`
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: var(--spacing-xxxs);
`;
