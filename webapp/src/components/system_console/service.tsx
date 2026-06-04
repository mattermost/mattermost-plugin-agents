// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useEffect, useRef} from 'react';
import styled from 'styled-components';
import {useIntl, type IntlShape} from 'react-intl';

import {TrashCanOutlineIcon, ChevronDownIcon, ChevronUpIcon} from '@mattermost/compass-icons/components';

import IconAI from '../assets/icon_ai';

import {ButtonIcon} from '../assets/buttons';

import {fetchModels} from '../../client';

import {BooleanItem, ItemList, SelectionItem, SelectionItemOption, TextItem, ComboboxItem} from './item';

export type LLMService = {
    id: string
    name: string
    type: string
    apiURL: string
    apiKey: string
    orgId: string
    defaultModel: string
    tokenLimit: number
    streamingTimeoutSeconds: number
    outputTokenLimit: number
    useResponsesAPI: boolean
    region: string
    awsAccessKeyID: string
    awsSecretAccessKey: string
    vertexProjectID: string
    vertexProjectNumber: string
    vertexAuthCredentials: string
}

const mapServiceTypeToDisplayName = new Map<string, string>([
    ['openai', 'OpenAI'],
    ['openaicompatible', 'OpenAI Compatible'],
    ['azure', 'Azure'],
    ['anthropic', 'Anthropic'],
    ['bedrock', 'AWS Bedrock'],
    ['cohere', 'Cohere'],
    ['mistral', 'Mistral'],
    ['asage', 'asksage (Experimental)'],
    ['gemini', 'Google Gemini'],
    ['vertex', 'Google Vertex AI'],
]);

function scaleAIToDisplayName(intl: IntlShape): string {
    return intl.formatMessage({defaultMessage: 'Scale AI'});
}

function serviceTypeToDisplayName(intl: IntlShape, serviceType: string): string {
    if (serviceType === 'scale') {
        return scaleAIToDisplayName(intl);
    }
    return mapServiceTypeToDisplayName.get(serviceType) || serviceType;
}

type ModelInfo = {
    id: string
    displayName: string
    inputTokenLimit?: number
    outputTokenLimit?: number
    contextLength?: number
}

type ServiceFieldsProps = {
    service: LLMService
    onChange: (service: LLMService) => void
}

export const ServiceFields = (props: ServiceFieldsProps) => {
    const type = props.service.type;
    const intl = useIntl();
    const isOpenAIType = type === 'openai' || type === 'openaicompatible' || type === 'azure' || type === 'cohere' || type === 'mistral' || type === 'scale';
    const supportsResponsesAPIToggle = type === 'openaicompatible' || type === 'azure';
    const isCohere = type === 'cohere';
    const isMistral = type === 'mistral';
    const isScale = type === 'scale';

    const [availableModels, setAvailableModels] = useState<ModelInfo[]>([]);
    const [loadingModels, setLoadingModels] = useState(false);
    const [modelsFetchError, setModelsFetchError] = useState<string>('');

    // Cached admin entries so toggling to a Bifrost-known model and back
    // restores the prior manual values instead of the auto-detected ones.
    const [manualInputLimit, setManualInputLimit] = useState<number>(props.service.tokenLimit);
    const [manualOutputLimit, setManualOutputLimit] = useState<number>(props.service.outputTokenLimit);

    // Tracks the limits we last wrote back via onChange so the sync effect below
    // can distinguish an external/upstream change from our own auto write-back
    // and avoid clobbering (or oscillating with) the cached manual entries.
    const lastWrittenLimits = useRef({input: props.service.tokenLimit, output: props.service.outputTokenLimit});

    // Hard-reset the cache when the edited service changes identity.
    useEffect(() => {
        setManualInputLimit(props.service.tokenLimit);
        setManualOutputLimit(props.service.outputTokenLimit);
        lastWrittenLimits.current = {input: props.service.tokenLimit, output: props.service.outputTokenLimit};
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [props.service.id]);

    // Re-seed the cache when the service's limits change upstream without the id
    // changing (e.g. the parent reloads a saved service). Changes we authored
    // ourselves via the write-effect below are skipped, since adopting an
    // auto-detected value here would overwrite the admin's manual entry.
    useEffect(() => {
        if (props.service.tokenLimit !== lastWrittenLimits.current.input) {
            setManualInputLimit(props.service.tokenLimit);
        }
        if (props.service.outputTokenLimit !== lastWrittenLimits.current.output) {
            setManualOutputLimit(props.service.outputTokenLimit);
        }
    }, [props.service.tokenLimit, props.service.outputTokenLimit]);

    const supportsModelFetching = type === 'anthropic' || type === 'openai' || type === 'azure' || type === 'openaicompatible' || type === 'gemini' || type === 'vertex';

    useEffect(() => {
        if (type === 'openai' && !props.service.useResponsesAPI) {
            props.onChange({...props.service, useResponsesAPI: true});
        }
    }, [type, props.onChange, props.service]);

    useEffect(() => {
        // Providers have different credential shapes for model listing:
        // - openaicompatible: API key OR API URL
        // - vertex: GCP project ID + region (service-account JSON optional)
        // - others: API key
        let hasRequiredCredentials = false;
        switch (type) {
        case 'openaicompatible':
            hasRequiredCredentials = Boolean(props.service.apiKey || props.service.apiURL);
            break;
        case 'vertex':
            hasRequiredCredentials = Boolean(props.service.vertexProjectID && props.service.region);
            break;
        default:
            hasRequiredCredentials = Boolean(props.service.apiKey);
        }

        if (!supportsModelFetching || !hasRequiredCredentials) {
            setAvailableModels([]);
            setModelsFetchError('');
            return;
        }

        const loadModels = async () => {
            setLoadingModels(true);
            setModelsFetchError('');

            try {
                const data: ModelInfo[] = await fetchModels(
                    type,
                    props.service.apiKey,
                    props.service.apiURL || '',
                    props.service.orgId || '',
                    {
                        region: props.service.region || '',
                        vertexProjectID: props.service.vertexProjectID || '',
                        vertexProjectNumber: props.service.vertexProjectNumber || '',
                        vertexAuthCredentials: props.service.vertexAuthCredentials || '',
                    },
                );
                setAvailableModels(data);
            } catch (error) {
                setModelsFetchError(intl.formatMessage({defaultMessage: 'Failed to fetch models. Please check your API key and API URL.'}));
                setAvailableModels([]);
            } finally {
                setLoadingModels(false);
            }
        };

        loadModels();
    }, [type, props.service.apiKey, props.service.apiURL, props.service.orgId, props.service.region, props.service.vertexProjectID, props.service.vertexProjectNumber, props.service.vertexAuthCredentials, supportsModelFetching, intl]);

    const getDefaultOutputTokenLimit = () => {
        switch (type) {
        case 'anthropic':
            return '8192';
        case 'bedrock':
            return '8192';
        default:
            return '0';
        }
    };

    let loadModelsHelpText = '';
    if (supportsModelFetching) {
        if (loadingModels) {
            loadModelsHelpText = intl.formatMessage({defaultMessage: 'Loading models...'});
        } else if (modelsFetchError) {
            loadModelsHelpText = modelsFetchError;
        }
    }

    const selectedFetchedModel = availableModels.find((m) => m.id === props.service.defaultModel);
    const bifrostInputTokenLimit = selectedFetchedModel?.inputTokenLimit;
    const bifrostOutputTokenLimit = selectedFetchedModel?.outputTokenLimit;
    const inputAutoFromProvider = typeof bifrostInputTokenLimit === 'number';
    const outputAutoFromProvider = typeof bifrostOutputTokenLimit === 'number';
    const autoFromProviderHelpText = intl.formatMessage({defaultMessage: 'Auto-detected from provider'});

    const effectiveInputLimit = inputAutoFromProvider ? (bifrostInputTokenLimit as number) : manualInputLimit;
    const effectiveOutputLimit = outputAutoFromProvider ? (bifrostOutputTokenLimit as number) : manualOutputLimit;

    useEffect(() => {
        const inputDrift = props.service.tokenLimit !== effectiveInputLimit;
        const outputDrift = props.service.outputTokenLimit !== effectiveOutputLimit;
        if (inputDrift || outputDrift) {
            // Record our own write so the re-seed effect above doesn't treat it
            // as an external change and clobber the cached manual entries.
            lastWrittenLimits.current = {input: effectiveInputLimit, output: effectiveOutputLimit};

            // Single onChange — separate calls race when both fire on the same render.
            props.onChange({
                ...props.service,
                tokenLimit: effectiveInputLimit,
                outputTokenLimit: effectiveOutputLimit,
            });
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [effectiveInputLimit, effectiveOutputLimit]);

    return (
        <>
            <TextItem
                label={intl.formatMessage({defaultMessage: 'Service name'})}
                value={props.service.name}
                onChange={(e) => props.onChange({...props.service, name: e.target.value})}
            />
            <SelectionItem
                label={intl.formatMessage({defaultMessage: 'Service type'})}
                value={props.service.type}
                onChange={(e) => {
                    const nextType = e.target.value;
                    props.onChange({
                        ...props.service,
                        type: nextType,
                        apiKey: nextType === 'vertex' ? '' : props.service.apiKey,
                        useResponsesAPI: nextType === 'openai' ? true : props.service.useResponsesAPI,
                    });
                }}
            >
                <SelectionItemOption value='openai'>{'OpenAI'}</SelectionItemOption>
                <SelectionItemOption value='anthropic'>{'Anthropic'}</SelectionItemOption>
                <SelectionItemOption value='gemini'>{'Google Gemini'}</SelectionItemOption>
                <SelectionItemOption value='vertex'>{'Google Vertex AI'}</SelectionItemOption>
                <SelectionItemOption value='bedrock'>{'AWS Bedrock'}</SelectionItemOption>
                <SelectionItemOption value='openaicompatible'>{'OpenAI Compatible'}</SelectionItemOption>
                <SelectionItemOption value='azure'>{'Azure'}</SelectionItemOption>
                <SelectionItemOption value='cohere'>{'Cohere'}</SelectionItemOption>
                <SelectionItemOption value='mistral'>{'Mistral'}</SelectionItemOption>
                <SelectionItemOption value='scale'>{scaleAIToDisplayName(intl)}</SelectionItemOption>
                <SelectionItemOption value='asage'>{'asksage (Experimental)'}</SelectionItemOption>
            </SelectionItem>
            {(type === 'openaicompatible' || type === 'azure' || type === 'asage' || type === 'scale') && (
                <TextItem
                    label={intl.formatMessage({defaultMessage: 'API URL'})}
                    value={props.service.apiURL}
                    onChange={(e) => props.onChange({...props.service, apiURL: e.target.value})}
                    helptext={isScale ? intl.formatMessage({defaultMessage: 'Scale API endpoint (e.g., https://sgp-api.scalegov.com/v5)'}) : undefined} // eslint-disable-line no-undefined
                />
            )}
            {type === 'bedrock' && (
                <>
                    <TextItem
                        label={intl.formatMessage({defaultMessage: 'AWS Region'})}
                        value={props.service.region}
                        onChange={(e) => props.onChange({...props.service, region: e.target.value})}
                        helptext={intl.formatMessage({defaultMessage: 'AWS region where Bedrock is available (e.g., us-east-1, us-west-2)'})}
                    />
                    <TextItem
                        label={intl.formatMessage({defaultMessage: 'Custom Endpoint URL (Optional)'})}
                        value={props.service.apiURL}
                        onChange={(e) => props.onChange({...props.service, apiURL: e.target.value})}
                        helptext={intl.formatMessage({defaultMessage: 'Optional custom endpoint for VPC endpoints or proxies (e.g., https://bedrock-runtime.vpce-xxx.us-east-1.vpce.amazonaws.com)'})}
                    />
                    <TextItem
                        label={intl.formatMessage({defaultMessage: 'AWS Access Key ID (Optional)'})}
                        value={props.service.awsAccessKeyID}
                        onChange={(e) => props.onChange({...props.service, awsAccessKeyID: e.target.value})}
                        helptext={intl.formatMessage({defaultMessage: 'IAM user access key ID. If set, these credentials take precedence over API Key. Can also be set via AWS_ACCESS_KEY_ID environment variable. System console takes precedence over environment variables.'})}
                    />
                    <TextItem
                        label={intl.formatMessage({defaultMessage: 'AWS Secret Access Key (Optional)'})}
                        type='password'
                        value={props.service.awsSecretAccessKey}
                        onChange={(e) => props.onChange({...props.service, awsSecretAccessKey: e.target.value})}
                        helptext={intl.formatMessage({defaultMessage: 'IAM user secret access key. Required if AWS Access Key ID is provided. Can also be set via AWS_SECRET_ACCESS_KEY environment variable. System console takes precedence over environment variables.'})}
                    />
                </>
            )}
            {type === 'vertex' && (
                <>
                    <TextItem
                        label={intl.formatMessage({defaultMessage: 'GCP Project ID'})}
                        value={props.service.vertexProjectID}
                        onChange={(e) => props.onChange({...props.service, vertexProjectID: e.target.value})}
                        helptext={intl.formatMessage({defaultMessage: 'Your Google Cloud project ID (e.g., my-project-123)'})}
                    />
                    <TextItem
                        label={intl.formatMessage({defaultMessage: 'GCP Project Number (Optional)'})}
                        value={props.service.vertexProjectNumber}
                        onChange={(e) => props.onChange({...props.service, vertexProjectNumber: e.target.value})}
                        helptext={intl.formatMessage({defaultMessage: 'Numeric project number. Required for some Vertex endpoints, leave blank otherwise.'})}
                    />
                    <TextItem
                        label={intl.formatMessage({defaultMessage: 'GCP Region'})}
                        value={props.service.region}
                        onChange={(e) => props.onChange({...props.service, region: e.target.value})}
                        helptext={intl.formatMessage({defaultMessage: 'Vertex AI region (e.g., us-central1, europe-west4)'})}
                    />
                    <TextItem
                        label={intl.formatMessage({defaultMessage: 'Service Account JSON (Optional)'})}
                        type='password'
                        value={props.service.vertexAuthCredentials}
                        onChange={(e) => props.onChange({...props.service, vertexAuthCredentials: e.target.value})}
                        helptext={intl.formatMessage({defaultMessage: 'Paste the full service account JSON. Leave blank to use Application Default Credentials (ADC) or an attached IAM role.'})}
                    />
                </>
            )}
            {type !== 'vertex' && (
                <TextItem
                    label={intl.formatMessage({defaultMessage: 'API Key'})}
                    type='password'
                    value={props.service.apiKey}
                    onChange={(e) => props.onChange({...props.service, apiKey: e.target.value})}
                    // eslint-disable-next-line no-undefined
                    helptext={type === 'bedrock' ? intl.formatMessage({defaultMessage: 'Optional. Bedrock console API key (base64 encoded). If IAM credentials above are set, they take precedence.'}) : undefined}
                />
            )}
            {isOpenAIType && (
                <>
                    {!isCohere && !isMistral && (
                        <TextItem
                            label={isScale ? intl.formatMessage({defaultMessage: 'Account ID'}) : intl.formatMessage({defaultMessage: 'Organization ID'})}
                            value={props.service.orgId}
                            onChange={(e) => props.onChange({...props.service, orgId: e.target.value})}
                            helptext={isScale ? intl.formatMessage({defaultMessage: 'Scale Account ID (x-selected-account-id header, required for ScaleGov)'}) : undefined} // eslint-disable-line no-undefined
                        />
                    )}
                    {supportsResponsesAPIToggle && (
                        <BooleanItem
                            label={intl.formatMessage({defaultMessage: 'Use Responses API'})}
                            value={props.service.useResponsesAPI ?? false}
                            onChange={(to: boolean) => props.onChange({...props.service, useResponsesAPI: to})}
                            helpText={intl.formatMessage({defaultMessage: 'Use the new OpenAI Responses API with support for reasoning summaries and other advanced features. Disable for legacy Completions API compatibility.'})}
                        />
                    )}
                </>
            )}
            {supportsModelFetching && availableModels.length > 0 && (
                <ComboboxItem
                    label={intl.formatMessage({defaultMessage: 'Default model'})}
                    value={props.service.defaultModel}
                    options={availableModels}
                    placeholder={intl.formatMessage({defaultMessage: 'Select a model or enter custom model name'})}
                    onChange={(e) => props.onChange({...props.service, defaultModel: e.target.value})}
                    helptext={intl.formatMessage({defaultMessage: 'Select from the list or type a custom model name'})}
                    isClearable={false}
                />
            )}
            {!(supportsModelFetching && availableModels.length > 0) && (
                <TextItem
                    label={intl.formatMessage({defaultMessage: 'Default model'})}
                    value={props.service.defaultModel}
                    onChange={(e) => props.onChange({...props.service, defaultModel: e.target.value})}
                    helptext={loadModelsHelpText || (isScale ? intl.formatMessage({defaultMessage: 'Use vendor/model-name format (e.g., openai/gpt-4o). See Scale AI documentation for available models.'}) : '')}
                />
            )}
            <TextItem
                label={intl.formatMessage({defaultMessage: 'Input token limit'})}
                type='number'
                value={effectiveInputLimit.toString()}
                disabled={inputAutoFromProvider}
                helptext={inputAutoFromProvider ? autoFromProviderHelpText : ''}
                onChange={(e) => {
                    const value = parseInt(e.target.value, 10);
                    const tokenLimit = isNaN(value) ? 0 : value;
                    setManualInputLimit(tokenLimit);
                    props.onChange({...props.service, tokenLimit});
                }}
            />
            <TextItem
                label={intl.formatMessage({defaultMessage: 'Output token limit'})}
                type='number'
                value={(outputAutoFromProvider ? effectiveOutputLimit : (effectiveOutputLimit || parseInt(getDefaultOutputTokenLimit(), 10))).toString()}
                disabled={outputAutoFromProvider}
                helptext={outputAutoFromProvider ? autoFromProviderHelpText : ''}
                onChange={(e) => {
                    const value = parseInt(e.target.value, 10);
                    const outputTokenLimit = isNaN(value) ? 0 : value;
                    setManualOutputLimit(outputTokenLimit);
                    props.onChange({...props.service, outputTokenLimit});
                }}
            />
            {isOpenAIType && (
                <TextItem
                    label={intl.formatMessage({defaultMessage: 'Streaming Timeout Seconds'})}
                    type='number'
                    value={props.service.streamingTimeoutSeconds?.toString() || '0'}
                    onChange={(e) => {
                        const value = parseInt(e.target.value, 10);
                        const streamingTimeoutSeconds = isNaN(value) ? 0 : value;
                        props.onChange({...props.service, streamingTimeoutSeconds});
                    }}
                />
            )}
        </>
    );
};

type Props = {
    service: LLMService
    onChange: (service: LLMService) => void
    onDelete: () => void
}

const Service = (props: Props) => {
    const [open, setOpen] = useState(false);
    const intl = useIntl();

    return (
        <ServiceContainer>
            <HeaderContainer onClick={() => setOpen((o) => !o)}>
                <IconAI/>
                <Title>
                    <NameText>
                        {props.service.name || serviceTypeToDisplayName(intl, props.service.type)}
                    </NameText>
                    <VerticalDivider/>
                    <ServiceTypeText>{serviceTypeToDisplayName(intl, props.service.type)}</ServiceTypeText>
                    {props.service.defaultModel && (
                        <>
                            <VerticalDivider/>
                            <ServiceTypeText>{props.service.defaultModel}</ServiceTypeText>
                        </>
                    )}
                </Title>
                <Spacer/>
                <ButtonIcon
                    onClick={(e) => {
                        e.stopPropagation();
                        props.onDelete();
                    }}
                >
                    <TrashIcon/>
                </ButtonIcon>
                {open ? <ChevronUpIcon/> : <ChevronDownIcon/>}
            </HeaderContainer>
            {open && (
                <ItemListContainer>
                    <ItemList>
                        <ServiceFields
                            service={props.service}
                            onChange={props.onChange}
                        />
                    </ItemList>
                </ItemListContainer>
            )}
        </ServiceContainer>
    );
};

const ItemListContainer = styled.div`
	padding: 24px 20px;
	padding-right: 76px;
`;

const Title = styled.div`
	display: flex;
	flex-direction: row;
	align-items: center;
	gap: 8px;
`;

const NameText = styled.div`
	font-size: 14px;
	font-weight: 600;
`;

const ServiceTypeText = styled.div`
	font-size: 14px;
	font-weight: 400;
	color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const Spacer = styled.div`
	flex-grow: 1;
`;

const TrashIcon = styled(TrashCanOutlineIcon)`
	width: 16px;
	height: 16px;
	color: #D24B4E;
`;

const VerticalDivider = styled.div`
	width: 1px;
	border-left: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
	height: 24px;
`;

const ServiceContainer = styled.div`
	display: flex;
	flex-direction: column;

	border-radius: 4px;
	border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);

	&:hover {
		box-shadow: 0px 2px 3px 0px rgba(0, 0, 0, 0.08);
	}
`;

const HeaderContainer = styled.div`
	display: flex;
	flex-direction: row;
	justify-content: space-between;
	align-items: center;
	gap: 16px;
	padding: 12px 16px 12px 20px;
	cursor: pointer;
`;

export default Service;
