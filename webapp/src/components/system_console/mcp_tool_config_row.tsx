// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useId, useState} from 'react';
import styled from 'styled-components';
import {ChevronDownIcon, ChevronRightIcon} from '@mattermost/compass-icons/components';
import {useIntl} from 'react-intl';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {TextInput} from '@mattermost/compass-ui/components/text-input';

import {useIsLicensedFor} from '@/license';

import {ToggleSwitch} from '../toggle_switch';

import {LicenseChip} from './enterprise_chip';
import {SelectField} from './item';
import {MCPToolConfig, MCPToolInfo} from './mcp_types';

type ToolPolicy = MCPToolConfig['policy'];

type MCPToolConfigRowProps = {
    tool: MCPToolInfo;
    toolConfig: MCPToolConfig;
    onToolConfigChange: (config: MCPToolConfig) => void;
    serverDisabled?: boolean;
    displayName?: string;
};

const MCPToolConfigRow = ({tool, toolConfig, onToolConfigChange, serverDisabled, displayName}: MCPToolConfigRowProps) => {
    const intl = useIntl();
    const approvalPoliciesLicensed = useIsLicensedFor('tool_approval_policies');
    const [schemaExpanded, setSchemaExpanded] = useState(false);
    const overrideInputId = useId();

    const handlePolicyChange = (policy: string) => {
        onToolConfigChange({
            ...toolConfig,
            policy: policy as ToolPolicy,
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
                        <PolicySelectContainer>
                            <SelectField
                                value={toolConfig.policy}
                                onChange={handlePolicyChange}
                                disabled={serverDisabled || !approvalPoliciesLicensed}
                                ariaLabel={intl.formatMessage(
                                    {defaultMessage: 'Approval policy for {toolName}'},
                                    {toolName: displayName ?? tool.name},
                                )}
                                options={[
                                    {value: 'auto_run_in_dm', label: intl.formatMessage({defaultMessage: 'Auto Run (DM)'})},
                                    {value: 'auto_run_everywhere', label: intl.formatMessage({defaultMessage: 'Auto Run (Everywhere)'})},
                                    {value: 'ask', label: intl.formatMessage({defaultMessage: 'Ask Every Time'})},
                                ]}
                            />
                        </PolicySelectContainer>
                        {!approvalPoliciesLicensed && (
                            <LicenseChip capability='tool_approval_policies'/>
                        )}
                    </PolicySelectWrapper>
                    <ToggleWrapper>
                        <ToggleSwitch
                            checked={toolConfig.enabled}
                            onChange={handleEnabledChange}
                            disabled={serverDisabled}
                            size='small'
                        />
                    </ToggleWrapper>
                    <IconButton
                        icon={<Icon glyph={schemaExpanded ? <ChevronDownIcon/> : <ChevronRightIcon/>}/>}
                        size='small'
                        onClick={() => setSchemaExpanded(!schemaExpanded)}
                        aria-label={intl.formatMessage({defaultMessage: 'Show tool details'})}
                        aria-expanded={schemaExpanded}
                    />
                </ToolRowRight>
            </ToolRowMain>
            {schemaExpanded && (
                <ExpandedContainer>
                    <OverrideField>
                        <OverrideLabel htmlFor={overrideInputId}>
                            {intl.formatMessage({defaultMessage: 'Retrieval description override'})}
                        </OverrideLabel>
                        <TextInput
                            id={overrideInputId}
                            size='small'
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
    flex-direction: row;
    align-items: center;
    justify-content: flex-end;
    gap: 8px;
    width: auto;
    min-width: 192px;
`;

const PolicySelectContainer = styled.div`
    width: 200px;
    flex-shrink: 0;
`;

const ToggleWrapper = styled.div`
    display: flex;
    align-items: center;
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
