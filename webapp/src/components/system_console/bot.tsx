// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useEffect} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {TrashCanOutlineIcon, ChevronDownIcon, AlertOutlineIcon, ChevronUpIcon} from '@mattermost/compass-icons/components';

import IconAI from '../assets/icon_ai';
import {DangerPill} from '../pill';

import {ButtonIcon} from '../assets/buttons';

import {fetchModels} from '../../client';

import {BooleanItem, FormRow, FieldControlRow, InlineCheckbox, ItemList, SelectionItem, SelectionItemOption, TextItem, ItemLabel, HelpText, ComboboxItem} from './item';
import AvatarItem from './avatar';
import {ChannelAccessLevelItem, UserAccessLevelItem} from './llm_access';
import {LLMService} from './service';
import ReasoningConfigItem from './reasoning_config';

export enum ChannelAccessLevel {
    All = 0,
    Allow,
    Block,
    None,
}

export enum UserAccessLevel {
    All = 0,
    Allow,
    Block,
    None,
}

export type LLMBotConfig = {
    id: string
    name: string
    displayName: string
    serviceID: string
    model: string
    customInstructions: string
    enableVision: boolean
    disableTools: boolean
    channelAccessLevel: ChannelAccessLevel

    // Server sends nil Go slices as JSON null.
    channelIDs: string[] | null
    userAccessLevel: UserAccessLevel
    userIDs: string[] | null
    teamIDs: string[] | null
    enabledNativeTools?: string[] | null
    reasoningEnabled?: boolean
    reasoningEffort?: string
    thinkingBudget?: number
    structuredOutputEnabled?: boolean
}

// Component for configuring native tools (OpenAI / Anthropic / Google).
export type NativeToolsItemProps = {
    enabledTools: string[]
    onChange: (tools: string[]) => void
    provider?: 'openai' | 'anthropic' | 'google'
}

const nativeToolsWebSearchHelpText = (provider: 'openai' | 'anthropic' | 'google', intl: ReturnType<typeof useIntl>): string => {
    switch (provider) {
    case 'anthropic':
        return intl.formatMessage({defaultMessage: 'Enable Claude\'s built-in web search capability. See Anthropic\'s web search tool documentation for capabilities and pricing.'});
    case 'google':
        return intl.formatMessage({defaultMessage: 'Enable Google Search grounding via the Gemini / Vertex AI provider'});
    default:
        return intl.formatMessage({defaultMessage: 'Enable OpenAI\'s built-in web search capability'});
    }
};

const nativeToolsTitle = (provider: 'openai' | 'anthropic' | 'google', intl: ReturnType<typeof useIntl>): string => {
    switch (provider) {
    case 'anthropic':
        return intl.formatMessage({defaultMessage: 'Native Claude Tools'});
    case 'google':
        return intl.formatMessage({defaultMessage: 'Native Google Tools'});
    default:
        return intl.formatMessage({defaultMessage: 'Native OpenAI Tools'});
    }
};

type NativeToolOption = {
    id: string;
    label: string;
    helpText: string;
};

// Per-provider native tool checklists. Must stay in sync with the server-side
// support matrix (bifrost.SupportedNativeToolsForServiceType); the server
// filters unsupported ids at request time regardless of what is persisted.
// Help text describes what each toggle does in Mattermost and defers to the
// provider's own documentation for capabilities, pricing and data handling —
// those specifics change with provider tool versions.
const nativeToolOptions = (provider: 'openai' | 'anthropic' | 'google', intl: ReturnType<typeof useIntl>): NativeToolOption[] => {
    const webSearch: NativeToolOption = {
        id: 'web_search',
        label: intl.formatMessage({defaultMessage: 'Web Search'}),
        helpText: nativeToolsWebSearchHelpText(provider, intl),
    };

    switch (provider) {
    case 'anthropic':
        return [
            webSearch,
            {
                id: 'web_fetch',
                label: intl.formatMessage({defaultMessage: 'Web Fetch'}),
                helpText: intl.formatMessage({defaultMessage: 'Let the agent retrieve the full content of specific web pages and PDFs. See Anthropic\'s web fetch tool documentation for capabilities and pricing.'}),
            },
            {
                id: 'code_interpreter',
                label: intl.formatMessage({defaultMessage: 'Code Execution'}),
                helpText: intl.formatMessage({defaultMessage: 'Let the agent run code in Anthropic\'s managed sandbox. Enabling this also lets web search and web fetch filter results in the sandbox. Conversation content may be processed and retained in the sandbox; see Anthropic\'s code execution tool documentation for details and pricing.'}),
            },
        ];
    case 'google':
        return [webSearch];
    default:
        // file_search is intentionally absent: OpenAI requires vector_store_ids
        // on the tool and the plugin has no vector-store configuration yet, so
        // enabling it would break every completion for the agent.
        return [
            webSearch,
            {
                id: 'code_interpreter',
                label: intl.formatMessage({defaultMessage: 'Code Interpreter'}),
                helpText: intl.formatMessage({defaultMessage: 'Let the agent run code in OpenAI\'s managed sandbox. See OpenAI\'s code interpreter documentation for details and pricing.'}),
            },
        ];
    }
};

export const NativeToolsItem = (props: NativeToolsItemProps) => {
    const intl = useIntl();
    const provider = props.provider || 'openai';

    const availableNativeTools = nativeToolOptions(provider, intl);

    const setToolEnabled = (toolId: string, enabled: boolean) => {
        const currentTools = props.enabledTools || [];
        if (enabled) {
            if (!currentTools.includes(toolId)) {
                props.onChange([...currentTools, toolId]);
            }
            return;
        }
        props.onChange(currentTools.filter((t) => t !== toolId));
    };

    const titleMessage = nativeToolsTitle(provider, intl);

    return (
        <FormRow>
            <ItemLabel>
                {titleMessage}
            </ItemLabel>
            <NativeToolsColumn>
                {availableNativeTools.map((tool) => (
                    <NativeToolField key={tool.id}>
                        <FieldControlRow>
                            <InlineCheckbox
                                testId={`native-tool-${tool.id}`}
                                label={tool.label}
                                checked={(props.enabledTools || []).includes(tool.id)}
                                onChange={(checked) => setToolEnabled(tool.id, checked)}
                            />
                        </FieldControlRow>
                        <NativeToolHelpText>{tool.helpText}</NativeToolHelpText>
                    </NativeToolField>
                ))}
            </NativeToolsColumn>
        </FormRow>
    );
};

type Props = {
    bot: LLMBotConfig
    services: LLMService[]
    onChange: (bot: LLMBotConfig) => void
    onDelete: () => void
    changedAvatar: (image: File) => void
}

type ModelInfo = {
    id: string
    displayName: string
}

const Bot = (props: Props) => {
    const [open, setOpen] = useState(false);
    const intl = useIntl();
    const [availableModels, setAvailableModels] = useState<ModelInfo[]>([]);
    const [loadingModels, setLoadingModels] = useState(false);
    const [modelsFetchError, setModelsFetchError] = useState<string>('');

    const missingUsername = !props.bot.name || props.bot.name.trim() === '';
    const invalidUsername = props.bot.name !== '' && (!(/^[a-z0-9.\-_]+$/).test(props.bot.name) || !(/[a-z]/).test(props.bot.name.charAt(0)));
    const missingService = !props.bot.serviceID || !props.services.find((s) => s.id === props.bot.serviceID);

    // Find the selected service
    const selectedService = props.services.find((s) => s.id === props.bot.serviceID);
    const supportsModelFetching = selectedService &&
        (selectedService.type === 'anthropic' ||
         selectedService.type === 'openai' ||
         selectedService.type === 'azure' ||
         selectedService.type === 'openaicompatible' ||
         selectedService.type === 'gemini' ||
         selectedService.type === 'vertex');

    // Fetch models when the service changes
    useEffect(() => {
        if (!supportsModelFetching || !selectedService) {
            setAvailableModels([]);
            setModelsFetchError('');
            return;
        }

        // Providers have different credential shapes for model listing:
        // - openaicompatible: API key OR API URL
        // - vertex: GCP project ID + region
        // - others: API key
        let hasRequiredCredentials: string | boolean = false;
        switch (selectedService.type) {
        case 'openaicompatible':
            hasRequiredCredentials = selectedService.apiKey || selectedService.apiURL;
            break;
        case 'vertex':
            hasRequiredCredentials = Boolean(selectedService.vertexProjectID && selectedService.region);
            break;
        default:
            hasRequiredCredentials = selectedService.apiKey;
        }

        if (!hasRequiredCredentials) {
            setAvailableModels([]);
            setModelsFetchError('');
            return;
        }

        const loadModels = async () => {
            setLoadingModels(true);
            setModelsFetchError('');

            try {
                const data: ModelInfo[] = await fetchModels(
                    selectedService.type,
                    selectedService.apiKey,
                    selectedService.apiURL || '',
                    selectedService.orgId || '',
                    {
                        region: selectedService.region || '',
                        vertexProjectID: selectedService.vertexProjectID || '',
                        vertexProjectNumber: selectedService.vertexProjectNumber || '',
                        vertexAuthCredentials: selectedService.vertexAuthCredentials || '',
                    },
                );
                setAvailableModels(data);
            } catch (error) {
                setModelsFetchError(intl.formatMessage({defaultMessage: 'Failed to fetch models. Please check the service configuration.'}));
                setAvailableModels([]);
            } finally {
                setLoadingModels(false);
            }
        };

        loadModels();
    }, [selectedService?.id, selectedService?.type, selectedService?.apiKey, selectedService?.apiURL, selectedService?.orgId, selectedService?.region, selectedService?.vertexProjectID, selectedService?.vertexProjectNumber, selectedService?.vertexAuthCredentials, supportsModelFetching, intl]);

    return (
        <BotContainer>
            <HeaderContainer onClick={() => setOpen((o) => !o)}>
                <IconAI/>
                <Title>
                    <NameText>
                        {props.bot.displayName}
                    </NameText>
                </Title>
                <Spacer/>
                {missingService && (
                    <DangerPill>
                        <AlertOutlineIcon/>
                        <FormattedMessage defaultMessage='No Service Selected'/>
                    </DangerPill>
                )}
                {missingUsername && (
                    <DangerPill>
                        <AlertOutlineIcon/>
                        <FormattedMessage defaultMessage='No Username'/>
                    </DangerPill>
                )}
                {invalidUsername && (
                    <DangerPill>
                        <AlertOutlineIcon/>
                        <FormattedMessage defaultMessage='Invalid Username'/>
                    </DangerPill>
                )}
                <ButtonIcon
                    onClick={props.onDelete}
                >
                    <TrashIcon/>
                </ButtonIcon>
                {open ? <ChevronUpIcon/> : <ChevronDownIcon/>}
            </HeaderContainer>
            {open && (
                <ItemListContainer>
                    <ItemList>
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Display name'})}
                            value={props.bot.displayName}
                            onChange={(e) => props.onChange({...props.bot, displayName: e.target.value})}
                        />
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Bot Username'})}
                            helptext={intl.formatMessage({defaultMessage: 'Team members can mention this bot with this username'})}
                            maxLength={22}
                            value={props.bot.name}
                            onChange={(e) => props.onChange({...props.bot, name: e.target.value})}
                        />
                        <AvatarItem
                            botusername={props.bot.name}
                            avatarOwnerKey={props.bot.id}
                            changedAvatar={props.changedAvatar}
                        />
                        <SelectionItem
                            label={intl.formatMessage({defaultMessage: 'AI Service'})}
                            value={props.bot.serviceID}
                            onChange={(e) => props.onChange({...props.bot, serviceID: e.target.value})}
                        >
                            <SelectionItemOption value=''>
                                {intl.formatMessage({defaultMessage: 'Select a service'})}
                            </SelectionItemOption>
                            {props.services.map((service) => (
                                <SelectionItemOption
                                    key={service.id}
                                    value={service.id}
                                >
                                    {service.name || service.type}
                                </SelectionItemOption>
                            ))}
                        </SelectionItem>
                        {supportsModelFetching && availableModels.length > 0 ? (
                            <ComboboxItem
                                label={intl.formatMessage({defaultMessage: 'Model'})}
                                value={props.bot.model}
                                options={availableModels}
                                placeholder={intl.formatMessage({defaultMessage: 'Use service default'})}
                                onChange={(value) => props.onChange({...props.bot, model: value})}
                                helptext={intl.formatMessage({defaultMessage: 'Optional: Override the service\'s default model for this agent. Select from the list or type a custom model name.'})}
                            />
                        ) : (
                            <TextItem
                                label={intl.formatMessage({defaultMessage: 'Model'})}
                                helptext={(() => {
                                    if (supportsModelFetching && loadingModels) {
                                        return intl.formatMessage({defaultMessage: 'Loading models...'});
                                    }
                                    if (supportsModelFetching && modelsFetchError) {
                                        return modelsFetchError;
                                    }
                                    return intl.formatMessage({defaultMessage: 'Optional: Override the service\'s default model for this agent. Leave empty to use the service default.'});
                                })()}
                                placeholder={intl.formatMessage({defaultMessage: 'Leave empty to use service default'})}
                                value={props.bot.model}
                                onChange={(e) => props.onChange({...props.bot, model: e.target.value})}
                            />
                        )}
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Custom instructions'})}
                            placeholder={intl.formatMessage({defaultMessage: 'How would you like the AI to respond?'})}
                            multiline={true}
                            value={props.bot.customInstructions}
                            onChange={(e) => props.onChange({...props.bot, customInstructions: e.target.value})}
                        />
                        {(() => {
                            const selectedService = props.services.find((s) => s.id === props.bot.serviceID);
                            const supportsVisionAndTools = selectedService &&
                                ['openai', 'openaicompatible', 'azure', 'anthropic', 'cohere', 'mistral', 'gemini', 'vertex'].includes(selectedService.type);

                            if (!supportsVisionAndTools) {
                                return null;
                            }

                            return (
                                <>
                                    <BooleanItem
                                        label={intl.formatMessage({defaultMessage: 'Enable Vision'})}
                                        value={props.bot.enableVision}
                                        onChange={(to: boolean) => props.onChange({...props.bot, enableVision: to})}
                                        helpText={intl.formatMessage({defaultMessage: 'Enable Vision to allow the bot to process images. Requires a compatible model.'})}
                                    />
                                    <BooleanItem
                                        label={intl.formatMessage({defaultMessage: 'Enable Tools'})}
                                        value={!props.bot.disableTools}
                                        onChange={(to: boolean) => props.onChange({...props.bot, disableTools: !to})}
                                        helpText={intl.formatMessage({defaultMessage: 'By default some tool use is enabled to allow for features such as integrations with JIRA. Disabling this allows use of models that do not support or are not very good at tool use. Some features will not work without tools.'})}
                                    />
                                    {(() => {
                                        // Direct OpenAI always uses the Responses API. OpenAI-compatible
                                        // and Azure only expose native tools when their toggle is enabled.
                                        const isAnthropic = selectedService.type === 'anthropic';
                                        const isGoogle = selectedService.type === 'gemini' || selectedService.type === 'vertex';
                                        const isOpenAIWithResponses =
                                            selectedService.type === 'openai' ||
                                            (['openaicompatible', 'azure'].includes(selectedService.type) && selectedService.useResponsesAPI);

                                        if (isAnthropic) {
                                            return (
                                                <NativeToolsItem
                                                    enabledTools={props.bot.enabledNativeTools || []}
                                                    onChange={(tools: string[]) => props.onChange({...props.bot, enabledNativeTools: tools})}
                                                    provider='anthropic'
                                                />
                                            );
                                        }

                                        if (isGoogle) {
                                            return (
                                                <NativeToolsItem
                                                    enabledTools={props.bot.enabledNativeTools || []}
                                                    onChange={(tools: string[]) => props.onChange({...props.bot, enabledNativeTools: tools})}
                                                    provider='google'
                                                />
                                            );
                                        }

                                        if (isOpenAIWithResponses) {
                                            return (
                                                <NativeToolsItem
                                                    enabledTools={props.bot.enabledNativeTools || []}
                                                    onChange={(tools: string[]) => props.onChange({...props.bot, enabledNativeTools: tools})}
                                                    provider='openai'
                                                />
                                            );
                                        }

                                        return null;
                                    })()}
                                    <ReasoningConfigItem
                                        bot={props.bot}
                                        service={selectedService}
                                        maxTokens={selectedService?.outputTokenLimit || 4096}
                                        onChange={props.onChange}
                                    />
                                    {(selectedService.type === 'anthropic' || ['openai', 'openaicompatible', 'azure'].includes(selectedService.type)) && (
                                        <BooleanItem
                                            label={intl.formatMessage({defaultMessage: 'Structured Output'})}
                                            value={props.bot.structuredOutputEnabled ?? false}
                                            onChange={(to: boolean) => props.onChange({...props.bot, structuredOutputEnabled: to})}
                                            helpText={selectedService.type === 'anthropic' ?
                                                intl.formatMessage({defaultMessage: 'Enable structured JSON output for this bot. When enabled and a JSON schema is provided in the request, the model will produce valid JSON matching the schema. Requires a compatible Anthropic model (Claude 4.5/4.6+). Note: Requests that ask for structured JSON output will skip extended thinking; all other requests keep using it.'}) :
                                                intl.formatMessage({defaultMessage: 'Enable structured JSON output for this bot. When enabled and a JSON schema is provided in the request, the model will produce valid JSON matching the schema.'})
                                            }
                                        />
                                    )}
                                </>
                            );
                        })()}
                        <ChannelAccessLevelItem
                            label={intl.formatMessage({defaultMessage: 'Channel access'})}
                            level={props.bot.channelAccessLevel ?? ChannelAccessLevel.All}
                            onChangeLevel={(to: ChannelAccessLevel) => props.onChange({...props.bot, channelAccessLevel: to})}
                            channelIDs={props.bot.channelIDs ?? []}
                            onChangeChannelIDs={(channels: string[]) => props.onChange({...props.bot, channelIDs: channels})}
                        />
                        <UserAccessLevelItem
                            label={intl.formatMessage({defaultMessage: 'User access'})}
                            level={props.bot.userAccessLevel ?? ChannelAccessLevel.All}
                            onChangeLevel={(to: UserAccessLevel) => props.onChange({...props.bot, userAccessLevel: to})}
                            userIDs={props.bot.userIDs ?? []}
                            teamIDs={props.bot.teamIDs ?? []}
                            onChangeIDs={(userIds: string[], teamIds: string[]) => props.onChange({...props.bot, userIDs: userIds, teamIDs: teamIds})}
                        />

                    </ItemList>
                </ItemListContainer>
            )}
        </BotContainer>
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

const Spacer = styled.div`
	flex-grow: 1;
`;

const TrashIcon = styled(TrashCanOutlineIcon)`
	width: 16px;
	height: 16px;
	color: #D24B4E;
`;

const BotContainer = styled.div`
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
	border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
	cursor: pointer;
`;

const NativeToolsColumn = styled.div`
	display: flex;
	flex-direction: column;
	gap: 12px;
`;

const NativeToolField = styled.div`
	display: flex;
	flex-direction: column;
	gap: 0;
`;

const NativeToolHelpText = styled(HelpText)`
	padding-left: 0;
`;

export default Bot;
