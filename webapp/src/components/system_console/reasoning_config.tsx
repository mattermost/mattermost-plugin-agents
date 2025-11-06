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

    // Determine if this service supports reasoning
    const isAnthropic = props.service.type === 'anthropic';
    const isOpenAIWithResponses = ['openai', 'openaicompatible', 'azure'].includes(props.service.type) && props.service.useResponsesAPI;

    if (!isAnthropic && !isOpenAIWithResponses) {
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

    return (
        <>
            <ItemLabel>
                <Horizontal>
                    {isAnthropic ?
                        intl.formatMessage({defaultMessage: 'Extended Thinking'}) :
                        intl.formatMessage({defaultMessage: 'Reasoning'})
                    }
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
    background: white;
    font-size: 14px;
    font-weight: 400;
    line-height: 20px;
    max-width: 200px;

    &:focus {
        border-color: #66afe9;
        box-shadow: inset 0 1px 1px rgba(0, 0, 0, 0.075), 0 0 8px rgba(102, 175, 233, 0.75);
        outline: 0;
    }
`;

const FieldSelect = styled.select`
    appearance: none;
    padding: 7px 12px;
    border-radius: 2px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    box-shadow: 0px 1px 1px rgba(0, 0, 0, 0.075) inset;
    height: 35px;
    background: white;
    font-size: 14px;
    font-weight: 400;
    line-height: 20px;
    max-width: 200px;
    cursor: pointer;

    &:focus {
        border-color: #66afe9;
        box-shadow: inset 0 1px 1px rgba(0, 0, 0, 0.075), 0 0 8px rgba(102, 175, 233, 0.75);
        outline: 0;
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

