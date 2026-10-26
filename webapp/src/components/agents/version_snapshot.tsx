// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {Tag} from '@mattermost/compass-ui/components/tag';

import {ChannelAccessLevel, UserAccessLevel} from '@/components/system_console/bot';
import {DefaultMaxToolTurns, ServiceInfo, UserAgent, normalizeSkillsConfig} from '@/types/agents';

import {changedFieldLabels} from './agent_version_labels';

type Props = {
    config: Partial<UserAgent>;
    services: ServiceInfo[];

    /** BotConfig keys this version changed; their rows are marked. */
    changedFields?: string[] | null;
}

// The BotConfig keys behind each row, so a changed key marks the row showing it.
const FIELD_GROUPS = {
    displayName: ['displayName'],
    service: ['serviceID'],
    model: ['model'],
    instructions: ['customInstructions'],
    tools: ['disableTools'],
    maxToolTurns: ['maxToolTurns'],
    dynamicToolLoading: ['mcpDynamicToolLoading'],
    mcpTools: ['enabledMCPTools', 'autoEnableNewMCPTools'],
    skills: ['skills'],
    vision: ['enableVision'],
    reasoning: ['reasoningEnabled', 'reasoningEffort', 'thinkingBudget'],
    nativeTools: ['enabledNativeTools'],
    channelAccess: ['channelAccessLevel', 'channelIDs'],
    userAccess: ['userAccessLevel', 'userIDs', 'teamIDs'],
};

const SHOWN_FIELDS = new Set(Object.values(FIELD_GROUPS).flat());

/**
 * Read-only rendering of a stored agent version snapshot (llm.BotConfig JSON).
 */
const VersionSnapshot = ({config, services, changedFields}: Props) => {
    const intl = useIntl();

    const changed = new Set(changedFields ?? []);
    const isChanged = (group: keyof typeof FIELD_GROUPS) => FIELD_GROUPS[group].some((key) => changed.has(key));

    // Changes to settings this view has no row for (e.g. the username).
    const otherChanges = changedFieldLabels(intl, (changedFields ?? []).filter((key) => !SHOWN_FIELDS.has(key)));

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

    const skills = normalizeSkillsConfig(config.skills);
    let skillsValue = disabled;
    if (!skills.disabled) {
        skillsValue = intl.formatMessage(
            {defaultMessage: 'Enabled, personal skills {personal, select, true {allowed} other {not allowed}}, {count, plural, one {# shared skill} other {# shared skills}}'},
            {personal: String(skills.allowPersonal), count: skills.enabledSharedIDs?.length ?? 0},
        );
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
        <>
            <Grid data-testid='version-snapshot'>
                <Row
                    label={intl.formatMessage({defaultMessage: 'Display name'})}
                    changed={isChanged('displayName')}
                >
                    {config.displayName || none}
                </Row>
                <Row
                    label={intl.formatMessage({defaultMessage: 'AI service'})}
                    changed={isChanged('service')}
                >
                    {serviceLabel}
                </Row>
                <Row
                    label={intl.formatMessage({defaultMessage: 'Model'})}
                    changed={isChanged('model')}
                >
                    {config.model || intl.formatMessage({defaultMessage: 'Service default'})}
                </Row>
                <Row
                    label={intl.formatMessage({defaultMessage: 'Custom instructions'})}
                    changed={isChanged('instructions')}
                >
                    {config.customInstructions ? (
                        <Instructions
                            tabIndex={0} // eslint-disable-line agents/no-noninteractive-tabindex -- scrollable region must be keyboard-focusable to scroll
                            aria-label={intl.formatMessage({defaultMessage: 'Custom instructions (read-only)'})}
                            data-testid='version-snapshot-instructions'
                        >
                            {config.customInstructions}
                        </Instructions>
                    ) : none}
                </Row>
                <Row
                    label={intl.formatMessage({defaultMessage: 'Tools'})}
                    changed={isChanged('tools')}
                >
                    {toolsDisabled ? disabled : enabled}
                </Row>
                <Row
                    label={intl.formatMessage({defaultMessage: 'Max tool turns'})}
                    changed={isChanged('maxToolTurns')}
                >
                    {config.maxToolTurns && config.maxToolTurns > 0 ? config.maxToolTurns : DefaultMaxToolTurns}
                </Row>
                <Row
                    label={intl.formatMessage({defaultMessage: 'Dynamic tool loading'})}
                    changed={isChanged('dynamicToolLoading')}
                >
                    {(config.mcpDynamicToolLoading ?? true) ? enabled : disabled}
                </Row>
                <Row
                    label={intl.formatMessage({defaultMessage: 'MCP tools'})}
                    changed={isChanged('mcpTools')}
                >
                    {mcpToolsValue}
                </Row>
                <Row
                    label={intl.formatMessage({defaultMessage: 'Skills'})}
                    changed={isChanged('skills')}
                >
                    {skillsValue}
                </Row>
                <Row
                    label={intl.formatMessage({defaultMessage: 'Vision'})}
                    changed={isChanged('vision')}
                >
                    {(config.enableVision ?? true) ? enabled : disabled}
                </Row>
                <Row
                    label={intl.formatMessage({defaultMessage: 'Reasoning'})}
                    changed={isChanged('reasoning')}
                >
                    {reasoning}
                </Row>
                <Row
                    label={intl.formatMessage({defaultMessage: 'Native tools'})}
                    changed={isChanged('nativeTools')}
                >
                    {nativeTools.length === 0 ? none : nativeTools.join(', ')}
                </Row>
                <Row
                    label={intl.formatMessage({defaultMessage: 'Channel access'})}
                    changed={isChanged('channelAccess')}
                >
                    {channelAccess}
                </Row>
                <Row
                    label={intl.formatMessage({defaultMessage: 'User access'})}
                    changed={isChanged('userAccess')}
                >
                    {userAccess}
                </Row>
            </Grid>
            {otherChanges.length > 0 && (
                <OtherChanges data-testid='version-other-changes'>
                    {intl.formatMessage(
                        {defaultMessage: 'Also changed: {fields}'},
                        {fields: otherChanges.join(', ')},
                    )}
                </OtherChanges>
            )}
        </>
    );
};

const Row = ({label, changed, children}: {label: string; changed: boolean; children: React.ReactNode}) => (
    <RowWrap>
        <Label>
            {label}
            {changed && (
                <Tag
                    type='default'
                    size='x-small'
                    label={<FormattedMessage defaultMessage='Changed'/>}
                />
            )}
        </Label>
        <Value>{children}</Value>
    </RowWrap>
);

const Grid = styled.dl`
    margin: 0;
    font-size: 14px;
    line-height: 20px;
`;

const RowWrap = styled.div`
    display: grid;
    grid-template-columns: 180px minmax(0, 1fr);
    align-items: start;
    gap: var(--spacing-l);
    padding: var(--spacing-xs) 0;
    /* --border-light is a whole border shorthand in the host (e.g. "1px solid rgba(...)"), not a color. */
    border-bottom: var(--border-light, 1px solid rgba(var(--center-channel-color-rgb), 0.08));
`;

const Label = styled.dt`
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--spacing-xxxs) var(--spacing-xs);
    min-height: 20px;
    font-size: var(--font-size-75);
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

const OtherChanges = styled.p`
    margin: var(--spacing-m) 0 0;
    font-size: var(--font-size-75);
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const Instructions = styled.div`
    min-width: 0;
    max-height: 280px;
    overflow: auto;
    white-space: pre-wrap;
    overflow-wrap: break-word;
    word-break: normal;
`;

const ToolList = styled.ul`
    margin: 0;
    padding-left: 18px;
`;

const ToolOrigin = styled.span`
    margin-left: var(--spacing-xs);
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

export default VersionSnapshot;
