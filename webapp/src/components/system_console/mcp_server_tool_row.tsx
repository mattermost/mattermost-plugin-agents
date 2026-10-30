// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import styled from 'styled-components';
import {ChevronDownIcon, ExclamationThickIcon} from '@mattermost/compass-icons/components';
import {FormattedMessage, useIntl} from 'react-intl';

import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';
import {Tag} from '@mattermost/compass-ui/components/tag';

import {useIsLicensedFor} from '@/license';

import {ToggleSwitch} from '../toggle_switch';

import {pluginIDFromServerOrigin, stripPluginPrefix} from '../../utils/tool_names';

import {SelectField} from './item';

import {MCPServerConfig, MCPServerInfo, MCPToolConfig, MCPToolInfo} from './mcp_types';
import MCPToolConfigRow, {useToolPolicyOptions} from './mcp_tool_config_row';

type MCPServerToolRowProps = {
    server: MCPServerInfo;
    serverConfig: MCPServerConfig | null;
    onServerConfigChange: (config: MCPServerConfig) => void;
};

const MCPServerToolRow = ({server, serverConfig, onServerConfigChange}: MCPServerToolRowProps) => {
    const [expanded, setExpanded] = useState(false);
    const intl = useIntl();
    const remoteMcpLicensed = useIsLicensedFor('remote_mcp');
    const approvalPoliciesLicensed = useIsLicensedFor('tool_approval_policies');
    const policyOptions = useToolPolicyOptions();
    const isRemoteMcp = server.serverType === 'remote' || server.serverType === 'plugin';
    const enableRemoteBlocked = isRemoteMcp && !remoteMcpLicensed;

    const getToolConfig = (tool: MCPToolInfo): MCPToolConfig => {
        const existing = serverConfig?.tool_configs?.find((tc) => tc.name === tool.name);
        if (existing) {
            return existing;
        }

        // Unconfigured tools are enabled with the server-computed default
        // policy (auto run in DMs for tools the MCP server marks read-only).
        return {name: tool.name, policy: tool.defaultPolicy ?? 'ask', enabled: true};
    };

    const enabledToolCount = server.tools.filter((tool) => {
        const tc = getToolConfig(tool);
        return tc.enabled;
    }).length;

    const serverEnabled = serverConfig?.enabled ?? false;

    const handleServerToggle = (enabled: boolean) => {
        if (serverConfig) {
            onServerConfigChange({...serverConfig, enabled});
        }
    };

    const handleToolConfigChange = (toolName: string, updatedToolConfig: MCPToolConfig) => {
        if (!serverConfig) {
            return;
        }
        const existingConfigs = serverConfig.tool_configs || [];
        const existingIndex = existingConfigs.findIndex((tc) => tc.name === toolName);
        let updatedConfigs: MCPToolConfig[];
        if (existingIndex >= 0) {
            updatedConfigs = [...existingConfigs];
            updatedConfigs[existingIndex] = updatedToolConfig;
        } else {
            updatedConfigs = [...existingConfigs, updatedToolConfig];
        }
        onServerConfigChange({...serverConfig, tool_configs: updatedConfigs});
    };

    // Sets one policy on every tool in tools, keeping each tool's enabled
    // state and retrieval override.
    const handleBulkPolicyChange = (tools: MCPToolInfo[], policy: MCPToolConfig['policy']) => {
        if (!serverConfig) {
            return;
        }
        const names = new Set(tools.map((tool) => tool.name));
        const existingConfigs = serverConfig.tool_configs || [];
        const configuredNames = new Set(existingConfigs.map((tc) => tc.name));
        const updatedConfigs = existingConfigs.map((tc) => (names.has(tc.name) ? {...tc, policy} : tc));
        for (const tool of tools) {
            if (!configuredNames.has(tool.name)) {
                updatedConfigs.push({...getToolConfig(tool), policy});
            }
        }
        onServerConfigChange({...serverConfig, tool_configs: updatedConfigs});
    };

    // The policy every tool in tools shares, or '' when they differ.
    const sharedPolicy = (tools: MCPToolInfo[]): string => {
        const policies = new Set(tools.map((tool) => getToolConfig(tool).policy));
        return policies.size === 1 ? [...policies][0] : '';
    };

    const bulkDisabled = !serverConfig || !serverEnabled || !approvalPoliciesLicensed;

    const renderBulkPolicyControl = (tools: MCPToolInfo[], ariaLabel: string) => (
        <BulkPolicySelect>
            <SelectField
                value={sharedPolicy(tools)}
                onChange={(policy) => handleBulkPolicyChange(tools, policy as MCPToolConfig['policy'])}
                options={policyOptions}
                disabled={bulkDisabled}
                placeholder={intl.formatMessage({defaultMessage: 'Mixed'})}
                ariaLabel={ariaLabel}
            />
        </BulkPolicySelect>
    );

    const renderToolRow = (tool: MCPToolInfo) => {
        const isPlugin = server.serverType === 'plugin';
        const pluginID = isPlugin ? pluginIDFromServerOrigin(server.url) : '';
        const displayName = isPlugin ? stripPluginPrefix(tool.name, pluginID) : tool.name;

        return (
            <MCPToolConfigRow
                key={tool.name}
                tool={tool}
                toolConfig={getToolConfig(tool)}
                onToolConfigChange={(updatedConfig) =>
                    handleToolConfigChange(tool.name, updatedConfig)
                }
                serverDisabled={!serverEnabled}
                displayName={displayName}
            />
        );
    };

    // Tools are grouped by the server's readOnlyHint annotation. A server
    // that annotates none of its tools gets one ungrouped list: "write"
    // would be a guess for every tool.
    const readOnlyTools = server.tools.filter((tool) => tool.readOnlyHint);
    const writeTools = server.tools.filter((tool) => !tool.readOnlyHint);
    const toolGroups = readOnlyTools.length > 0 ? [
        {
            key: 'read',
            tools: readOnlyTools,
            title: intl.formatMessage({defaultMessage: 'Read-only tools'}),
            description: intl.formatMessage({defaultMessage: 'The server reports that these tools only read data.'}),
            ariaLabel: intl.formatMessage({defaultMessage: 'Approval policy for all read-only tools on {serverName}'}, {serverName: server.name}),
        },
        {
            key: 'write',
            tools: writeTools,
            title: intl.formatMessage({defaultMessage: 'Write tools'}),
            description: intl.formatMessage({defaultMessage: 'Tools the server does not mark as read-only. They may create, change, or delete data.'}),
            ariaLabel: intl.formatMessage({defaultMessage: 'Approval policy for all write tools on {serverName}'}, {serverName: server.name}),
        },
    ].filter((group) => group.tools.length > 0) : [];

    return (
        <ServerRowContainer>
            <ServerRowHeader>
                <ServerRowExpandButton
                    type='button'
                    onClick={() => setExpanded(!expanded)}
                    aria-expanded={expanded}
                >
                    <ServerAvatar>
                        {server.name.charAt(0).toUpperCase()}
                    </ServerAvatar>
                    <ServerInfo>
                        <ServerName>{server.name}</ServerName>
                        <ServerMeta>
                            {server.serverType === 'plugin' && (
                                <Tag label={<FormattedMessage defaultMessage='Plugin'/>}/>
                            )}
                            {server.error && (
                                <ErrorIndicator>
                                    <ExclamationThickIcon size={16}/>
                                    <FormattedMessage defaultMessage='Error'/>
                                </ErrorIndicator>
                            )}
                            {!server.error && server.needsOAuth && (
                                <OAuthIndicator>
                                    <FormattedMessage defaultMessage='Needs OAuth'/>
                                </OAuthIndicator>
                            )}
                            {!server.error && !server.needsOAuth && (
                                <ToolCount>
                                    <FormattedMessage
                                        defaultMessage='{enabled}/{total} tools enabled'
                                        values={{enabled: enabledToolCount, total: server.tools.length}}
                                    />
                                </ToolCount>
                            )}
                        </ServerMeta>
                    </ServerInfo>
                    <ExpandChevron>
                        <StyledChevron $expanded={expanded}>
                            <ChevronDownIcon size={16}/>
                        </StyledChevron>
                    </ExpandChevron>
                </ServerRowExpandButton>
                {serverConfig && (
                    <ToggleWrapper>
                        <ToggleSwitch
                            checked={serverEnabled}
                            onChange={(enabled) => {
                                if (enabled && enableRemoteBlocked) {
                                    return;
                                }
                                handleServerToggle(enabled);
                            }}
                            disabled={enableRemoteBlocked && !serverEnabled}
                            size='medium'
                            ariaLabel={intl.formatMessage(
                                {defaultMessage: 'Enable {serverName}'},
                                {serverName: server.name},
                            )}
                        />
                    </ToggleWrapper>
                )}
            </ServerRowHeader>

            {expanded && (
                <ToolsContainer>
                    {server.error && (
                        <RowNotice
                            type='danger'
                            title={<FormattedMessage defaultMessage='Connection Error'/>}
                            description={server.error}
                        />
                    )}
                    {!server.error && server.needsOAuth && server.oauthURL && (
                        <RowNotice
                            type='info'
                            title={<FormattedMessage defaultMessage='OAuth Required'/>}
                            description={<FormattedMessage defaultMessage="You must authenticate to fetch this server's tool list and configure per-tool approval policies. This only connects your account — each user must authenticate separately."/>}
                            primaryButtonLabel={remoteMcpLicensed ? <FormattedMessage defaultMessage='Connect Account'/> : null}
                            onPrimaryAction={() => window.open(server.oauthURL, '_blank', 'noopener,noreferrer')}
                        />
                    )}
                    {!server.error && !server.needsOAuth && server.tools.length === 0 && (
                        <EmptyTools>
                            <FormattedMessage defaultMessage='No tools available from this server'/>
                        </EmptyTools>
                    )}
                    {!server.error && !server.needsOAuth && server.tools.length > 0 && (
                        <>
                            <BulkRow>
                                <BulkLabel>
                                    <BulkTitle>
                                        <FormattedMessage defaultMessage='All tools'/>
                                    </BulkTitle>
                                    <BulkDescription>
                                        <FormattedMessage defaultMessage='Set the approval policy for every tool on this server.'/>
                                    </BulkDescription>
                                </BulkLabel>
                                {renderBulkPolicyControl(
                                    server.tools,
                                    intl.formatMessage({defaultMessage: 'Approval policy for all tools on {serverName}'}, {serverName: server.name}),
                                )}
                            </BulkRow>
                            {toolGroups.length === 0 && server.tools.map(renderToolRow)}
                            {toolGroups.map((group) => (
                                <ToolGroup
                                    key={group.key}
                                    role='group'
                                    aria-label={group.title}
                                >
                                    <BulkRow>
                                        <BulkLabel>
                                            <BulkTitle>
                                                {group.title}
                                                <GroupCount>{group.tools.length}</GroupCount>
                                            </BulkTitle>
                                            <BulkDescription>{group.description}</BulkDescription>
                                        </BulkLabel>
                                        {renderBulkPolicyControl(group.tools, group.ariaLabel)}
                                    </BulkRow>
                                    {group.tools.map(renderToolRow)}
                                </ToolGroup>
                            ))}
                        </>
                    )}
                </ToolsContainer>
            )}
        </ServerRowContainer>
    );
};

// Styled components
const ServerRowContainer = styled.div`
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    border-radius: var(--radius-s);
    background-color: var(--center-channel-bg);
    overflow: hidden;
`;

const ServerRowHeader = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-xs);
    padding-right: var(--spacing-l);
`;

const ServerRowExpandButton = styled.button`
    display: flex;
    align-items: center;
    flex: 1;
    min-width: 0;
    padding: var(--spacing-m) var(--spacing-xs) var(--spacing-m) var(--spacing-l);
    cursor: pointer;
    gap: var(--spacing-xs);
    border: none;
    background: none;
    text-align: left;
    font: inherit;
    color: inherit;

    &:hover {
        background-color: rgba(var(--center-channel-color-rgb), 0.04);
    }
`;

const ServerAvatar = styled.div`
    width: var(--spacing-xxxxl);
    height: var(--spacing-xxxxl);
    border-radius: var(--radius-full);
    background: rgba(var(--center-channel-color-rgb), 0.08);
    display: flex;
    align-items: center;
    justify-content: center;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    font-size: var(--font-size-100);
    font-weight: var(--font-weight-semibold);
    flex-shrink: 0;
`;

const ServerInfo = styled.div`
    display: flex;
    flex-direction: column;
    flex: 1;
    min-width: 0;
    gap: 2px;
`;

const ServerName = styled.div`
    font-family: var(--font-family-body, 'Open Sans', sans-serif);
    font-weight: var(--font-weight-semibold);
    font-size: var(--font-size-100);
    line-height: var(--line-height-100);
    color: var(--center-channel-color);
`;

const ServerMeta = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-xs);
`;

const ToolCount = styled.span`
    font-family: var(--font-family-body, 'Open Sans', sans-serif);
    font-size: var(--font-size-75);
    font-weight: var(--font-weight-regular);
    line-height: var(--line-height-75);
    color: rgba(var(--center-channel-color-rgb), 0.75);
`;

const ToggleWrapper = styled.div`
    display: flex;
    align-items: center;
    flex-shrink: 0;
`;

const ExpandChevron = styled.div`
    display: flex;
    align-items: center;
    justify-content: center;
    margin-left: auto;
    padding: var(--spacing-xs);
    flex-shrink: 0;
`;

const StyledChevron = styled.div<{$expanded: boolean}>`
    display: flex;
    align-items: center;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    transform: ${(props) => (props.$expanded ? 'rotate(0deg)' : 'rotate(-90deg)')};
    transition: transform 0.2s;
`;

const ToolsContainer = styled.div`
    background-color: rgba(var(--center-channel-color-rgb), 0.04);
    border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    display: flex;
    flex-direction: column;
    gap: var(--spacing-l);
    padding: var(--spacing-m) 0;
`;

const ToolGroup = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-l);
    padding-top: var(--spacing-m);
    border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;

// Lines the bulk select up with the per-tool policy selects: the tool rows
// put a toggle and a details button to the right of theirs.
const BulkRow = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-m);
    padding: 0 calc(var(--spacing-l) + 90px) 0 var(--spacing-l);
`;

const BulkLabel = styled.div`
    display: flex;
    flex-direction: column;
    flex: 1;
    min-width: 0;
`;

const BulkTitle = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-xs);
    font-size: var(--font-size-100);
    font-weight: var(--font-weight-semibold);
    line-height: var(--line-height-100);
    color: var(--center-channel-color);
`;

const GroupCount = styled.span`
    font-size: var(--font-size-75);
    font-weight: var(--font-weight-regular);
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const BulkDescription = styled.div`
    font-size: var(--font-size-75);
    line-height: var(--line-height-75);
    color: rgba(var(--center-channel-color-rgb), 0.75);
`;

const BulkPolicySelect = styled.div`
    width: 200px;
    flex-shrink: 0;
`;

const ErrorIndicator = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-xxxs);
    font-size: var(--font-size-75);
    font-weight: var(--font-weight-semibold);
    color: var(--error-text);
`;

const OAuthIndicator = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-xxxs);
    font-size: var(--font-size-75);
    font-weight: var(--font-weight-semibold);
    color: var(--button-bg);
`;

const RowNotice = styled(SectionNotice)`
    margin: 0 var(--spacing-l);
`;

const EmptyTools = styled.div`
    text-align: center;
    padding: var(--spacing-l);
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

export default MCPServerToolRow;
