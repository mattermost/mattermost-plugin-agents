// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useId} from 'react';
import styled from 'styled-components';
import {useIntl} from 'react-intl';

import {Tag} from '@mattermost/compass-ui/components/tag';
import {TextInput} from '@mattermost/compass-ui/components/text-input';

import {useIsLicensedFor} from '@/license';

import MCPToolRow from '../mcp_tools/mcp_tool_row';
import {useToolPolicyOptions} from '../mcp_tools/policy_utils';

import {LicenseChip} from './enterprise_chip';
import {SelectField} from './item';
import {useSettingsReadOnly} from './settings_read_only';
import {MCPToolConfig, MCPToolInfo} from './mcp_types';

export {useToolPolicyOptions};

type ToolPolicy = MCPToolConfig['policy'];

type MCPToolConfigRowProps = {
    tool: MCPToolInfo;
    toolConfig: MCPToolConfig;

    // policyChosen is true when the admin picked the policy, so it is stored
    // even if it equals the tool's default.
    onToolConfigChange: (config: MCPToolConfig, policyChosen?: boolean) => void;
    serverDisabled?: boolean;
    serverName?: string;
    displayName?: string;

    // notReviewed marks a tool no admin has chosen a policy for.
    notReviewed?: boolean;
};

const MCPToolConfigRow = ({
    tool,
    toolConfig,
    onToolConfigChange,
    serverDisabled,
    serverName,
    displayName,
    notReviewed,
}: MCPToolConfigRowProps) => {
    const intl = useIntl();
    const approvalPoliciesLicensed = useIsLicensedFor('tool_approval_policies');
    const policyOptions = useToolPolicyOptions();
    const overrideInputId = useId();
    const readOnly = useSettingsReadOnly();

    const visibleName = displayName ?? tool.name;

    const handlePolicyChange = (policy: string) => {
        onToolConfigChange({
            ...toolConfig,
            policy: policy as ToolPolicy,
        }, true);
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

    const toggleAriaLabel = (() => {
        if (serverName) {
            return toolConfig.enabled ? intl.formatMessage(
                {defaultMessage: 'Disable tool {toolName} on {serverName}'},
                {toolName: visibleName, serverName},
            ) : intl.formatMessage(
                {defaultMessage: 'Enable tool {toolName} on {serverName}'},
                {toolName: visibleName, serverName},
            );
        }
        return toolConfig.enabled ? intl.formatMessage(
            {defaultMessage: 'Disable tool {toolName}'},
            {toolName: visibleName},
        ) : intl.formatMessage(
            {defaultMessage: 'Enable tool {toolName}'},
            {toolName: visibleName},
        );
    })();

    const policyControl = (
        <PolicySelectWrapper>
            <PolicySelectContainer>
                <SelectField
                    value={toolConfig.policy}
                    onChange={handlePolicyChange}
                    disabled={serverDisabled || !approvalPoliciesLicensed}
                    ariaLabel={intl.formatMessage(
                        {defaultMessage: 'Approval policy for {toolName}'},
                        {toolName: visibleName},
                    )}
                    options={policyOptions}
                />
            </PolicySelectContainer>
            {!approvalPoliciesLicensed && (
                <LicenseChip capability='tool_approval_policies'/>
            )}
        </PolicySelectWrapper>
    );

    const extraDetails = (
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
                disabled={serverDisabled || readOnly}
                placeholder={intl.formatMessage({defaultMessage: 'Describe when the agent should use this tool...'})}
            />
            <OverrideHelp>
                {intl.formatMessage({defaultMessage: 'Optional. Used only by dynamic tool loading search to help the agent find this tool. It does not change the tool schema sent after loading.'})}
            </OverrideHelp>
        </OverrideField>
    );

    return (
        <MCPToolRow
            displayName={visibleName}
            description={tool.description}
            inputSchema={tool.inputSchema}
            badges={notReviewed ? (
                <Tag
                    label={intl.formatMessage({defaultMessage: 'Not reviewed'})}
                    type='warning'
                />
            ) : null}
            policyControl={policyControl}
            enabled={toolConfig.enabled}
            toggleDisabled={Boolean(serverDisabled || readOnly)}
            toggleAriaLabel={toggleAriaLabel}
            onToggleEnabled={handleEnabledChange}
            dimmed={Boolean(serverDisabled)}
            extraDetails={extraDetails}
        />
    );
};

const PolicySelectWrapper = styled.div`
    display: flex;
    flex-direction: row;
    align-items: center;
    justify-content: flex-end;
    gap: var(--spacing-xs);
    width: auto;
    min-width: 192px;
`;

const PolicySelectContainer = styled.div`
    width: 200px;
    flex-shrink: 0;
`;

const OverrideField = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xxxs);
`;

const OverrideLabel = styled.label`
    font-size: var(--font-size-75);
    font-weight: var(--font-weight-semibold);
    color: rgba(var(--center-channel-color-rgb), 0.8);
`;

const OverrideHelp = styled.div`
    font-size: 11px;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

export default MCPToolConfigRow;
