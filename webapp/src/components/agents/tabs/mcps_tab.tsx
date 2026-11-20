// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {RefreshIcon} from '@mattermost/compass-icons/components';

import {Button} from '@mattermost/compass-ui/components/button';
import {Checkbox} from '@mattermost/compass-ui/components/checkbox';
import {EmptyState} from '@mattermost/compass-ui/components/empty-state';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';
import {Tag} from '@mattermost/compass-ui/components/tag';

import {getUserMCPTools, refreshUserMCPTools, type UserMCPServerInfo, type UserMCPToolInfo} from '@/client';
import MCPUnavailableBadge from '@/components/mcp_unavailable_badge';
import MCPServerAccordion, {MCPBulkScope, MCPBulkToggleState} from '@/components/mcp_tools/mcp_server_accordion';
import MCPToolRow from '@/components/mcp_tools/mcp_tool_row';
import MCPToolsBrowser from '@/components/mcp_tools/mcp_tools_browser';
import {filterMCPServers} from '@/components/mcp_tools/mcp_tools_filter';
import {formatToolPolicyLabel} from '@/components/mcp_tools/policy_utils';
import {EnabledTool} from '@/types/agents';
import {useMCPConnectionEvents} from '@/hooks/use_mcp_connection_events';
import {mcpServerStatus, type MCPServerStatus} from '@/utils/mcp_availability';
import {pluginIDFromServerOrigin, stripPluginPrefix} from '@/utils/tool_names';
import {useIsLicensedFor} from '@/license';
import {LicenseChip} from '@/components/system_console/enterprise_chip';

// Same sentinel as llm.MCPServerToolWildcard ('*' = all tools from that origin).
const MCPServerToolWildcard = '*';

type Props = {
    agentId?: string;
    enabledTools: EnabledTool[];
    autoEnableNewMCPTools: boolean;
    useServiceAccountAuth: boolean;

    /** Soft-lock auto-enable + tool grants while SA is on for non-admins; SA checkbox stays reachable. */
    serviceAccountFieldsLocked: boolean;

    /** From parent: whether the current user may enable service account auth (system admin or read/write settings delegate). */
    canEditServiceAccountAuth: boolean;
    onChange: (updates: {
        enabledTools?: EnabledTool[];
        autoEnableNewMCPTools?: boolean;
        useServiceAccountAuth?: boolean;
    }) => void;

    // Optional server-state reconciliation callback. Used when removing entries
    // that no longer exist in the live MCP catalog (orphans). Distinct from
    // onChange so the parent can update its dirty-tracking baseline alongside
    // the draft and avoid treating reconciliation as a user edit (MM-69185).
    onReconcileEnabledTools?: (cleaned: EnabledTool[]) => void;
}

function serverStatusBadge(status: MCPServerStatus): React.ReactNode {
    switch (status) {
    case 'connected':
        return (
            <Tag
                type='success'
                label={<FormattedMessage defaultMessage='Connected'/>}
            />
        );
    case 'no-sa-credentials':
        return <Tag label={<FormattedMessage defaultMessage='No service account credentials'/>}/>;
    case 'sa-connect-failed':
        return <Tag label={<FormattedMessage defaultMessage="Couldn't connect"/>}/>;
    case 'sa-only-unavailable':
        return <MCPUnavailableBadge/>;
    case 'not-connected':
        return <Tag label={<FormattedMessage defaultMessage='Not connected'/>}/>;
    case 'none':
        return null;
    default: {
        const exhaustive: never = status;
        return exhaustive;
    }
    }
}

function emptyToolsNotice(
    status: MCPServerStatus,
    opts: {wildcardOn: boolean; useServiceAccountAuth: boolean; canConnect: boolean},
): React.ReactNode {
    if (opts.wildcardOn) {
        return (
            <EmptyToolsNotice>
                {opts.useServiceAccountAuth ? (
                    <FormattedMessage defaultMessage='This server has no tools available right now. When it connects with the service account, every tool it exposes will be enabled.'/>
                ) : (
                    <FormattedMessage defaultMessage='This server has no tools available right now. When a user of this agent authenticates, every tool this server exposes will be enabled.'/>
                )}
            </EmptyToolsNotice>
        );
    }
    switch (status) {
    case 'no-sa-credentials':
        return (
            <EmptyToolsNotice>
                <FormattedMessage defaultMessage='This agent cannot use this server until service account credentials are added in Agents Settings, under MCP servers.'/>
            </EmptyToolsNotice>
        );
    case 'sa-connect-failed':
        return (
            <EmptyToolsNotice>
                <FormattedMessage defaultMessage="Couldn't connect with the configured service account credentials. Check the header values and server logs."/>
            </EmptyToolsNotice>
        );
    case 'none':
        return opts.canConnect ? (
            <EmptyToolsNotice>
                <FormattedMessage defaultMessage='Connect this server to see and pick individual tools, or toggle it on to give the agent access to every tool the server exposes once a user connects.'/>
            </EmptyToolsNotice>
        ) : null;
    case 'connected':
    case 'sa-only-unavailable':
    case 'not-connected':
        return null;
    default: {
        const exhaustive: never = status;
        return exhaustive;
    }
    }
}

const McpsTab = (props: Props) => {
    const {
        agentId,
        enabledTools,
        autoEnableNewMCPTools,
        useServiceAccountAuth,
        serviceAccountFieldsLocked,
        canEditServiceAccountAuth,
        onChange,
        onReconcileEnabledTools,
    } = props;
    const intl = useIntl();
    const serviceAccountLicensed = useIsLicensedFor('mcp_service_account');
    const remoteMcpLicensed = useIsLicensedFor('remote_mcp');

    const [servers, setServers] = useState<UserMCPServerInfo[]>([]);
    const [loading, setLoading] = useState(true);
    const [refreshing, setRefreshing] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const [refreshError, setRefreshError] = useState<string | null>(null);
    const [expandedServers, setExpandedServers] = useState<Set<string>>(new Set());
    const [collapsedInSearch, setCollapsedInSearch] = useState<Set<string>>(new Set());
    const [searchQuery, setSearchQuery] = useState('');
    const requestRef = useRef(0);

    const handleSearchChange = useCallback((value: string) => {
        setSearchQuery(value);
        setCollapsedInSearch(new Set());
    }, []);

    useEffect(() => () => {
        requestRef.current += 1;
    }, []);

    const loadServers = useCallback(async (opts: {showLoading?: boolean; forceRefresh?: boolean} = {}) => {
        const request = ++requestRef.current;
        try {
            if (opts.showLoading) {
                setLoading(true);
            }
            if (opts.forceRefresh) {
                setRefreshing(true);
                setRefreshError(null);
            }
            const reqOpts = {
                agentId,
                serviceAccount: useServiceAccountAuth,
                agentConfig: true,
            };
            const response = opts.forceRefresh ? await refreshUserMCPTools(reqOpts) : await getUserMCPTools(reqOpts);
            if (request !== requestRef.current) {
                return;
            }
            setServers(response.servers || []);
            setError(null);
            setRefreshError(null);
        } catch (err) {
            if (request !== requestRef.current) {
                return;
            }
            if (opts.forceRefresh) {
                // eslint-disable-next-line no-console
                console.error('Failed to refresh MCP tools:', err);
                setRefreshError(intl.formatMessage({defaultMessage: 'Failed to refresh MCP tools.'}));
            } else if (opts.showLoading) {
                // eslint-disable-next-line no-console
                console.error('Failed to load MCP tools:', err);
                setError(intl.formatMessage({defaultMessage: 'Failed to load MCP tools.'}));
            } else {
                // eslint-disable-next-line no-console
                console.error('Background refresh of MCP tools failed:', err);
            }
        } finally {
            if (request === requestRef.current) {
                if (opts.showLoading) {
                    setLoading(false);
                }
                if (opts.forceRefresh) {
                    setRefreshing(false);
                }
            }
        }
    }, [agentId, intl, useServiceAccountAuth]);

    useEffect(() => {
        loadServers({showLoading: true});
    }, [loadServers]);

    useMCPConnectionEvents(useCallback(() => {
        loadServers();
    }, [loadServers]));

    const hasServerWildcard = useCallback((serverOrigin: string) => {
        return enabledTools.some(
            (t) => t.server_origin === serverOrigin && t.tool_name === MCPServerToolWildcard,
        );
    }, [enabledTools]);

    const isToolEnabled = useCallback((serverOrigin: string, toolName: string) => {
        if (autoEnableNewMCPTools) {
            return true;
        }
        if (hasServerWildcard(serverOrigin)) {
            return true;
        }
        return enabledTools.some(
            (t) => t.server_origin === serverOrigin && t.tool_name === toolName,
        );
    }, [autoEnableNewMCPTools, enabledTools, hasServerWildcard]);

    const toggleTool = useCallback((serverOrigin: string, toolName: string) => {
        const exists = enabledTools.some(
            (t) => t.server_origin === serverOrigin && t.tool_name === toolName,
        );
        if (exists) {
            onChange({enabledTools: enabledTools.filter(
                (t) => !(t.server_origin === serverOrigin && t.tool_name === toolName),
            )});
        } else {
            onChange({enabledTools: [...enabledTools, {server_origin: serverOrigin, tool_name: toolName}]});
        }
    }, [enabledTools, onChange]);

    const setGroupToolsEnabled = useCallback((serverOrigin: string, groupTools: UserMCPToolInfo[], enable: boolean) => {
        const toggleableNames = new Set(groupTools.filter((t) => t.enabled).map((t) => t.name));
        if (toggleableNames.size === 0) {
            return;
        }
        const withoutGroup = enabledTools.filter(
            (et) => !(et.server_origin === serverOrigin && toggleableNames.has(et.tool_name)),
        );
        if (!enable) {
            onChange({enabledTools: withoutGroup});
            return;
        }
        const added = [...toggleableNames].map((toolName) => ({
            server_origin: serverOrigin,
            tool_name: toolName,
        }));
        onChange({enabledTools: [...withoutGroup, ...added]});
    }, [enabledTools, onChange]);

    const toggleServer = useCallback((serverOrigin: string, isFiltered: boolean) => {
        if (isFiltered) {
            setCollapsedInSearch((prev) => {
                const next = new Set(prev);
                if (next.has(serverOrigin)) {
                    next.delete(serverOrigin);
                } else {
                    next.add(serverOrigin);
                }
                return next;
            });
            return;
        }
        setExpandedServers((prev) => {
            const next = new Set(prev);
            if (next.has(serverOrigin)) {
                next.delete(serverOrigin);
            } else {
                next.add(serverOrigin);
            }
            return next;
        });
    }, []);

    const toggleAllServerTools = useCallback((server: UserMCPServerInfo) => {
        const serverTools = server.tools.filter((t) => t.enabled);
        const hasWildcard = hasServerWildcard(server.serverOrigin);
        const allEnabled = hasWildcard || (
            serverTools.length > 0 &&
            serverTools.every((t) =>
                enabledTools.some(
                    (e) => e.server_origin === server.serverOrigin && e.tool_name === t.name,
                ),
            )
        );

        if (allEnabled) {
            onChange({enabledTools: enabledTools.filter((t) => t.server_origin !== server.serverOrigin)});
            return;
        }

        // If every discovered tool on this server is disabled by an admin, do
        // not turn the server on with a wildcard grant.
        if (serverTools.length === 0 && server.tools.length > 0) {
            return;
        }

        const existing = enabledTools.filter((t) => t.server_origin !== server.serverOrigin);
        if (serverTools.length === 0) {
            onChange({enabledTools: [...existing, {server_origin: server.serverOrigin, tool_name: MCPServerToolWildcard}]});
            return;
        }
        const newTools = serverTools.map((t) => ({
            server_origin: server.serverOrigin,
            tool_name: t.name,
        }));
        onChange({enabledTools: [...existing, ...newTools]});
    }, [enabledTools, hasServerWildcard, onChange]);

    const isEntryAvailable = useCallback((et: EnabledTool) => {
        return servers.some((s) => {
            if (s.serverOrigin !== et.server_origin) {
                return false;
            }
            if (et.tool_name === MCPServerToolWildcard) {
                return true;
            }

            // Keep saved grants for SA-only servers the current user cannot
            // use; Unavailable is display-only and must not strip enabledTools.
            if (mcpServerStatus(s, useServiceAccountAuth) === 'sa-only-unavailable') {
                return true;
            }
            return s.tools.some((t) => t.name === et.tool_name);
        });
    }, [servers, useServiceAccountAuth]);

    // Service account agents run against the admin-provisioned catalog. That
    // catalog is what this tab loads when the flag is on, but a failed SA
    // connect looks like "no tools" and must not wipe grants. Skip orphan
    // detection entirely while the flag is on.
    const orphanedTools = useMemo(() => {
        if (useServiceAccountAuth || autoEnableNewMCPTools || servers.length === 0) {
            return [];
        }
        return enabledTools.filter((et) => !isEntryAvailable(et));
    }, [useServiceAccountAuth, autoEnableNewMCPTools, enabledTools, servers, isEntryAvailable]);

    useEffect(() => {
        if (!useServiceAccountAuth && !autoEnableNewMCPTools && orphanedTools.length > 0 && servers.length > 0) {
            const cleaned = enabledTools.filter((et) => isEntryAvailable(et));
            if (onReconcileEnabledTools) {
                onReconcileEnabledTools(cleaned);
            } else {
                onChange({enabledTools: cleaned});
            }
        }
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [useServiceAccountAuth, autoEnableNewMCPTools, enabledTools, servers]);

    const filteredServers = useMemo(() => {
        const searchable = servers.map((server) => {
            const pluginID = pluginIDFromServerOrigin(server.serverOrigin);
            return {
                server,
                name: server.name,
                tools: server.tools.map((tool) => ({
                    tool,
                    name: tool.name,
                    displayName: pluginID ? stripPluginPrefix(tool.name, pluginID) : tool.name,
                    description: tool.description,
                })),
            };
        });
        return filterMCPServers(searchable, searchQuery).map((match) => ({
            server: match.server.server,
            matchedTools: match.matchedTools.map((t) => t.tool),
            isFiltered: match.isFiltered,
        }));
    }, [servers, searchQuery]);

    // Rendered above every catalog state so the setting stays reachable while
    // the catalog is loading, failed or empty. Non-admins see the checkbox only
    // while the draft still has the flag on, so they can turn it off; unchecking
    // unmounts the section (Cancel restores the persisted value).
    const serviceAccountSection = (!canEditServiceAccountAuth && !useServiceAccountAuth) ? null : (
        <ServiceAccountSection>
            <CheckboxRow>
                <Checkbox
                    id='mcp-use-service-accounts'
                    checked={useServiceAccountAuth}
                    disabled={!serviceAccountLicensed && !useServiceAccountAuth}
                    onChange={(e) => {
                        if (e.target.checked && !serviceAccountLicensed) {
                            return;
                        }
                        onChange({useServiceAccountAuth: e.target.checked});
                    }}
                >
                    <CheckboxText>
                        <CheckboxTitle>
                            <FormattedMessage defaultMessage='Use service accounts for authentication'/>
                        </CheckboxTitle>
                        <CheckboxHint>
                            <FormattedMessage defaultMessage="External MCP servers authenticate with shared service-account credentials. Mattermost and plugin tools run with each requesting user's own permissions. Users are never asked to connect their own accounts."/>
                        </CheckboxHint>
                    </CheckboxText>
                </Checkbox>
                {!serviceAccountLicensed && (
                    <LicenseChip capability='mcp_service_account'/>
                )}
            </CheckboxRow>
            {useServiceAccountAuth && (
                <div role='status'>
                    <SectionNotice
                        type='warning'
                        title={<FormattedMessage defaultMessage="Anyone who can use this agent acts with its shared service account access on external MCP servers. Mattermost (embedded) and plugin tools run with each requesting user's own permissions. Restrict who can use this agent on the Access tab. External MCP servers without service account credentials configured are excluded from this agent."/>}
                    />
                </div>
            )}
        </ServiceAccountSection>
    );

    const toolGrantsDisabled = serviceAccountFieldsLocked || autoEnableNewMCPTools;
    const totalTools = servers.reduce((sum, server) => sum + server.tools.length, 0);
    const hasServers = !loading && !error && servers.length > 0;

    const beforeSearch = (
        <>
            {serviceAccountSection}
            {hasServers && (
                <CheckboxRow>
                    <Checkbox
                        id='mcp-auto-enable'
                        checked={autoEnableNewMCPTools}
                        disabled={serviceAccountFieldsLocked}
                        onChange={(e) => onChange({autoEnableNewMCPTools: e.target.checked})}
                    >
                        <CheckboxText>
                            <CheckboxTitle>
                                <FormattedMessage defaultMessage='Automatically enable all MCP tools'/>
                            </CheckboxTitle>
                            <CheckboxHint>
                                <FormattedMessage defaultMessage='Give this agent access to every currently available MCP tool and any added in the future.'/>
                            </CheckboxHint>
                        </CheckboxText>
                    </Checkbox>
                </CheckboxRow>
            )}
        </>
    );

    const notices = (
        <>
            {error && (
                <SectionNotice
                    type='danger'
                    title={error}
                />
            )}

            {refreshError && (
                <SectionNotice
                    type='danger'
                    title={refreshError}
                />
            )}

            {hasServers && useServiceAccountAuth && (
                <SectionNotice
                    type='info'
                    title={<FormattedMessage defaultMessage="This list is the agent's service-account catalog, not your personal MCP connections. Servers that only have service account credentials show as connected here when those credentials work."/>}
                />
            )}

            {hasServers && autoEnableNewMCPTools && (
                <SectionNotice
                    type='info'
                    title={<FormattedMessage defaultMessage='Every MCP tool is enabled for this agent. Disable "Automatically enable all MCP tools" above to pick specific tools.'/>}
                />
            )}

            {hasServers && orphanedTools.length > 0 && (
                <div role='status'>
                    <SectionNotice
                        type='warning'
                        title={(
                            <FormattedMessage
                                defaultMessage='{count, plural, one {# tool is} other {# tools are}} from servers that are no longer available. They will be removed on save.'
                                values={{count: orphanedTools.length}}
                            />
                        )}
                    />
                </div>
            )}
        </>
    );

    const summary = hasServers ? (
        <FormattedMessage
            defaultMessage='{totalTools} tools from {serverCount} servers'
            values={{totalTools, serverCount: servers.length}}
        />
    ) : null;

    const actions = hasServers ? (
        <Button
            emphasis='tertiary'
            size='small'
            onClick={() => loadServers({forceRefresh: true})}
            loading={refreshing}
            leadingIcon={<Icon glyph={<RefreshIcon/>}/>}
        >
            <FormattedMessage defaultMessage='Refresh Tools'/>
        </Button>
    ) : null;

    const emptyState = (() => {
        if (error) {
            return null;
        }
        if (servers.length === 0) {
            return (
                <EmptyState
                    title={<FormattedMessage defaultMessage='No MCP servers are configured. Ask your system administrator to add MCP servers in the Agents settings.'/>}
                />
            );
        }
        return null;
    })();

    const bulkToggleAriaLabel = (serverName: string, scope: MCPBulkScope, allEnabled: boolean): string => {
        switch (scope) {
        case 'read':
            return allEnabled ? intl.formatMessage(
                {defaultMessage: 'Disable all read-only tools on {serverName}'},
                {serverName},
            ) : intl.formatMessage(
                {defaultMessage: 'Enable all read-only tools on {serverName}'},
                {serverName},
            );
        case 'write':
            return allEnabled ? intl.formatMessage(
                {defaultMessage: 'Disable all write tools on {serverName}'},
                {serverName},
            ) : intl.formatMessage(
                {defaultMessage: 'Enable all write tools on {serverName}'},
                {serverName},
            );
        case 'all':
        default:
            return allEnabled ? intl.formatMessage(
                {defaultMessage: 'Disable all tools on {serverName}'},
                {serverName},
            ) : intl.formatMessage(
                {defaultMessage: 'Enable all tools on {serverName}'},
                {serverName},
            );
        }
    };

    return (
        <MCPToolsBrowser
            beforeSearch={beforeSearch}
            summary={summary}
            actions={actions}
            searchQuery={searchQuery}
            onSearchChange={handleSearchChange}
            showSearch={hasServers}
            notices={notices}
            loading={loading}
            loadingLabel={<FormattedMessage defaultMessage='Loading MCP tools...'/>}
            emptyState={emptyState}
            noSearchResults={hasServers && searchQuery.trim() !== '' && filteredServers.length === 0}
        >
            {hasServers && filteredServers.map(({server, matchedTools, isFiltered}) => {
                const status = mcpServerStatus(server, useServiceAccountAuth);
                const unavailable = status === 'sa-only-unavailable';
                const isExpanded = isFiltered ? !collapsedInSearch.has(server.serverOrigin) : expandedServers.has(server.serverOrigin);
                const wildcardOn = hasServerWildcard(server.serverOrigin);
                const adminEnabledTools = server.tools.filter((t) => t.enabled);
                const adminDisabledCount = server.tools.length - adminEnabledTools.length;
                const allAdminDisabled = server.tools.length > 0 && adminEnabledTools.length === 0;
                const enabledCount = adminEnabledTools.filter(
                    (t) => isToolEnabled(server.serverOrigin, t.name),
                ).length;
                const totalAvailableCount = adminEnabledTools.length;

                const allKnownOn = totalAvailableCount > 0 && enabledCount === totalAvailableCount;
                const allOn = !allAdminDisabled && (autoEnableNewMCPTools || wildcardOn || allKnownOn);
                const serverEnabled = !unavailable && allOn;
                const serverToggleLabel = serverEnabled ? intl.formatMessage(
                    {defaultMessage: 'Disable all tools for {serverName}'},
                    {serverName: server.name},
                ) : intl.formatMessage(
                    {defaultMessage: 'Enable all tools for {serverName}'},
                    {serverName: server.name},
                );
                const canConnect = remoteMcpLicensed && status === 'none' && Boolean(server.authURL);
                const metaDetail = (() => {
                    if (wildcardOn && totalAvailableCount === 0 && !allAdminDisabled) {
                        return intl.formatMessage({defaultMessage: 'All tools enabled'});
                    }
                    if (totalAvailableCount === 0) {
                        return intl.formatMessage({defaultMessage: '0 tools available'});
                    }
                    if (enabledCount > 0) {
                        return intl.formatMessage(
                            {defaultMessage: '{enabled} of {total} tools enabled'},
                            {enabled: enabledCount, total: totalAvailableCount},
                        );
                    }
                    return intl.formatMessage(
                        {defaultMessage: '{total} tools available'},
                        {total: totalAvailableCount},
                    );
                })();

                const pluginID = pluginIDFromServerOrigin(server.serverOrigin);
                const toolsDisabled = toolGrantsDisabled || wildcardOn || unavailable;

                const getBulkToggleState = (groupTools: UserMCPToolInfo[], scope: MCPBulkScope): MCPBulkToggleState => {
                    const toggleable = groupTools.filter((t) => t.enabled);
                    const groupAllOn = !unavailable && toggleable.length > 0 && (
                        autoEnableNewMCPTools ||
                        wildcardOn ||
                        toggleable.every((t) => isToolEnabled(server.serverOrigin, t.name))
                    );
                    return {
                        checked: groupAllOn,
                        disabled: toolsDisabled || toggleable.length === 0,
                        ariaLabel: bulkToggleAriaLabel(server.name, scope, groupAllOn),
                        onChange: (checked) => !toolsDisabled && setGroupToolsEnabled(server.serverOrigin, groupTools, checked),
                    };
                };

                const renderToolRow = (tool: UserMCPToolInfo) => {
                    const adminDisabled = !tool.enabled;
                    const toolOn = !unavailable && !adminDisabled && isToolEnabled(server.serverOrigin, tool.name);
                    const displayName = pluginID ? stripPluginPrefix(tool.name, pluginID) : tool.name;
                    const rowToggleDisabled = toolsDisabled || adminDisabled;
                    const ariaLabel = toolOn ? intl.formatMessage(
                        {defaultMessage: 'Disable tool {toolName} on {serverName}'},
                        {toolName: displayName, serverName: server.name},
                    ) : intl.formatMessage(
                        {defaultMessage: 'Enable tool {toolName} on {serverName}'},
                        {toolName: displayName, serverName: server.name},
                    );
                    return (
                        <MCPToolRow
                            key={tool.name}
                            displayName={displayName}
                            description={tool.description}
                            inputSchema={tool.inputSchema}
                            badges={adminDisabled ? (
                                <Tag label={<FormattedMessage defaultMessage='Disabled by admin'/>}/>
                            ) : null}
                            policyControl={(
                                <Tag label={formatToolPolicyLabel(intl, tool.policy)}/>
                            )}
                            enabled={toolOn}
                            toggleDisabled={rowToggleDisabled}
                            toggleAriaLabel={ariaLabel}
                            onToggleEnabled={() => !rowToggleDisabled && toggleTool(server.serverOrigin, tool.name)}
                            dimmed={unavailable || adminDisabled}
                        />
                    );
                };

                const serverNotices = (
                    <>
                        {server.tools.length === 0 && emptyToolsNotice(status, {
                            wildcardOn,
                            useServiceAccountAuth,
                            canConnect,
                        })}
                        {allAdminDisabled && (
                            <EmptyToolsNotice>
                                <FormattedMessage defaultMessage='All tools on this server are disabled by an administrator.'/>
                            </EmptyToolsNotice>
                        )}
                    </>
                );

                return (
                    <MCPServerAccordion
                        key={server.serverOrigin}
                        serverName={server.name}
                        serverIdKey={server.serverOrigin}
                        serverType={server.kind}
                        meta={(
                            <>
                                <span>{metaDetail}</span>
                                {adminDisabledCount > 0 && (
                                    <Tag
                                        label={(
                                            <FormattedMessage
                                                defaultMessage='{count, plural, one {# disabled by admin} other {# disabled by admin}}'
                                                values={{count: adminDisabledCount}}
                                            />
                                        )}
                                    />
                                )}
                                {serverStatusBadge(status)}
                            </>
                        )}
                        expandAriaLabel={intl.formatMessage(
                            {defaultMessage: '{serverName}, {detail}. Press to expand or collapse tools.'},
                            {serverName: server.name, detail: metaDetail},
                        )}
                        expanded={isExpanded}
                        onToggleExpanded={() => toggleServer(server.serverOrigin, isFiltered)}
                        dimmed={unavailable}
                        headerActions={canConnect ? (
                            <ConnectButton
                                emphasis='secondary'
                                size='small'
                                onClick={() => {
                                    window.open(server.authURL!, '_blank', 'noopener,noreferrer');
                                }}
                            >
                                <FormattedMessage defaultMessage='Connect'/>
                            </ConnectButton>
                        ) : null}
                        serverToggle={{
                            checked: serverEnabled,
                            disabled: toolGrantsDisabled || unavailable || allAdminDisabled,
                            ariaLabel: serverToggleLabel,
                            onChange: () => !toolGrantsDisabled && !unavailable && !allAdminDisabled && toggleAllServerTools(server),
                        }}
                        notices={serverNotices}
                        tools={matchedTools}
                        totalToolCount={server.tools.length}
                        isSearchFiltered={isFiltered}
                        allToolsDescription={<FormattedMessage defaultMessage='Enable or disable every available tool on this server.'/>}
                        getBulkToggleState={getBulkToggleState}
                        renderToolRow={renderToolRow}
                    />
                );
            })}
        </MCPToolsBrowser>
    );
};

const ServiceAccountSection = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-m);
    padding-bottom: var(--spacing-l);
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;

const CheckboxRow = styled.div`
    display: flex;
    align-items: flex-start;
    gap: var(--spacing-s);
`;

const CheckboxText = styled.span`
    display: flex;
    flex-direction: column;
    gap: 2px;
`;

const CheckboxTitle = styled.span`
    font-size: var(--font-size-100);
    font-weight: var(--font-weight-semibold);
    color: var(--center-channel-color);
`;

const CheckboxHint = styled.span`
    font-size: var(--font-size-75);
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const EmptyToolsNotice = styled.div`
    padding: 0 var(--spacing-l);
    font-size: var(--font-size-75);
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const ConnectButton = styled(Button)`
    flex-shrink: 0;
`;

export default McpsTab;
