// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {useIntl} from 'react-intl';

import {ItemLabel, HelpText, FormRow, FieldControlRow, InlineCheckbox, SelectField} from './item';
import {LLMBotConfig} from './bot';
import {LLMService} from './service';

// Matches the server-side default: Anthropic resolves an unset effort to high,
// the thinking depth used before effort was configurable.
export const defaultReasoningEffort = (serviceType?: string): string => (serviceType === 'anthropic' ? 'high' : 'medium');

// Anthropic has no "minimal" level; the server treats it as low.
const anthropicEffortValue = (effort: string | undefined): string => {
    if (effort === 'minimal') {
        return 'low';
    }
    return effort || defaultReasoningEffort('anthropic');
};

type EffortSelectProps = {
    value: string
    includeMinimal: boolean
    onChange: (effort: string) => void
}

const EffortSelect = (props: EffortSelectProps) => {
    const intl = useIntl();
    return (
        <SelectField
            maxWidth='200px'
            value={props.value}
            onChange={(e) => props.onChange(e.target.value)}
        >
            {props.includeMinimal && (
                <option value='minimal'>
                    {intl.formatMessage({defaultMessage: 'Minimal'})}
                </option>
            )}
            <option value='low'>
                {intl.formatMessage({defaultMessage: 'Low'})}
            </option>
            <option value='medium'>
                {intl.formatMessage({defaultMessage: 'Medium'})}
            </option>
            <option value='high'>
                {intl.formatMessage({defaultMessage: 'High'})}
            </option>
        </SelectField>
    );
};

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
    //   - OpenAI direct and Cohere North always use the Responses API.
    //   - Anthropic uses extended thinking controlled by an effort level.
    //   - Gemini / Vertex AI map reasoning to Google's thinkingConfig via Bifrost,
    //     accepting both a thinking budget and an effort level.
    const isAnthropic = props.service.type === 'anthropic';
    const isOpenAIWithResponses =
        props.service.type === 'openai' ||
        props.service.type === 'north' ||
        (['openaicompatible', 'azure'].includes(props.service.type) && props.service.useResponsesAPI);
    const isGoogle = props.service.type === 'gemini' || props.service.type === 'vertex';

    if (!isAnthropic && !isOpenAIWithResponses && !isGoogle) {
        return null;
    }

    const reasoningEnabled = props.bot.reasoningEnabled ?? true; // Default to enabled
    const reasoningEffort = props.bot.reasoningEffort || defaultReasoningEffort(props.service.type);
    const handleEffortChange = (effort: string) => props.onChange({...props.bot, reasoningEffort: effort});

    // For thinking budget, use the value from the bot config, or empty string if 0/undefined
    const thinkingBudgetValue = (props.bot.thinkingBudget && props.bot.thinkingBudget > 0) ? props.bot.thinkingBudget.toString() : '';

    const handleThinkingBudgetChange = (e: React.ChangeEvent<HTMLInputElement>) => {
        const value = e.target.value === '' ? 0 : parseInt(e.target.value, 10);
        props.onChange({...props.bot, thinkingBudget: value});
    };

    const headerLabel = isAnthropic ?
        intl.formatMessage({defaultMessage: 'Extended Thinking'}) :
        intl.formatMessage({defaultMessage: 'Reasoning'});

    return (
        <FormRow>
            <ItemLabel>
                <Horizontal>
                    {headerLabel}
                </Horizontal>
            </ItemLabel>
            <ReasoningContainer>
                <FieldControlRow>
                    <InlineCheckbox
                        testId='reasoning-enable'
                        label={intl.formatMessage({defaultMessage: 'Enable'})}
                        checked={reasoningEnabled}
                        onChange={(checked) => props.onChange({...props.bot, reasoningEnabled: checked})}
                    />
                </FieldControlRow>

                {reasoningEnabled && (
                    <>
                        {isAnthropic && (
                            <ConfigField>
                                <FieldLabel>
                                    {intl.formatMessage({defaultMessage: 'Thinking Effort'})}
                                </FieldLabel>
                                <EffortSelect
                                    value={anthropicEffortValue(props.bot.reasoningEffort)}
                                    includeMinimal={false}
                                    onChange={handleEffortChange}
                                />
                                <HelpText>
                                    {intl.formatMessage({
                                        defaultMessage: 'Controls how much the model thinks before responding. Higher effort allows deeper reasoning but increases response time and cost. Models with adaptive thinking use this effort level directly; older models get a thinking budget scaled to it.',
                                    })}
                                </HelpText>
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
                                    <EffortSelect
                                        value={reasoningEffort}
                                        includeMinimal={true}
                                        onChange={handleEffortChange}
                                    />
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
                                <EffortSelect
                                    value={reasoningEffort}
                                    includeMinimal={true}
                                    onChange={handleEffortChange}
                                />
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
        </FormRow>
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

const ConfigField = styled.div`
    display: flex;
    flex-direction: column;
    gap: 8px;
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

export default ReasoningConfigItem;

