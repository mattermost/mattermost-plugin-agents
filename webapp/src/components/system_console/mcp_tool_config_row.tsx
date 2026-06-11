// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useId, useState} from 'react';
import styled from 'styled-components';
import {ChevronDownIcon} from '@mattermost/compass-icons/components';
import {useIntl} from 'react-intl';

import {ToggleSwitch} from '../toggle_switch';

import {MCPToolConfig} from './mcp_servers';
import {MCPToolInfo} from './mcp_tools_viewer';

type MCPToolConfigRowProps = {
    tool: MCPToolInfo;
    toolConfig: MCPToolConfig;
    onToolConfigChange: (config: MCPToolConfig) => void;
    serverDisabled?: boolean;
    displayName?: string;
};

const MCPToolConfigRow = ({tool, toolConfig, onToolConfigChange, serverDisabled, displayName}: MCPToolConfigRowProps) => {
    const intl = useIntl();
    const [schemaExpanded, setSchemaExpanded] = useState(false);
    const overrideInputId = useId();

    const handlePolicyChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
        onToolConfigChange({
            ...toolConfig,
            policy: e.target.value as 'auto_run_in_dm' | 'auto_run_everywhere' | 'ask',
        });
    };

    const handleEnabledChange = (checked: boolean) => {
        onToolConfigChange({
            ...toolConfig,
            enabled: checked,
        });
    };

    const handleRetrievalDescriptionOverrideChange = (e: React.ChangeEvent<HTMLInputElement>) => {
        const value = e.target.value;
        const nextToolConfig = {...toolConfig};
        if (value.trim() === '') {
            delete nextToolConfig.retrieval_description_override;
        } else {
            nextToolConfig.retrieval_description_override = value;
        }
        onToolConfigChange(nextToolConfig);
    };

    const handleRetrievalDescriptionOverrideBlur = (e: React.FocusEvent<HTMLInputElement>) => {
        const trimmedValue = e.target.value.trim();
        const nextToolConfig = {...toolConfig};
        if (trimmedValue === '') {
            delete nextToolConfig.retrieval_description_override;
        } else {
            nextToolConfig.retrieval_description_override = trimmedValue;
        }
        onToolConfigChange(nextToolConfig);
    };

    return (
        <ToolRowContainer $disabled={serverDisabled}>
            <ToolRowMain>
                <ToolRowLeft>
                    <ToolName>{displayName ?? tool.name}</ToolName>
                    {tool.description && (
                        <ToolDescription>{tool.description}</ToolDescription>
                    )}
                </ToolRowLeft>
                <ToolRowRight>
                    <PolicySelectWrapper>
                        <PolicySelect
                            value={toolConfig.policy}
                            onChange={handlePolicyChange}
                            disabled={serverDisabled}
                        >
                            <option value='auto_run_in_dm'>
                                {intl.formatMessage({defaultMessage: 'Auto Run (DM)'})}
                            </option>
                            <option value='auto_run_everywhere'>
                                {intl.formatMessage({defaultMessage: 'Auto Run (Everywhere)'})}
                            </option>
                            <option value='ask'>
                                {intl.formatMessage({defaultMessage: 'Ask Every Time'})}
                            </option>
                        </PolicySelect>
                    </PolicySelectWrapper>
                    <ToggleWrapper>
                        <ToggleSwitch
                            checked={toolConfig.enabled}
                            onChange={handleEnabledChange}
                            disabled={serverDisabled}
                            size='small'
                        />
                    </ToggleWrapper>
                    <ExpandChevron
                        type='button'
                        onClick={() => setSchemaExpanded(!schemaExpanded)}
                        aria-label={intl.formatMessage({defaultMessage: 'Show tool details'})}
                        aria-expanded={schemaExpanded}
                    >
                        <StyledChevron $expanded={schemaExpanded}>
                            <ChevronDownIcon size={16}/>
                        </StyledChevron>
                    </ExpandChevron>
                </ToolRowRight>
            </ToolRowMain>
            {schemaExpanded && (
                <ExpandedContainer>
                    <OverrideField>
                        <OverrideLabel htmlFor={overrideInputId}>
                            {intl.formatMessage({defaultMessage: 'Retrieval description override'})}
                        </OverrideLabel>
                        <OverrideInput
                            id={overrideInputId}
                            value={toolConfig.retrieval_description_override || ''}
                            onChange={handleRetrievalDescriptionOverrideChange}
                            onBlur={handleRetrievalDescriptionOverrideBlur}
                            disabled={serverDisabled}
                            placeholder={intl.formatMessage({defaultMessage: 'Describe when the agent should use this tool...'})}
                        />
                        <OverrideHelp>
                            {intl.formatMessage({defaultMessage: 'Optional. Used only by dynamic tool loading search to help the agent find this tool. It does not change the tool schema sent after loading.'})}
                        </OverrideHelp>
                    </OverrideField>
                    {tool.inputSchema && (
                        <SchemaContainer>
                            {JSON.stringify(tool.inputSchema, null, 2)}
                        </SchemaContainer>
                    )}
                </ExpandedContainer>
            )}
        </ToolRowContainer>
    );
};

const ToolRowContainer = styled.div<{$disabled?: boolean}>`
    display: flex;
    flex-direction: column;
    opacity: ${(props) => (props.$disabled ? 0.5 : 1)};
    pointer-events: ${(props) => (props.$disabled ? 'none' : 'auto')};
`;

const ToolRowMain = styled.div`
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 0 16px 0 24px;
`;

const ToolRowLeft = styled.div`
    display: flex;
    flex-direction: column;
    flex: 1;
    min-width: 0;
`;

const ToolRowRight = styled.div`
    display: flex;
    align-items: center;
    gap: 16px;
    flex-shrink: 0;
`;

const ToolName = styled.div`
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    font-size: 13px;
    font-weight: 400;
    color: var(--center-channel-color);
    line-height: 20px;
`;

const ToolDescription = styled.div`
    font-size: 12px;
    font-weight: 400;
    color: rgba(var(--center-channel-color-rgb), 0.75);
    line-height: 16px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
`;

const PolicySelectWrapper = styled.div`
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    justify-content: center;
    width: 192px;
`;

const PolicySelect = styled.select`
    appearance: none;
    padding: 4px 20px 4px 4px;
    border: none;
    border-radius: 4px;
    background: transparent;
    font-family: 'Open Sans', sans-serif;
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.11px;
    color: rgba(var(--center-channel-color-rgb), 0.75);
    cursor: pointer;
    line-height: 16px;
    background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 24 24' fill='none' stroke='rgba(63,67,80,0.64)' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'%3E%3Cpolyline points='6 9 12 15 18 9'%3E%3C/polyline%3E%3C/svg%3E");
    background-repeat: no-repeat;
    background-position: right 6px center;

    &:focus {
        outline: none;
    }

    &:disabled {
        opacity: 0.5;
        cursor: not-allowed;
    }
`;

const ToggleWrapper = styled.div`
    display: flex;
    align-items: center;
`;

const ExpandChevron = styled.button`
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    cursor: pointer;
    padding: 8px;
    border-radius: 4px;
    overflow: hidden;
    border: none;
    background: transparent;

    &:focus-visible {
        outline: 2px solid var(--button-bg);
        outline-offset: 2px;
    }
`;

const StyledChevron = styled.div<{$expanded: boolean}>`
    display: flex;
    align-items: center;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    transform: ${(props) => (props.$expanded ? 'rotate(0deg)' : 'rotate(-90deg)')};
    transition: transform 0.2s;
`;

const ExpandedContainer = styled.div`
    display: flex;
    flex-direction: column;
    gap: 8px;
    margin-top: 8px;
    margin-left: 24px;
    margin-right: 16px;
`;

const OverrideField = styled.div`
    display: flex;
    flex-direction: column;
    gap: 4px;
`;

const OverrideLabel = styled.label`
    font-size: 12px;
    font-weight: 600;
    color: rgba(var(--center-channel-color-rgb), 0.8);
`;

const OverrideInput = styled.input`
    padding: 8px 10px;
    border-radius: 4px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    background: var(--center-channel-bg);
    color: var(--center-channel-color);
    font-size: 13px;

    &:focus {
        border-color: var(--button-bg);
        outline: none;
    }

    &:disabled {
        cursor: not-allowed;
    }
`;

const OverrideHelp = styled.div`
    font-size: 11px;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const SchemaContainer = styled.div`
    padding: 8px;
    background: rgba(var(--center-channel-color-rgb), 0.04);
    border-radius: 4px;
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    font-size: 11px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    max-height: 200px;
    overflow: auto;
    white-space: pre;
`;

export default MCPToolConfigRow;
