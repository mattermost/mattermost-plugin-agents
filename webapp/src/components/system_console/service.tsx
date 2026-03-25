// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useEffect} from 'react';
import styled from 'styled-components';
import {useIntl} from 'react-intl';

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
    sendUserId: boolean
    outputTokenLimit: number
    useResponsesAPI: boolean
    region: string
    awsAccessKeyID: string
    awsSecretAccessKey: string
    bifrostKeyJSON: string
    bifrostProviderConfigJSON: string
}

type ModelInfo = {
    id: string
    displayName: string
}

export type ServiceTypeInfo = {
    id: string
    displayName: string
}

type ServiceFieldsProps = {
    service: LLMService
    serviceTypes: ServiceTypeInfo[]
    onChange: (service: LLMService) => void
}

const ServiceFields = (props: ServiceFieldsProps) => {
    const type = props.service.type;
    const intl = useIntl();
    const isOpenAIFamily = type === 'openai' || type === 'openaicompatible' || type === 'azure';
    const isBedrock = type === 'bedrock';
    const showAPIURLField = type !== '' && !isBedrock;

    const [availableModels, setAvailableModels] = useState<ModelInfo[]>([]);
    const [loadingModels, setLoadingModels] = useState(false);
    const [modelsFetchError, setModelsFetchError] = useState<string>('');

    useEffect(() => {
        const canAttemptModelFetch = Boolean(
            props.service.type &&
            (
                props.service.apiKey ||
                props.service.apiURL ||
                props.service.region ||
                props.service.awsAccessKeyID ||
                props.service.awsSecretAccessKey ||
                props.service.bifrostKeyJSON ||
                props.service.bifrostProviderConfigJSON
            ),
        );

        if (!canAttemptModelFetch) {
            setAvailableModels([]);
            setModelsFetchError('');
            return;
        }

        const loadModels = async () => {
            setLoadingModels(true);
            setModelsFetchError('');

            try {
                const data: ModelInfo[] = await fetchModels(props.service);
                setAvailableModels(data);
            } catch (error) {
                setModelsFetchError(intl.formatMessage({defaultMessage: 'Failed to fetch models. Please check the service configuration or use advanced Bifrost configuration.'}));
                setAvailableModels([]);
            } finally {
                setLoadingModels(false);
            }
        };

        loadModels();
    }, [
        props.service,
        intl,
    ]);

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

    const serviceTypeMap = new Map(props.serviceTypes.map((serviceType) => [serviceType.id, serviceType.displayName]));
    const serviceTypeOptions = props.service.type && !serviceTypeMap.has(props.service.type) ?
        [{id: props.service.type, displayName: props.service.type}, ...props.serviceTypes] :
        props.serviceTypes;

    let loadModelsHelpText = '';
    if (loadingModels) {
        loadModelsHelpText = intl.formatMessage({defaultMessage: 'Loading models...'});
    } else if (modelsFetchError) {
        loadModelsHelpText = modelsFetchError;
    }

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
                        useResponsesAPI: nextType === 'openai' ? true : props.service.useResponsesAPI,
                    });
                }}
            >
                {serviceTypeOptions.map((serviceType) => (
                    <SelectionItemOption
                        key={serviceType.id}
                        value={serviceType.id}
                    >
                        {serviceType.displayName}
                    </SelectionItemOption>
                ))}
            </SelectionItem>
            {showAPIURLField && (
                <TextItem
                    label={intl.formatMessage({defaultMessage: 'API URL'})}
                    value={props.service.apiURL}
                    onChange={(e) => props.onChange({...props.service, apiURL: e.target.value})}
                    helptext={type === 'openaicompatible' ?
                        intl.formatMessage({defaultMessage: 'Endpoint for your OpenAI-compatible API (for example http://localhost:11434/v1 for Ollama).'}) :
                        ''} // eslint-disable-line no-undefined
                />
            )}
            {isBedrock && (
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
            <TextItem
                label={intl.formatMessage({defaultMessage: 'API Key'})}
                type='password'
                value={props.service.apiKey}
                onChange={(e) => props.onChange({...props.service, apiKey: e.target.value})}
                // eslint-disable-next-line no-undefined
                helptext={type === 'bedrock' ? intl.formatMessage({defaultMessage: 'Optional. Bedrock console API key (base64 encoded). If IAM credentials above are set, they take precedence.'}) : undefined}
            />
            {(type === 'openai' || type === 'openaicompatible') && (
                <>
                    <TextItem
                        label={intl.formatMessage({defaultMessage: 'Organization ID'})}
                        value={props.service.orgId}
                        onChange={(e) => props.onChange({...props.service, orgId: e.target.value})}
                    />
                </>
            )}
            {isOpenAIFamily && (
                <>
                    <BooleanItem
                        label={intl.formatMessage({defaultMessage: 'Send User ID'})}
                        value={props.service.sendUserId}
                        onChange={(to: boolean) => props.onChange({...props.service, sendUserId: to})}
                        helpText={intl.formatMessage({defaultMessage: 'Sends the Mattermost user ID to the upstream LLM.'})}
                    />
                    <BooleanItem
                        label={intl.formatMessage({defaultMessage: 'Use Responses API'})}
                        value={props.service.useResponsesAPI ?? false}
                        onChange={(to: boolean) => props.onChange({...props.service, useResponsesAPI: to})}
                        helpText={intl.formatMessage({defaultMessage: 'Use the new OpenAI Responses API with support for reasoning summaries and other advanced features. Disable for legacy Completions API compatibility.'})}
                    />
                </>
            )}
            {availableModels.length > 0 && (
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
            {availableModels.length === 0 && (
                <TextItem
                    label={intl.formatMessage({defaultMessage: 'Default model'})}
                    value={props.service.defaultModel}
                    onChange={(e) => props.onChange({...props.service, defaultModel: e.target.value})}
                    helptext={loadModelsHelpText || intl.formatMessage({defaultMessage: 'Enter a model name manually or configure the service so model discovery can succeed.'})}
                />
            )}
            <TextItem
                label={intl.formatMessage({defaultMessage: 'Input token limit'})}
                type='number'
                value={props.service.tokenLimit.toString()}
                onChange={(e) => {
                    const value = parseInt(e.target.value, 10);
                    const tokenLimit = isNaN(value) ? 0 : value;
                    props.onChange({...props.service, tokenLimit});
                }}
            />
            <TextItem
                label={intl.formatMessage({defaultMessage: 'Output token limit'})}
                type='number'
                value={props.service.outputTokenLimit?.toString() || getDefaultOutputTokenLimit()}
                onChange={(e) => {
                    const value = parseInt(e.target.value, 10);
                    const outputTokenLimit = isNaN(value) ? 0 : value;
                    props.onChange({...props.service, outputTokenLimit});
                }}
            />
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
            <TextItem
                label={intl.formatMessage({defaultMessage: 'Advanced Bifrost Key JSON'})}
                value={props.service.bifrostKeyJSON || ''}
                multiline={true}
                onChange={(e) => props.onChange({...props.service, bifrostKeyJSON: e.target.value})}
                helptext={intl.formatMessage({defaultMessage: 'Optional advanced Bifrost key configuration JSON for providers that need provider-specific auth fields (for example Vertex, vLLM, Hugging Face, or Replicate).'})}
            />
            <TextItem
                label={intl.formatMessage({defaultMessage: 'Advanced Bifrost Provider Config JSON'})}
                value={props.service.bifrostProviderConfigJSON || ''}
                multiline={true}
                onChange={(e) => props.onChange({...props.service, bifrostProviderConfigJSON: e.target.value})}
                helptext={intl.formatMessage({defaultMessage: 'Optional advanced Bifrost provider configuration JSON. Use this to override network settings or provide advanced provider-specific configuration without waiting for plugin changes.'})}
            />
        </>
    );
};

type Props = {
    service: LLMService
    serviceTypes: ServiceTypeInfo[]
    onChange: (service: LLMService) => void
    onDelete: () => void
}

const Service = (props: Props) => {
    const [open, setOpen] = useState(false);
    const serviceTypeMap = new Map(props.serviceTypes.map((serviceType) => [serviceType.id, serviceType.displayName]));

    return (
        <ServiceContainer>
            <HeaderContainer onClick={() => setOpen((o) => !o)}>
                <IconAI/>
                <Title>
                    <NameText>
                        {props.service.name || serviceTypeMap.get(props.service.type) || props.service.type}
                    </NameText>
                    <VerticalDivider/>
                    <ServiceTypeText>{serviceTypeMap.get(props.service.type) || props.service.type}</ServiceTypeText>
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
                            serviceTypes={props.serviceTypes}
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
