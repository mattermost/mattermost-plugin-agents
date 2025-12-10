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
}

const mapServiceTypeToDisplayName = new Map<string, string>([
    ['openai', 'OpenAI'],
    ['openaicompatible', 'OpenAI Compatible'],
    ['azure', 'Azure'],
    ['anthropic', 'Anthropic'],
    ['cohere', 'Cohere'],
    ['mistral', 'Mistral'],
    ['asage', 'asksage (Experimental)'],
]);

function serviceTypeToDisplayName(serviceType: string): string {
    return mapServiceTypeToDisplayName.get(serviceType) || serviceType;
}

type ModelInfo = {
    id: string
    displayName: string
}

type ServiceFieldsProps = {
    service: LLMService
    onChange: (service: LLMService) => void
}

const ServiceFields = (props: ServiceFieldsProps) => {
    const type = props.service.type;
    const intl = useIntl();
    const isOpenAIType = type === 'openai' || type === 'openaicompatible' || type === 'azure' || type === 'cohere' || type === 'mistral';
    const isCohere = type === 'cohere';
    const isMistral = type === 'mistral';

    const [availableModels, setAvailableModels] = useState<ModelInfo[]>([]);
    const [loadingModels, setLoadingModels] = useState(false);
    const [modelsFetchError, setModelsFetchError] = useState<string>('');

    // Determine if we should support model fetching for this service type
    const supportsModelFetching = type === 'anthropic' || type === 'openai' || type === 'azure' || type === 'openaicompatible';

    // Fetch models when API key or URL changes for supported service types
    useEffect(() => {
        // For openaicompatible, API key is optional if there's an API URL
        // For other types, API key is required
        const hasRequiredCredentials = type === 'openaicompatible' ? (props.service.apiKey || props.service.apiURL) : props.service.apiKey;

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
    }, [type, props.service.apiKey, props.service.apiURL, props.service.orgId, supportsModelFetching, intl]);

    const getDefaultOutputTokenLimit = () => {
        switch (type) {
        case 'anthropic':
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
                onChange={(e) => props.onChange({...props.service, type: e.target.value})}
            >
                <SelectionItemOption value='openai'>{'OpenAI'}</SelectionItemOption>
                <SelectionItemOption value='anthropic'>{'Anthropic'}</SelectionItemOption>
                <SelectionItemOption value='openaicompatible'>{'OpenAI Compatible'}</SelectionItemOption>
                <SelectionItemOption value='azure'>{'Azure'}</SelectionItemOption>
                <SelectionItemOption value='cohere'>{'Cohere'}</SelectionItemOption>
                <SelectionItemOption value='mistral'>{'Mistral'}</SelectionItemOption>
                <SelectionItemOption value='asage'>{'asksage (Experimental)'}</SelectionItemOption>
            </SelectionItem>
            {(type === 'openaicompatible' || type === 'azure' || type === 'asage') && (
                <TextItem
                    label={intl.formatMessage({defaultMessage: 'API URL'})}
                    value={props.service.apiURL}
                    onChange={(e) => props.onChange({...props.service, apiURL: e.target.value})}
                />
            )}
            <TextItem
                label={intl.formatMessage({defaultMessage: 'API Key'})}
                type='password'
                value={props.service.apiKey}
                onChange={(e) => props.onChange({...props.service, apiKey: e.target.value})}
            />
            {isOpenAIType && (
                <>
                    {!isCohere && !isMistral && (
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Organization ID'})}
                            value={props.service.orgId}
                            onChange={(e) => props.onChange({...props.service, orgId: e.target.value})}
                        />
                    )}
                    <BooleanItem
                        label={intl.formatMessage({defaultMessage: 'Send User ID'})}
                        value={props.service.sendUserId}
                        onChange={(to: boolean) => props.onChange({...props.service, sendUserId: to})}
                        helpText={intl.formatMessage({defaultMessage: 'Sends the Mattermost user ID to the upstream LLM.'})}
                    />
                    {(type === 'openai' || type === 'openaicompatible' || type === 'azure') && (
                        <BooleanItem
                            label={intl.formatMessage({defaultMessage: 'Use Responses API'})}
                            value={props.service.useResponsesAPI ?? false}
                            onChange={(to: boolean) => props.onChange({...props.service, useResponsesAPI: to})}
                            helpText={intl.formatMessage({defaultMessage: 'Use the new OpenAI Responses API with support for reasoning summaries and other advanced features. Disable for legacy Completions API compatibility.'})}
                        />
                    )}
                </>
            )}
            {supportsModelFetching && availableModels.length > 0 ? (
                <ComboboxItem
                    label={intl.formatMessage({defaultMessage: 'Default model'})}
                    value={props.service.defaultModel}
                    options={availableModels}
                    placeholder={intl.formatMessage({defaultMessage: 'Select a model or enter custom model name'})}
                    onChange={(e) => props.onChange({...props.service, defaultModel: e.target.value})}
                    helptext={intl.formatMessage({defaultMessage: 'Select from the list or type a custom model name'})}
                    isClearable={false}
                />
            ) : (
                <TextItem
                    label={intl.formatMessage({defaultMessage: 'Default model'})}
                    value={props.service.defaultModel}
                    onChange={(e) => props.onChange({...props.service, defaultModel: e.target.value})}
                    helptext={loadModelsHelpText}
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

    return (
        <ServiceContainer>
            <HeaderContainer onClick={() => setOpen((o) => !o)}>
                <IconAI/>
                <Title>
                    <NameText>
                        {props.service.name || serviceTypeToDisplayName(props.service.type)}
                    </NameText>
                    <VerticalDivider/>
                    <ServiceTypeText>{serviceTypeToDisplayName(props.service.type)}</ServiceTypeText>
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
