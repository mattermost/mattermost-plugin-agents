// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {useIntl} from 'react-intl';

import {ItemLabel, HelpText} from './item';
import {LLMBotConfig} from './bot';
import {LLMService} from './service';

const maxReasoningBudget = 8192;
const minReasoningBudget = 1024;

type ReasoningConfigItemProps = {
    bot: LLMBotConfig
    service: LLMService | undefined
    maxTokens: number
    onChange: (bot: LLMBotConfig) => void
}

const ReasoningConfigItem = (props: ReasoningConfigItemProps) => {
    const intl = useIntl();

    if (!props.service) {
        return null;
    }

    // Determine if this service supports reasoning.
    //   - OpenAI direct always uses the Responses API.
    //   - Anthropic uses extended thinking with a token budget.
    //   - Gemini / Vertex AI map reasoning to Google's thinkingConfig via Bifrost,
    //     accepting both a thinking budget and an effort level.
    const isAnthropic = props.service.type === 'anthropic';
    const isOpenAIWithResponses =
        props.service.type === 'openai' ||
        (['openaicompatible', 'azure'].includes(props.service.type) && props.service.useResponsesAPI);
    const isGoogle = props.service.type === 'gemini' || props.service.type === 'vertex';

    if (!isAnthropic && !isOpenAIWithResponses && !isGoogle) {
        return null;
    }

    const reasoningEnabled = props.bot.reasoningEnabled ?? true; // Default to enabled
    const reasoningEffort = props.bot.reasoningEffort || 'medium';

    // For thinking budget, use the value from the bot config, or empty string if 0/undefined
    const thinkingBudgetValue = (props.bot.thinkingBudget && props.bot.thinkingBudget > 0) ? props.bot.thinkingBudget.toString() : '';

    const handleThinkingBudgetChange = (e: React.ChangeEvent<HTMLInputElement>) => {
        const value = e.target.value === '' ? 0 : parseInt(e.target.value, 10);
        props.onChange({...props.bot, thinkingBudget: value});
    };

    // Calculate default for help text
    const getDefaultThinkingBudget = () => {
        let defaultBudget = Math.floor(props.maxTokens / 4);
        if (defaultBudget > maxReasoningBudget) {
            defaultBudget = maxReasoningBudget;
        }
        if (defaultBudget < minReasoningBudget) {
            defaultBudget = minReasoningBudget;
        }
        return defaultBudget;
    };

    const headerLabel = isAnthropic ?
        intl.formatMessage({defaultMessage: 'Extended Thinking'}) :
        intl.formatMessage({defaultMessage: 'Reasoning'});

    return (
        <>
            <ItemLabel>
                <Horizontal>
                    {headerLabel}
                </Horizontal>
            </ItemLabel>
            <ReasoningContainer>
                <BooleanToggle>
                    <StyledCheckbox
                        type='checkbox'
                        checked={reasoningEnabled}
                        onChange={(e) => props.onChange({...props.bot, reasoningEnabled: e.target.checked})}
                    />
                    <ToggleLabel>
                        {intl.formatMessage({defaultMessage: 'Enable'})}
                    </ToggleLabel>
                </BooleanToggle>

                {reasoningEnabled && (
                    <>
                        {isAnthropic && (
                            <ConfigField>
                                <FieldLabel>
                                    {intl.formatMessage({defaultMessage: 'Thinking Budget (tokens)'})}
                                </FieldLabel>
                                <FieldInput
                                    type='number'
                                    min='1024'
                                    max={props.maxTokens}
                                    value={thinkingBudgetValue}
                                    onChange={handleThinkingBudgetChange}
                                    placeholder={getDefaultThinkingBudget().toString()}
                                />
                                <HelpText>
                                    {intl.formatMessage({
                                        defaultMessage: 'Token budget for extended thinking. Higher values allow deeper reasoning but increase response time and cost. Must be between 1024 and {maxTokens}. Leave blank to use default ({defaultBudget}).',
                                    }, {
                                        maxTokens: props.maxTokens,
                                        defaultBudget: getDefaultThinkingBudget(),
                                    })}
                                </HelpText>
                                {typeof props.bot.thinkingBudget === 'number' && props.bot.thinkingBudget > 0 && props.bot.thinkingBudget < 1024 && (
                                    <ErrorText>
                                        {intl.formatMessage({defaultMessage: 'Thinking budget must be at least 1024 tokens.'})}
                                    </ErrorText>
                                )}
                                {typeof props.bot.thinkingBudget === 'number' && props.bot.thinkingBudget > props.maxTokens && (
                                    <ErrorText>
                                        {intl.formatMessage({
                                            defaultMessage: 'Thinking budget cannot exceed max tokens ({maxTokens}).',
                                        }, {maxTokens: props.maxTokens})}
                                    </ErrorText>
                                )}
                            </ConfigField>
                        )}

                        {isGoogle && (
                            <>
                                <ConfigField>
                                    <FieldLabel>
                                        {intl.formatMessage({defaultMessage: 'Thinking Budget (tokens, optional)'})}
                                    </FieldLabel>
                                    <FieldInput
                                        type='number'
                                        min='0'
                                        max={props.maxTokens}
                                        value={thinkingBudgetValue}
                                        onChange={handleThinkingBudgetChange}
                                        placeholder={intl.formatMessage({defaultMessage: 'Use effort level'})}
                                    />
                                    <HelpText>
                                        {intl.formatMessage({
                                            defaultMessage: 'Optional token budget for Gemini thinking. When set this takes priority over the effort level and maps to thinkingConfig.thinkingBudget. Leave blank to use the effort level below.',
                                        })}
                                    </HelpText>
                                </ConfigField>
                                <ConfigField>
                                    <FieldLabel>
                                        {intl.formatMessage({defaultMessage: 'Reasoning Effort'})}
                                    </FieldLabel>
                                    <FieldSelect
                                        value={reasoningEffort}
                                        onChange={(e) => props.onChange({...props.bot, reasoningEffort: e.target.value})}
                                    >
                                        <option value='minimal'>
                                            {intl.formatMessage({defaultMessage: 'Minimal'})}
                                        </option>
                                        <option value='low'>
                                            {intl.formatMessage({defaultMessage: 'Low'})}
                                        </option>
                                        <option value='medium'>
                                            {intl.formatMessage({defaultMessage: 'Medium'})}
                                        </option>
                                        <option value='high'>
                                            {intl.formatMessage({defaultMessage: 'High'})}
                                        </option>
                                    </FieldSelect>
                                    <HelpText>
                                        {intl.formatMessage({
                                            defaultMessage: 'Effort level maps to Gemini 3.0+ thinkingLevel and is estimated as a budget for Gemini 2.5 models. Ignored when a thinking budget is set above.',
                                        })}
                                    </HelpText>
                                </ConfigField>
                            </>
                        )}

                        {isOpenAIWithResponses && (
                            <ConfigField>
                                <FieldLabel>
                                    {intl.formatMessage({defaultMessage: 'Reasoning Effort'})}
                                </FieldLabel>
                                <FieldSelect
                                    value={reasoningEffort}
                                    onChange={(e) => props.onChange({...props.bot, reasoningEffort: e.target.value})}
                                >
                                    <option value='minimal'>
                                        {intl.formatMessage({defaultMessage: 'Minimal'})}
                                    </option>
                                    <option value='low'>
                                        {intl.formatMessage({defaultMessage: 'Low'})}
                                    </option>
                                    <option value='medium'>
                                        {intl.formatMessage({defaultMessage: 'Medium'})}
                                    </option>
                                    <option value='high'>
                                        {intl.formatMessage({defaultMessage: 'High'})}
                                    </option>
                                </FieldSelect>
                                <HelpText>
                                    {intl.formatMessage({
                                        defaultMessage: 'Controls how much computational effort the model spends on reasoning. Higher effort levels produce more thorough responses but take longer and cost more. Minimal is fastest, High is most thorough.',
                                    })}
                                </HelpText>
                            </ConfigField>
                        )}
                    </>
                )}
            </ReasoningContainer>
        </>
    );
};

const Horizontal = styled.div`
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: 8px;
`;

const ReasoningContainer = styled.div`
    display: flex;
    flex-direction: column;
    gap: 16px;
`;

const BooleanToggle = styled.div`
    display: flex;
    flex-direction: row;
    align-items: flex-start;
    gap: 8px;
`;

const ToggleLabel = styled.label`
    font-size: 14px;
    font-weight: 400;
    line-height: 20px;
    cursor: pointer;
`;

const ConfigField = styled.div`
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding-left: 28px;
`;

const FieldLabel = styled.label`
    font-size: 13px;
    font-weight: 600;
    line-height: 18px;
`;

const FieldInput = styled.input`
    appearance: none;
    padding: 7px 12px;
    border-radius: 2px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    box-shadow: 0px 1px 1px rgba(0, 0, 0, 0.075) inset;
    height: 35px;
    background: var(--center-channel-bg);
    color: var(--center-channel-color);
    font-size: 14px;
    font-weight: 400;
    line-height: 20px;
    max-width: 200px;

    &::placeholder {
        color: rgba(var(--center-channel-color-rgb), 0.48);
    }

    &:focus {
        border-color: var(--button-bg);
        outline: none;
        box-shadow: none;
    }
`;

const FieldSelect = styled.select`
    appearance: none;
    padding: 7px 12px;
    border-radius: 2px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    box-shadow: 0px 1px 1px rgba(0, 0, 0, 0.075) inset;
    height: 35px;
    background: var(--center-channel-bg);
    color: var(--center-channel-color);
    font-size: 14px;
    font-weight: 400;
    line-height: 20px;
    max-width: 200px;
    cursor: pointer;

    &:focus {
        border-color: var(--button-bg);
        outline: none;
        box-shadow: none;
    }
`;

const ErrorText = styled.div`
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: var(--dnd-indicator, #D24B4E);
`;

const StyledCheckbox = styled.input`
    cursor: pointer;
    margin: 0;
`;

export default ReasoningConfigItem;

