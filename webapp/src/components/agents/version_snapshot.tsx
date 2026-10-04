// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {useIntl} from 'react-intl';

import {ChannelAccessLevel, UserAccessLevel} from '@/components/system_console/bot';
import {DefaultMaxToolTurns, ServiceInfo, UserAgent} from '@/types/agents';

type Props = {
    config: Partial<UserAgent>;
    services: ServiceInfo[];
}

/**
 * Read-only rendering of a stored agent version snapshot (llm.BotConfig JSON).
 */
const VersionSnapshot = ({config, services}: Props) => {
    const intl = useIntl();

    const enabled = intl.formatMessage({defaultMessage: 'Enabled'});
    const disabled = intl.formatMessage({defaultMessage: 'Disabled'});
    const none = intl.formatMessage({defaultMessage: 'None'});

    const service = services.find((s) => s.id === config.serviceID);
    const serviceLabel = service ? service.name : intl.formatMessage({defaultMessage: 'Unknown service'});

    const channelCount = config.channelIDs?.length ?? 0;
    let channelAccess: string;
    switch (config.channelAccessLevel) {
    case ChannelAccessLevel.Allow:
        channelAccess = channelCount > 0 ? intl.formatMessage({defaultMessage: 'Selected channels only ({count})'}, {count: channelCount}) : intl.formatMessage({defaultMessage: 'Selected channels only'});
        break;
    case ChannelAccessLevel.Block:
        channelAccess = channelCount > 0 ? intl.formatMessage({defaultMessage: 'All channels except selected ({count})'}, {count: channelCount}) : intl.formatMessage({defaultMessage: 'All channels except selected'});
        break;
    case ChannelAccessLevel.None:
        channelAccess = intl.formatMessage({defaultMessage: 'No channels'});
        break;
    default:
        channelAccess = intl.formatMessage({defaultMessage: 'All channels'});
    }

    const userCount = (config.userIDs?.length ?? 0) + (config.teamIDs?.length ?? 0);
    let userAccess: string;
    switch (config.userAccessLevel) {
    case UserAccessLevel.Allow:
        userAccess = userCount > 0 ? intl.formatMessage({defaultMessage: 'Selected users and teams only ({count})'}, {count: userCount}) : intl.formatMessage({defaultMessage: 'Selected users and teams only'});
        break;
    case UserAccessLevel.Block:
        userAccess = userCount > 0 ? intl.formatMessage({defaultMessage: 'All users except selected ({count})'}, {count: userCount}) : intl.formatMessage({defaultMessage: 'All users except selected'});
        break;
    case UserAccessLevel.None:
        userAccess = intl.formatMessage({defaultMessage: 'No users'});
        break;
    case UserAccessLevel.AttributeBased:
        userAccess = intl.formatMessage({defaultMessage: 'Attribute-based (policy not stored in versions)'});
        break;
    default:
        userAccess = intl.formatMessage({defaultMessage: 'All users'});
    }

    const mcpTools = config.enabledMCPTools ?? [];
    const nativeTools = config.enabledNativeTools ?? [];
    const toolsDisabled = config.disableTools ?? false;

    let reasoning = disabled;
    if (config.reasoningEnabled ?? true) {
        const effort = config.reasoningEffort ?? '';
        const budget = config.thinkingBudget && config.thinkingBudget > 0 ? intl.formatNumber(config.thinkingBudget) : '';
        if (effort && budget) {
            reasoning = intl.formatMessage({defaultMessage: 'Enabled ({effort}, thinking budget {budget})'}, {effort, budget});
        } else if (effort) {
            reasoning = intl.formatMessage({defaultMessage: 'Enabled ({effort})'}, {effort});
        } else if (budget) {
            reasoning = intl.formatMessage({defaultMessage: 'Enabled (thinking budget {budget})'}, {budget});
        } else {
            reasoning = enabled;
        }
    }

    let mcpToolsValue: React.ReactNode = none;
    if (config.autoEnableNewMCPTools) {
        mcpToolsValue = intl.formatMessage({defaultMessage: 'All MCP tools'});
    } else if (mcpTools.length > 0) {
        mcpToolsValue = (
            <ToolList>
                {mcpTools.map((tool) => (
                    <li key={`${tool.server_origin}\u0000${tool.tool_name}`}>
                        {tool.tool_name}
                        <ToolOrigin>{tool.server_origin}</ToolOrigin>
                    </li>
                ))}
            </ToolList>
        );
    }

    return (
        <Grid data-testid='version-snapshot'>
            <Row label={intl.formatMessage({defaultMessage: 'Display name'})}>
                {config.displayName || none}
            </Row>
            <Row label={intl.formatMessage({defaultMessage: 'AI service'})}>
                {serviceLabel}
            </Row>
            <Row label={intl.formatMessage({defaultMessage: 'Model'})}>
                {config.model || intl.formatMessage({defaultMessage: 'Service default'})}
            </Row>
            <Row label={intl.formatMessage({defaultMessage: 'Custom instructions'})}>
                {config.customInstructions ? (
                    <Instructions
                        tabIndex={0}
                        aria-label={intl.formatMessage({defaultMessage: 'Custom instructions (read-only)'})}
                        data-testid='version-snapshot-instructions'
                    >
                        {config.customInstructions}
                    </Instructions>
                ) : none}
            </Row>
            <Row label={intl.formatMessage({defaultMessage: 'Tools'})}>
                {toolsDisabled ? disabled : enabled}
            </Row>
            <Row label={intl.formatMessage({defaultMessage: 'Max tool turns'})}>
                {config.maxToolTurns && config.maxToolTurns > 0 ? config.maxToolTurns : DefaultMaxToolTurns}
            </Row>
            <Row label={intl.formatMessage({defaultMessage: 'Dynamic tool loading'})}>
                {(config.mcpDynamicToolLoading ?? true) ? enabled : disabled}
            </Row>
            <Row label={intl.formatMessage({defaultMessage: 'MCP tools'})}>
                {mcpToolsValue}
            </Row>
            <Row label={intl.formatMessage({defaultMessage: 'Vision'})}>
                {(config.enableVision ?? true) ? enabled : disabled}
            </Row>
            <Row label={intl.formatMessage({defaultMessage: 'Reasoning'})}>
                {reasoning}
            </Row>
            <Row label={intl.formatMessage({defaultMessage: 'Native tools'})}>
                {nativeTools.length === 0 ? none : nativeTools.join(', ')}
            </Row>
            <Row label={intl.formatMessage({defaultMessage: 'Channel access'})}>
                {channelAccess}
            </Row>
            <Row label={intl.formatMessage({defaultMessage: 'User access'})}>
                {userAccess}
            </Row>
        </Grid>
    );
};

const Row = ({label, children}: {label: string; children: React.ReactNode}) => (
    <>
        <Label>{label}</Label>
        <Value>{children}</Value>
    </>
);

const Grid = styled.dl`
    display: grid;
    grid-template-columns: minmax(auto, 180px) minmax(0, 1fr);
    gap: 12px 16px;
    margin: 0;
    font-size: 14px;
    line-height: 20px;
`;

const Label = styled.dt`
    font-weight: 600;
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const Value = styled.dd`
    margin: 0;
    min-width: 0;
    color: var(--center-channel-color);
    overflow-wrap: break-word;
    word-break: normal;
`;

const Instructions = styled.pre`
    margin: 0;
    min-width: 0;
    padding: 8px 12px;
    max-height: 280px;
    overflow: auto;
    white-space: pre-wrap;
    overflow-wrap: break-word;
    word-break: normal;
    font-family: inherit;
    font-size: 13px;
    line-height: 20px;
    border-radius: 4px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    background: rgba(var(--center-channel-color-rgb), 0.04);
`;

const ToolList = styled.ul`
    margin: 0;
    padding-left: 18px;
`;

const ToolOrigin = styled.span`
    margin-left: 8px;
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

export default VersionSnapshot;
