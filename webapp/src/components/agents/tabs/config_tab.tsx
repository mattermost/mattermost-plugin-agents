// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useMemo, useRef, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {ChevronDownIcon, ChevronRightIcon} from '@mattermost/compass-icons/components';

import {fetchModelsForAgentService} from '@/client';
import {ServiceInfo} from '@/types/agents';
import {
    BooleanItem,
    ComboboxItem,
    FormRow,
    FieldControlRow,
    InlineCheckbox,
    ItemLabel,
    HelpText,
    ItemList,
    TextItem,
    SelectionItem,
    SelectionItemOption,
    TextFieldContainer,
} from '@/components/system_console/item';
import AvatarItem from '@/components/system_console/avatar';
import {
    LLMBotConfig,
    NativeToolsItem,
} from '@/components/system_console/bot';
import {IntItem} from '@/components/system_console/number_items';
import ReasoningConfigItem from '@/components/system_console/reasoning_config';
import {LLMService} from '@/components/system_console/service';

import {AgentDraft, DefaultMaxToolTurns, MaxAllowedMaxToolTurns} from '../agent_config_view';

type Props = {
    draft: AgentDraft;
    onChange: (updates: Partial<AgentDraft>) => void;
    onAvatarChange: (file: File | null) => void;
    botUserId?: string;
    services: ServiceInfo[];
    errors?: Record<string, string>;

    /** When true (e.g. edit mode), username cannot be changed; matches API behavior. */
    usernameLocked?: boolean;
}

// Keep in sync with legacy System Console bot form (webapp/src/components/system_console/bot.tsx).
const visionToolServiceTypes = ['openai', 'openaicompatible', 'azure', 'anthropic', 'cohere', 'mistral', 'gemini', 'vertex'];
const openAIStructuredOutputServiceTypes = ['openai', 'openaicompatible', 'azure'];

const ConfigTab = (props: Props) => {
    const {draft, onChange, onAvatarChange, services, errors = {}, usernameLocked = false} = props;
    const intl = useIntl();
    const [advancedExpanded, setAdvancedExpanded] = useState(false);
    const [availableModels, setAvailableModels] = useState<{id: string; displayName: string}[]>([]);

    useEffect(() => {
        if (errors.maxToolTurns) {
            setAdvancedExpanded(true);
        }
    }, [errors.maxToolTurns]);
    const [loadingModels, setLoadingModels] = useState(false);
    const [modelsFetchError, setModelsFetchError] = useState('');

    /** Captures `reasoningEnabled` before turning structured output on so we can restore it when structured output is turned off. */
    const reasoningBeforeStructuredRef = useRef<boolean | null>(null);
    const prevServiceIdRef = useRef<string | null>(null);

    useEffect(() => {
        reasoningBeforeStructuredRef.current = null;
    }, [draft.serviceId]);

    // Reset provider-specific fields when the AI service changes (avoid stale model / native tools / reasoning).
    // Skip when `prev` is empty: the edit modal can render once with a stale empty draft before `agentToDraft`
    // runs, then hydrate the real `serviceId`. Treating that as a "service change" would incorrectly wipe
    // `enabledNativeTools` and other fields loaded from the agent.
    // When only switching between services with the same `type` (e.g. two OpenAI-compatible entries), keep
    // reasoning/thinking/structured-output fields so users can compare services without losing migrated values.
    useEffect(() => {
        const prev = prevServiceIdRef.current;
        if (prev !== null && prev !== '' && prev !== draft.serviceId) {
            const prevSvc = services.find((s) => s.id === prev);
            const nextSvc = services.find((s) => s.id === draft.serviceId);
            const sameServiceType = Boolean(prevSvc && nextSvc && prevSvc.type === nextSvc.type);
            onChange({
                model: '',
                ...(sameServiceType ?
                    {} :
                    {
                        enabledNativeTools: ['web_search'],
                        reasoningEnabled: true,
                        reasoningEffort: 'medium',
                        thinkingBudget: 0,
                        structuredOutputEnabled: false,
                    }),
            });
        }
        prevServiceIdRef.current = draft.serviceId;
    }, [draft.serviceId, onChange, services]);

    const selectedService = services.find((s) => s.id === draft.serviceId);

    const supportsModelFetching = Boolean(selectedService &&
        (selectedService.type === 'anthropic' ||
         selectedService.type === 'openai' ||
         selectedService.type === 'azure' ||
         selectedService.type === 'openaicompatible' ||
         selectedService.type === 'gemini' ||
         selectedService.type === 'vertex'));

    const selectedServiceAsLLM: LLMService | null = useMemo(() => {
        if (!selectedService) {
            return null;
        }
        return {
            id: selectedService.id,
            name: selectedService.name,
            type: selectedService.type,
            apiURL: '',
            apiKey: '',
            orgId: '',
            defaultModel: selectedService.defaultModel,
            tokenLimit: 0,
            streamingTimeoutSeconds: 0,
            outputTokenLimit: selectedService.outputTokenLimit || 4096,
            useResponsesAPI: selectedService.type === 'openai' ? true : selectedService.useResponsesAPI,
            region: '',
            awsAccessKeyID: '',
            awsSecretAccessKey: '',
            vertexProjectID: '',
            vertexProjectNumber: '',
            vertexAuthCredentials: '',
        };
    }, [selectedService]);

    const reasoningBot: LLMBotConfig = useMemo(() => ({
        id: 'draft',
        name: draft.username,
        displayName: draft.displayName,
        serviceID: draft.serviceId,
        model: draft.model,
        customInstructions: draft.customInstructions,
        enableVision: draft.enableVision,
        disableTools: draft.disableTools,
        channelAccessLevel: draft.channelAccessLevel,
        channelIDs: draft.channelIds,
        userAccessLevel: draft.userAccessLevel,
        userIDs: draft.userIds,
        teamIDs: draft.teamIds,
        enabledNativeTools: draft.enabledNativeTools,
        reasoningEnabled: draft.reasoningEnabled,
        reasoningEffort: draft.reasoningEffort,
        thinkingBudget: draft.thinkingBudget,
        structuredOutputEnabled: draft.structuredOutputEnabled,
    }), [draft]);

    useEffect(() => {
        if (!supportsModelFetching || !draft.serviceId) {
            setAvailableModels([]);
            setModelsFetchError('');
            setLoadingModels(false);
        }
    }, [supportsModelFetching, draft.serviceId]);

    useEffect(() => {
        if (!supportsModelFetching || !draft.serviceId) {
            return () => {
                // No fetch in flight
            };
        }

        const ac = new AbortController();
        let stale = false;

        const loadModels = async () => {
            setLoadingModels(true);
            setModelsFetchError('');
            try {
                const data = await fetchModelsForAgentService(draft.serviceId, ac.signal);
                if (!stale) {
                    setAvailableModels(data || []);
                }
            } catch {
                if (!stale && !ac.signal.aborted) {
                    setModelsFetchError(intl.formatMessage({defaultMessage: 'Failed to fetch models. Please check the service configuration.'}));
                    setAvailableModels([]);
                }
            } finally {
                if (!stale && !ac.signal.aborted) {
                    setLoadingModels(false);
                }
            }
        };

        loadModels();
        return () => {
            stale = true;
            ac.abort();
        };
    }, [draft.serviceId, supportsModelFetching, intl]);

    const supportsVisionAndTools = selectedService &&
        visionToolServiceTypes.includes(selectedService.type);

    const isAnthropic = selectedService?.type === 'anthropic';
    const isGoogle = selectedService?.type === 'gemini' || selectedService?.type === 'vertex';
    const isOpenAIWithResponses = Boolean(selectedService &&
        (selectedService.type === 'openai' ||
         (['openaicompatible', 'azure'].includes(selectedService.type) && selectedService.useResponsesAPI)));
    const supportsStructuredOutput = Boolean(selectedService &&
        (isAnthropic || openAIStructuredOutputServiceTypes.includes(selectedService.type)));

    const maxTokens = selectedService?.outputTokenLimit || 4096;
    const serviceDefaultModel = selectedService?.defaultModel?.trim() || '';

    const modelPlaceholder = serviceDefaultModel ?
        intl.formatMessage({defaultMessage: 'Default: {model}'}, {model: serviceDefaultModel}) :
        intl.formatMessage({defaultMessage: 'Leave empty to use service default'});

    const modelHelptext = (() => {
        if (supportsModelFetching && loadingModels) {
            return intl.formatMessage({defaultMessage: 'Loading models...'});
        }
        if (supportsModelFetching && modelsFetchError) {
            return modelsFetchError;
        }
        if (serviceDefaultModel) {
            return intl.formatMessage(
                {defaultMessage: 'Optional: Override the service\'s default model ({defaultModel}) for this agent. Leave empty to use the service default.'},
                {defaultModel: serviceDefaultModel},
            );
        }
        return intl.formatMessage({defaultMessage: 'Optional: Override the service\'s default model for this agent. Leave empty to use the service default.'});
    })();

    const comboboxModelHelptext = serviceDefaultModel ?
        intl.formatMessage(
            {defaultMessage: 'Optional: Override the service\'s default model ({defaultModel}) for this agent. Select from the list or type a custom model name.'},
            {defaultModel: serviceDefaultModel},
        ) :
        intl.formatMessage({defaultMessage: 'Optional: Override the service\'s default model for this agent. Select from the list or type a custom model name.'});

    const comboboxModelPlaceholder = serviceDefaultModel ?
        intl.formatMessage({defaultMessage: 'Default: {model}'}, {model: serviceDefaultModel}) :
        intl.formatMessage({defaultMessage: 'Use service default'});

    const handleReasoningBotChange = (bot: LLMBotConfig) => {
        const re = bot.reasoningEnabled ?? true;
        let structured = bot.structuredOutputEnabled ?? false;
        if (re && structured) {
            structured = false;
            reasoningBeforeStructuredRef.current = null;
        }
        onChange({
            reasoningEnabled: re,
            reasoningEffort: bot.reasoningEffort || 'medium',
            thinkingBudget: bot.thinkingBudget ?? 0,
            structuredOutputEnabled: structured,
        });
    };

    return (
        <FormContainer>
            <ItemList>
                <TextItem
                    label={intl.formatMessage({defaultMessage: 'Display name'})}
                    value={draft.displayName}
                    onChange={(e) => onChange({displayName: e.target.value})}
                    placeholder={intl.formatMessage({defaultMessage: 'e.g. Sales Assistant'})}
                    error={errors.displayName}
                />
                <TextItem
                    label={intl.formatMessage({defaultMessage: 'Agent username'})}
                    value={draft.username}
                    maxLength={22}
                    disabled={usernameLocked}
                    onChange={(e) => onChange({username: e.target.value})}
                    error={errors.username}
                    helptext={intl.formatMessage({
                        defaultMessage:
                            'Users will mention this name to interact with the agent. Must start with a letter and contain only lowercase letters, numbers, dots, hyphens, or underscores. The username cannot be changed after the agent is created.',
                    })}
                />
                <AvatarItem
                    botusername={draft.username}
                    avatarOwnerKey={props.botUserId}
                    changedAvatar={(image: File) => onAvatarChange(image)}
                />
                <SelectionItem
                    label={intl.formatMessage({defaultMessage: 'AI Service'})}
                    value={draft.serviceId}
                    onChange={(e) => onChange({serviceId: e.target.value})}
                    error={errors.serviceId}
                    helptext={intl.formatMessage({
                        defaultMessage:
                            'Select an AI service to load model suggestions and configure vision, tools, native provider tools, reasoning, and structured output.',
                    })}
                >
                    <SelectionItemOption value=''>
                        {intl.formatMessage({defaultMessage: 'Select a service'})}
                    </SelectionItemOption>
                    {draft.serviceId && !services.find((s) => s.id === draft.serviceId) && (
                        <SelectionItemOption
                            value={draft.serviceId}
                            disabled={true}
                        >
                            {intl.formatMessage({defaultMessage: 'Unknown service (deleted)'})}
                        </SelectionItemOption>
                    )}
                    {services.map((svc) => (
                        <SelectionItemOption
                            key={svc.id}
                            value={svc.id}
                        >
                            {svc.name || svc.type}
                        </SelectionItemOption>
                    ))}
                </SelectionItem>

                {supportsModelFetching && availableModels.length > 0 ? (
                    <ComboboxItem
                        label={intl.formatMessage({defaultMessage: 'Model'})}
                        value={draft.model}
                        options={availableModels}
                        placeholder={comboboxModelPlaceholder}
                        onChange={(value) => onChange({model: value})}
                        helptext={comboboxModelHelptext}
                    />
                ) : (
                    <TextItem
                        label={intl.formatMessage({defaultMessage: 'Model'})}
                        helptext={modelHelptext}
                        placeholder={modelPlaceholder}
                        value={draft.model}
                        onChange={(e) => onChange({model: e.target.value})}
                    />
                )}

                <TextItem
                    label={intl.formatMessage({defaultMessage: 'Custom instructions'})}
                    placeholder={intl.formatMessage({defaultMessage: 'How would you like the agent to respond?'})}
                    multiline={true}
                    value={draft.customInstructions}
                    onChange={(e) => onChange({customInstructions: e.target.value})}
                />
            </ItemList>

            <AdvancedSection>
                <AdvancedHeader
                    type='button'
                    aria-expanded={advancedExpanded}
                    onClick={() => setAdvancedExpanded((prev) => !prev)}
                >
                    <ChevronContainer aria-hidden={true}>
                        {advancedExpanded ? <ChevronDownIcon size={16}/> : <ChevronRightIcon size={16}/>}
                    </ChevronContainer>
                    <AdvancedHeaderText>
                        <FormattedMessage defaultMessage='Advanced configuration'/>
                    </AdvancedHeaderText>
                    <AdvancedHeaderHint>
                        <FormattedMessage defaultMessage='Tool limits, vision, reasoning, and other model-specific options'/>
                    </AdvancedHeaderHint>
                </AdvancedHeader>
                {advancedExpanded && (
                    <AdvancedContent>
                        <ItemList>
                            <FormRow>
                                <ItemLabel>
                                    {intl.formatMessage({defaultMessage: 'Dynamic tool loading'})}
                                </ItemLabel>
                                <TextFieldContainer>
                                    <FieldControlRow>
                                        <InlineCheckbox
                                            testId='mcp-dynamic-tool-loading'
                                            inputAriaLabel={intl.formatMessage({defaultMessage: 'Dynamic tool loading'})}
                                            label={intl.formatMessage({defaultMessage: 'Enable'})}
                                            checked={draft.mcpDynamicToolLoading}
                                            onChange={(checked) => onChange({mcpDynamicToolLoading: checked})}
                                        />
                                    </FieldControlRow>
                                    <HelpText>
                                        {intl.formatMessage({
                                            defaultMessage:
                                                'Expose search and load helper tools first, then load MCP tool schemas only when the agent needs them. Disable this to use the full MCP tool list for this agent.',
                                        })}
                                    </HelpText>
                                </TextFieldContainer>
                            </FormRow>
                            <IntItem
                                label={intl.formatMessage({defaultMessage: 'Max tool turns'})}
                                value={draft.maxToolTurns}
                                min={1}
                                max={MaxAllowedMaxToolTurns}
                                allowEmpty={true}
                                clampOnChange={false}
                                defaultValue={DefaultMaxToolTurns}
                                placeholder={String(DefaultMaxToolTurns)}
                                onChange={(value: number) => onChange({maxToolTurns: value})}
                                error={errors.maxToolTurns}
                                helptext={intl.formatMessage({defaultMessage: 'Maximum number of consecutive tool-call/execute rounds the agent will run before stopping. Lower this for smaller models that tend to loop on tool calls; raise it for agents that chain many tools per turn.'})}
                            />

                            {supportsVisionAndTools && (
                                <>
                                    <BooleanItem
                                        label={intl.formatMessage({defaultMessage: 'Enable Vision'})}
                                        value={draft.enableVision}
                                        onChange={(to: boolean) => onChange({enableVision: to})}
                                        helpText={intl.formatMessage({defaultMessage: 'Enable Vision to allow the bot to process images. Requires a compatible model.'})}
                                    />
                                    <BooleanItem
                                        label={intl.formatMessage({defaultMessage: 'Enable Tools'})}
                                        value={!draft.disableTools}
                                        onChange={(to: boolean) => onChange({disableTools: !to})}
                                        helpText={intl.formatMessage({defaultMessage: 'By default some tool use is enabled to allow for features such as integrations with JIRA. Disabling this allows use of models that do not support or are not very good at tool use. Some features will not work without tools.'})}
                                    />
                                    {isAnthropic && (
                                        <NativeToolsItem
                                            enabledTools={draft.enabledNativeTools}
                                            onChange={(tools: string[]) => onChange({enabledNativeTools: tools})}
                                            provider='anthropic'
                                        />
                                    )}
                                    {isGoogle && (
                                        <NativeToolsItem
                                            enabledTools={draft.enabledNativeTools}
                                            onChange={(tools: string[]) => onChange({enabledNativeTools: tools})}
                                            provider='google'
                                        />
                                    )}
                                    {isOpenAIWithResponses && (
                                        <NativeToolsItem
                                            enabledTools={draft.enabledNativeTools}
                                            onChange={(tools: string[]) => onChange({enabledNativeTools: tools})}
                                            provider='openai'
                                        />
                                    )}
                                    {selectedServiceAsLLM && (
                                        <ReasoningConfigItem
                                            bot={reasoningBot}
                                            service={selectedServiceAsLLM}
                                            maxTokens={maxTokens}
                                            onChange={handleReasoningBotChange}
                                        />
                                    )}
                                    {supportsStructuredOutput && (
                                        <>
                                            <BooleanItem
                                                label={intl.formatMessage({defaultMessage: 'Structured Output'})}
                                                value={draft.structuredOutputEnabled}
                                                onChange={(to: boolean) => {
                                                    if (isAnthropic && to) {
                                                        reasoningBeforeStructuredRef.current = draft.reasoningEnabled;
                                                        onChange({
                                                            structuredOutputEnabled: true,
                                                            reasoningEnabled: false,
                                                        });
                                                    } else if (isAnthropic) {
                                                        const restore = reasoningBeforeStructuredRef.current;
                                                        reasoningBeforeStructuredRef.current = null;
                                                        onChange({
                                                            structuredOutputEnabled: false,
                                                            reasoningEnabled: restore === null ? true : restore,
                                                        });
                                                    } else {
                                                        onChange({structuredOutputEnabled: to});
                                                    }
                                                }}
                                                helpText={isAnthropic ?
                                                    intl.formatMessage({defaultMessage: 'Enable structured JSON output for this agent. When enabled and a JSON schema is provided in the request, the model will produce valid JSON matching the schema. Requires a compatible Anthropic model (Claude 4.5/4.6+). Note: Structured output and extended thinking cannot be used simultaneously.'}) :
                                                    intl.formatMessage({defaultMessage: 'Enable structured JSON output for this agent. When enabled and a JSON schema is provided in the request, the model will produce valid JSON matching the schema.'})
                                                }
                                            />
                                            {isAnthropic && draft.structuredOutputEnabled && (
                                                <FormRow>
                                                    <span aria-hidden={true}/>
                                                    <StructuredOutputNote>
                                                        {intl.formatMessage({
                                                            defaultMessage:
                                                                'Extended thinking is turned off while structured output is enabled (Anthropic does not support both at once). Turn structured output off to restore your previous extended thinking setting.',
                                                        })}
                                                    </StructuredOutputNote>
                                                </FormRow>
                                            )}
                                        </>
                                    )}
                                </>
                            )}
                        </ItemList>
                    </AdvancedContent>
                )}
            </AdvancedSection>
        </FormContainer>
    );
};

const FormContainer = styled.div`
    display: flex;
    flex-direction: column;
    gap: 24px;
`;

const AdvancedSection = styled.div`
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    border-radius: 4px;
    overflow: hidden;
`;

const AdvancedHeader = styled.button`
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    padding: 12px 16px;
    border: none;
    background: rgba(var(--center-channel-color-rgb), 0.04);
    cursor: pointer;
    text-align: left;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.08);
    }
`;

const ChevronContainer = styled.span`
    display: flex;
    align-items: center;
    justify-content: center;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    flex-shrink: 0;
`;

const AdvancedHeaderText = styled.span`
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    color: var(--center-channel-color);
`;

const AdvancedHeaderHint = styled.span`
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    margin-left: auto;
`;

const AdvancedContent = styled.div`
    padding: 24px 16px;
`;

const StructuredOutputNote = styled.div`
    font-size: 13px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    line-height: 20px;
    padding: 10px 12px;
    border-radius: 4px;
    background: rgba(var(--center-channel-color-rgb), 0.04);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
`;

export default ConfigTab;
