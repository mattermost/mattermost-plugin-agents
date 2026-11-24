// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useState} from 'react';
import styled from 'styled-components';
import {ExclamationThickIcon} from '@mattermost/compass-icons/components';
import {FormattedMessage, useIntl} from 'react-intl';

import {Button} from '@mattermost/compass-ui/components/button';
import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';
import {Tag} from '@mattermost/compass-ui/components/tag';
import {WithTooltip} from '@mattermost/shared/components/tooltip';

import {useIsLicensedFor} from '@/license';

import {pluginIDFromServerOrigin, stripPluginPrefix} from '../../utils/tool_names';
import MCPServerAccordion, {MCPBulkScope, MCPBulkToggleState} from '../mcp_tools/mcp_server_accordion';

import {SelectField} from './item';
import {MCPServerConfig, MCPServerInfo, MCPToolConfig, MCPToolInfo} from './mcp_types';
import MCPToolConfigRow, {useToolPolicyOptions} from './mcp_tool_config_row';
import {reviewToolConfigs} from './mcp_tool_review';
import {useSettingsReadOnly} from './settings_read_only';

type MCPServerToolRowProps = {
    server: MCPServerInfo;
    serverConfig: MCPServerConfig | null;
    onServerConfigChange: (config: MCPServerConfig) => void;

    // reviewing renders the row inside the tool review: expanded, without the
    // server toggle or review markers.
    reviewing?: boolean;

    // Optional search-filtered subset of server.tools supplied by MCPToolsViewer.
    visibleTools?: MCPToolInfo[];
    isSearchFiltered?: boolean;
};

const MCPServerToolRow = ({
    server,
    serverConfig,
    onServerConfigChange,
    reviewing = false,
    visibleTools,
    isSearchFiltered = false,
}: MCPServerToolRowProps) => {
    const readOnly = useSettingsReadOnly();
    const [manualExpanded, setManualExpanded] = useState(reviewing);
    const [collapsedInSearch, setCollapsedInSearch] = useState(false);
    const intl = useIntl();
    const remoteMcpLicensed = useIsLicensedFor('remote_mcp');
    const approvalPoliciesLicensed = useIsLicensedFor('tool_approval_policies');
    const policyOptions = useToolPolicyOptions();
    const isRemoteMcp = server.serverType === 'remote' || server.serverType === 'plugin';
    const enableRemoteBlocked = isRemoteMcp && !remoteMcpLicensed;

    useEffect(() => {
        if (!isSearchFiltered) {
            setCollapsedInSearch(false);
        }
    }, [isSearchFiltered]);

    const expanded = reviewing || (isSearchFiltered ? !collapsedInSearch : manualExpanded);
    const toggleExpanded = () => {
        if (isSearchFiltered) {
            setCollapsedInSearch((prev) => !prev);
            return;
        }
        setManualExpanded((prev) => !prev);
    };
    const toolsToDisplay = visibleTools ?? server.tools;

    const findStoredConfig = (toolName: string) => serverConfig?.tool_configs?.find((tc) => tc.name === toolName);

    // A remote server's tool is unreviewed until an admin chooses its policy.
    const marksUnreviewed = server.serverType === 'remote' && !reviewing;
    const isUnreviewed = (tool: MCPToolInfo) => marksUnreviewed && !findStoredConfig(tool.name)?.policy;
    const unreviewedCount = server.tools.filter(isUnreviewed).length;

    // The tool's config as shown: a tool with no stored entry is enabled. A
    // tool with no chosen policy shows its default, or, while unreviewed, the
    // suggestion that marking it reviewed stores (auto run in DMs for tools
    // the MCP server marks read-only).
    const getToolConfig = (tool: MCPToolInfo): MCPToolConfig => {
        const defaultPolicy = (isUnreviewed(tool) && tool.suggestedPolicy) || tool.defaultPolicy || 'ask';
        const existing = findStoredConfig(tool.name);
        if (existing) {
            return existing.policy ? existing : {...existing, policy: defaultPolicy};
        }
        return {name: tool.name, policy: defaultPolicy, enabled: true};
    };

    // Stores the shown suggestion as the chosen policy of every unreviewed
    // tool; policies an admin already chose are kept.
    const handleMarkReviewed = () => {
        if (serverConfig) {
            onServerConfigChange({...serverConfig, tool_configs: reviewToolConfigs(serverConfig, server.tools)});
        }
    };

    const enabledToolCount = server.tools.filter((tool) => getToolConfig(tool).enabled).length;
    const serverEnabled = serverConfig?.enabled ?? false;

    const handleServerToggle = (enabled: boolean) => {
        if (serverConfig) {
            onServerConfigChange({...serverConfig, enabled});
        }
    };

    const handleToolConfigChange = (tool: MCPToolInfo, changedToolConfig: MCPToolConfig, policyPicked = false) => {
        if (!serverConfig) {
            return;
        }

        // Only a policy the admin picks is stored. Toggling the tool or
        // editing its override keeps an unchosen policy empty, so the tool
        // keeps following its default.
        const policyChosen = policyPicked || Boolean(findStoredConfig(tool.name)?.policy);
        const updatedToolConfig = policyChosen ? changedToolConfig : {...changedToolConfig, policy: '' as const};

        const existingConfigs = serverConfig.tool_configs || [];
        const existingIndex = existingConfigs.findIndex((tc) => tc.name === tool.name);
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

    // Sets enabled on every tool in tools without choosing a policy for
    // unreviewed tools and while keeping each tool's retrieval override.
    const handleBulkEnabledChange = (tools: MCPToolInfo[], enabled: boolean) => {
        if (!serverConfig) {
            return;
        }
        const names = new Set(tools.map((tool) => tool.name));
        const existingConfigs = serverConfig.tool_configs || [];
        const configuredNames = new Set(existingConfigs.map((tc) => tc.name));
        const updatedConfigs = existingConfigs.map((tc) => (names.has(tc.name) ? {...tc, enabled} : tc));
        for (const tool of tools) {
            if (!configuredNames.has(tool.name)) {
                updatedConfigs.push({name: tool.name, policy: '', enabled});
            }
        }
        onServerConfigChange({...serverConfig, tool_configs: updatedConfigs});
    };

    // The policy every tool in tools shares, or '' when they differ.
    const sharedPolicy = (tools: MCPToolInfo[]): string => {
        const policies = new Set(tools.map((tool) => getToolConfig(tool).policy));
        return policies.size === 1 ? [...policies][0] : '';
    };

    const bulkPolicyDisabled = readOnly || !serverConfig || !serverEnabled || !approvalPoliciesLicensed;
    const bulkToggleDisabled = readOnly || !serverConfig || !serverEnabled;

    const bulkPolicyAriaLabel = (scope: MCPBulkScope): string => {
        switch (scope) {
        case 'read':
            return intl.formatMessage({defaultMessage: 'Approval policy for all read-only tools on {serverName}'}, {serverName: server.name});
        case 'write':
            return intl.formatMessage({defaultMessage: 'Approval policy for all write tools on {serverName}'}, {serverName: server.name});
        case 'all':
        default:
            return intl.formatMessage({defaultMessage: 'Approval policy for all tools on {serverName}'}, {serverName: server.name});
        }
    };

    const bulkToggleAriaLabel = (scope: MCPBulkScope, allEnabled: boolean): string => {
        switch (scope) {
        case 'read':
            return allEnabled ? intl.formatMessage(
                {defaultMessage: 'Disable all read-only tools on {serverName}'},
                {serverName: server.name},
            ) : intl.formatMessage(
                {defaultMessage: 'Enable all read-only tools on {serverName}'},
                {serverName: server.name},
            );
        case 'write':
            return allEnabled ? intl.formatMessage(
                {defaultMessage: 'Disable all write tools on {serverName}'},
                {serverName: server.name},
            ) : intl.formatMessage(
                {defaultMessage: 'Enable all write tools on {serverName}'},
                {serverName: server.name},
            );
        case 'all':
        default:
            return allEnabled ? intl.formatMessage(
                {defaultMessage: 'Disable all tools on {serverName}'},
                {serverName: server.name},
            ) : intl.formatMessage(
                {defaultMessage: 'Enable all tools on {serverName}'},
                {serverName: server.name},
            );
        }
    };

    const renderBulkPolicy = (tools: MCPToolInfo[], scope: MCPBulkScope) => (
        <BulkPolicySelect>
            <SelectField
                value={sharedPolicy(tools)}
                onChange={(policy) => handleBulkPolicyChange(tools, policy as MCPToolConfig['policy'])}
                options={policyOptions}
                disabled={bulkPolicyDisabled}
                placeholder={intl.formatMessage({defaultMessage: 'Mixed'})}
                ariaLabel={bulkPolicyAriaLabel(scope)}
            />
        </BulkPolicySelect>
    );

    const getBulkToggleState = (tools: MCPToolInfo[], scope: MCPBulkScope): MCPBulkToggleState => {
        const allEnabled = tools.length > 0 && tools.every((tool) => getToolConfig(tool).enabled);
        return {
            checked: allEnabled,
            disabled: bulkToggleDisabled || tools.length === 0,
            ariaLabel: bulkToggleAriaLabel(scope, allEnabled),
            onChange: (checked) => handleBulkEnabledChange(tools, checked),
        };
    };

    const renderToolRow = (tool: MCPToolInfo) => {
        const isPlugin = server.serverType === 'plugin';
        const pluginID = isPlugin ? pluginIDFromServerOrigin(server.url) : '';
        const displayName = isPlugin ? stripPluginPrefix(tool.name, pluginID) : tool.name;

        return (
            <MCPToolConfigRow
                key={tool.name}
                tool={tool}
                toolConfig={getToolConfig(tool)}
                onToolConfigChange={(updatedConfig, policyChosen) =>
                    handleToolConfigChange(tool, updatedConfig, policyChosen)
                }
                serverDisabled={!serverEnabled}
                serverName={server.name}
                displayName={displayName}
                notReviewed={isUnreviewed(tool)}
            />
        );
    };

    const markReviewedDisabledByVisibility = !expanded || isSearchFiltered;
    const markReviewedButton = (
        <Button
            emphasis='secondary'
            size='small'
            onClick={handleMarkReviewed}
            disabled={readOnly || !serverEnabled || markReviewedDisabledByVisibility}
            aria-label={intl.formatMessage({defaultMessage: 'Mark tools on {serverName} as reviewed'}, {serverName: server.name})}
        >
            <FormattedMessage defaultMessage='Mark tools as reviewed'/>
        </Button>
    );

    const headerActions = (() => {
        if (unreviewedCount <= 0 || !serverConfig) {
            return null;
        }
        if (!markReviewedDisabledByVisibility) {
            return markReviewedButton;
        }

        // Tools are marked reviewed only when the admin can see the full tool
        // list (expanded and not narrowed by search).
        const tooltip = isSearchFiltered ?
            intl.formatMessage({defaultMessage: 'Clear search to review all tools on this server'}) :
            intl.formatMessage({defaultMessage: 'Open the tool list to review its tools'});
        return (
            <WithTooltip title={tooltip}>
                <InactiveButtonAnchor>{markReviewedButton}</InactiveButtonAnchor>
            </WithTooltip>
        );
    })();

    const meta = (
        <>
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
            {unreviewedCount > 0 && (
                <Tag
                    type='warning'
                    label={(
                        <FormattedMessage
                            defaultMessage='{count, plural, one {# tool not reviewed} other {# tools not reviewed}}'
                            values={{count: unreviewedCount}}
                        />
                    )}
                />
            )}
        </>
    );

    const showTools = !server.error && !server.needsOAuth && toolsToDisplay.length > 0;

    const notices = (
        <>
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
                    description={<FormattedMessage defaultMessage='Sign in to fetch tools and configure policies. Connects only your account; each user signs in separately.'/>}
                    primaryButtonLabel={remoteMcpLicensed ? <FormattedMessage defaultMessage='Connect Account'/> : null}
                    onPrimaryAction={() => window.open(server.oauthURL, '_blank', 'noopener,noreferrer')}
                />
            )}
            {!server.error && !server.needsOAuth && server.tools.length === 0 && (
                <EmptyTools>
                    <FormattedMessage defaultMessage='No tools available from this server'/>
                </EmptyTools>
            )}
            {showTools && unreviewedCount > 0 && (
                <RowNotice
                    type='warning'
                    title={(
                        <FormattedMessage
                            defaultMessage='{count, plural, one {# tool has not been reviewed} other {# tools have not been reviewed}}'
                            values={{count: unreviewedCount}}
                        />
                    )}
                    description={<FormattedMessage defaultMessage='Suggested policies apply once you mark tools reviewed or pick a policy and save. Until then, they use their default (usually Ask Every Time).'/>}
                />
            )}
        </>
    );

    return (
        <MCPServerAccordion
            serverName={server.name}
            serverType={server.serverType}
            meta={meta}
            expanded={expanded}
            onToggleExpanded={toggleExpanded}
            headerActions={headerActions}
            serverToggle={serverConfig && !reviewing ? {
                checked: serverEnabled,
                disabled: readOnly || (enableRemoteBlocked && !serverEnabled),
                ariaLabel: intl.formatMessage(
                    {defaultMessage: 'Enable {serverName}'},
                    {serverName: server.name},
                ),
                onChange: (enabled) => {
                    if (enabled && enableRemoteBlocked) {
                        return;
                    }
                    handleServerToggle(enabled);
                },
            } : null}
            notices={notices}
            tools={showTools ? toolsToDisplay : []}
            totalToolCount={server.tools.length}
            isSearchFiltered={isSearchFiltered}
            allToolsDescription={<FormattedMessage defaultMessage='Enable, disable, or set the approval policy for every tool on this server.'/>}
            getBulkToggleState={getBulkToggleState}
            renderBulkPolicy={renderBulkPolicy}
            renderToolRow={renderToolRow}
        />
    );
};

// Hovering a disabled button reaches no handler, so the tooltip anchors on
// this wrapper instead.
const InactiveButtonAnchor = styled.span`
    display: inline-flex;

    button {
        pointer-events: none;
    }
`;

const ToolCount = styled.span`
    font-family: var(--font-family-body, 'Open Sans', sans-serif);
    font-size: var(--font-size-75);
    font-weight: var(--font-weight-regular);
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
