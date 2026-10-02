// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useMemo, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {ChevronDownIcon, ChevronRightIcon} from '@mattermost/compass-icons/components';

import {Button} from '@mattermost/compass-ui/components/button';
import {Checkbox} from '@mattermost/compass-ui/components/checkbox';
import {EmptyState} from '@mattermost/compass-ui/components/empty-state';
import {SearchInput} from '@mattermost/compass-ui/components/search-input';
import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';
import {Spinner} from '@mattermost/compass-ui/components/spinner';
import {Tag} from '@mattermost/compass-ui/components/tag';

import {getUserMCPTools, type UserMCPServerInfo} from '@/client';
import MCPUnavailableBadge from '@/components/mcp_unavailable_badge';
import {ToggleSwitch} from '@/components/toggle_switch';
import {EnabledTool} from '@/types/agents';
import {useMCPConnectionEvents} from '@/hooks/use_mcp_connection_events';
import {mcpServerStatus, type MCPServerStatus} from '@/utils/mcp_availability';
import {pluginIDFromServerOrigin, stripPluginPrefix} from '@/utils/tool_names';
import {useIsLicensedFor} from '@/license';
import {LicenseChip} from '@/components/system_console/enterprise_chip';

import {filterMcpsServersBySearchQuery} from './mcp_servers_filter';

// Same sentinel as llm.MCPServerToolWildcard ('*' = all tools from that origin).
const MCPServerToolWildcard = '*';

type Props = {
    agentId?: string;
    enabledTools: EnabledTool[];
    autoEnableNewMCPTools: boolean;
    useServiceAccountAuth: boolean;

    /** Soft-lock auto-enable + tool grants while SA is on for non-admins; SA checkbox stays reachable. */
    serviceAccountFieldsLocked: boolean;

    /** From parent: whether the current user may enable service account auth (manage_system). */
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

function serverToolsPanelId(serverOrigin: string): string {
    return `mcp-tools-${serverOrigin.replace(/[^a-zA-Z0-9_-]/g, '_')}`;
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
                <FormattedMessage defaultMessage='This agent cannot use this server until service account credentials are added in System Console MCP settings.'/>
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
    const [error, setError] = useState<string | null>(null);
    const [expandedServers, setExpandedServers] = useState<Set<string>>(new Set());
    const [searchQuery, setSearchQuery] = useState('');

    const loadServers = useCallback(async (opts: {showLoading?: boolean} = {}) => {
        try {
            if (opts.showLoading) {
                setLoading(true);
            }
            const response = await getUserMCPTools({
                agentId,
                serviceAccount: useServiceAccountAuth,
            });
            setServers(response.servers || []);
            setError(null);
        } catch (err) {
            if (opts.showLoading) {
                // eslint-disable-next-line no-console
                console.error('Failed to load MCP tools:', err);
                setError(intl.formatMessage({defaultMessage: 'Failed to load MCP tools.'}));
            } else {
                // eslint-disable-next-line no-console
                console.error('Background refresh of MCP tools failed:', err);
            }
        } finally {
            if (opts.showLoading) {
                setLoading(false);
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

    const toggleServer = useCallback((serverOrigin: string) => {
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

    // Search is implemented in mcp_servers_filter (see unit tests for query length rules).
    const filteredServers = useMemo(
        () => filterMcpsServersBySearchQuery(servers, searchQuery),
        [servers, searchQuery],
    );

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

    // Tool-grant UI is also locked while auto-enable is on (existing behavior).
    const toolGrantsDisabled = serviceAccountFieldsLocked || autoEnableNewMCPTools;

    if (loading) {
        return (
            <Container>
                {serviceAccountSection}
                <LoadingContainer>
                    <Spinner size='20'/>
                    <FormattedMessage defaultMessage='Loading MCP tools...'/>
                </LoadingContainer>
            </Container>
        );
    }

    if (error) {
        return (
            <Container>
                {serviceAccountSection}
                <SectionNotice
                    type='danger'
                    title={error}
                />
            </Container>
        );
    }

    if (servers.length === 0) {
        return (
            <Container>
                {serviceAccountSection}
                <EmptyState
                    title={<FormattedMessage defaultMessage='No MCP servers are configured. Ask your system administrator to configure MCP servers in the system console.'/>}
                />
            </Container>
        );
    }

    return (
        <Container>
            {serviceAccountSection}
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

            <SearchInput
                placeholder={intl.formatMessage({defaultMessage: 'Search servers and tools...'})}
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                onClear={() => setSearchQuery('')}
                disabled={toolGrantsDisabled}
            />

            {useServiceAccountAuth && (
                <SectionNotice
                    type='info'
                    title={<FormattedMessage defaultMessage="This list is the agent's service-account catalog, not your personal MCP connections. Servers that only have service account credentials show as connected here when those credentials work."/>}
                />
            )}

            {autoEnableNewMCPTools && (
                <SectionNotice
                    type='info'
                    title={<FormattedMessage defaultMessage='Every MCP tool is enabled for this agent. Disable "Automatically enable all MCP tools" above to pick specific tools.'/>}
                />
            )}

            {orphanedTools.length > 0 && (
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

            <ServerList>
                {filteredServers.map((server) => {
                    const status = mcpServerStatus(server, useServiceAccountAuth);
                    const unavailable = status === 'sa-only-unavailable';
                    const isExpanded = expandedServers.has(server.serverOrigin);
                    const wildcardOn = hasServerWildcard(server.serverOrigin);
                    const adminEnabledTools = server.tools.filter((t) => t.enabled);
                    const enabledCount = adminEnabledTools.filter(
                        (t) => isToolEnabled(server.serverOrigin, t.name),
                    ).length;
                    const totalCount = adminEnabledTools.length;

                    const toolsPanelId = serverToolsPanelId(server.serverOrigin);

                    const allKnownOn = totalCount > 0 && enabledCount === totalCount;
                    const allOn = autoEnableNewMCPTools || wildcardOn || allKnownOn;
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
                        if (wildcardOn && totalCount === 0) {
                            return intl.formatMessage({defaultMessage: 'All tools enabled'});
                        }
                        if (totalCount === 0) {
                            return intl.formatMessage({defaultMessage: '0 tools available'});
                        }
                        if (enabledCount > 0) {
                            return intl.formatMessage(
                                {defaultMessage: '{enabled} of {total} tools enabled'},
                                {enabled: enabledCount, total: totalCount},
                            );
                        }
                        return intl.formatMessage(
                            {defaultMessage: '{total} tools available'},
                            {total: totalCount},
                        );
                    })();

                    return (
                        <ServerBlock
                            key={server.serverOrigin}
                            $unavailable={unavailable}
                        >
                            <ServerTopRow>
                                <ServerHeaderButton
                                    type='button'
                                    aria-expanded={isExpanded}
                                    aria-controls={toolsPanelId}
                                    aria-label={intl.formatMessage(
                                        {defaultMessage: '{serverName}, {detail}. Press to expand or collapse tools.'},
                                        {serverName: server.name, detail: metaDetail},
                                    )}
                                    onClick={() => toggleServer(server.serverOrigin)}
                                >
                                    <ChevronContainer aria-hidden={true}>
                                        {isExpanded ? <ChevronDownIcon size={16}/> : <ChevronRightIcon size={16}/>}
                                    </ChevronContainer>
                                    <ServerInfo>
                                        <ServerName>{server.name}</ServerName>
                                        <ServerMeta>
                                            {metaDetail}
                                            {serverStatusBadge(status)}
                                        </ServerMeta>
                                    </ServerInfo>
                                </ServerHeaderButton>
                                {canConnect && (
                                    <ConnectButton
                                        emphasis='secondary'
                                        size='small'
                                        onClick={() => {
                                            window.open(server.authURL!, '_blank', 'noopener,noreferrer');
                                        }}
                                    >
                                        <FormattedMessage defaultMessage='Connect'/>
                                    </ConnectButton>
                                )}
                                <ToggleSwitch
                                    ariaLabel={serverToggleLabel}
                                    checked={serverEnabled}
                                    onChange={() => !toolGrantsDisabled && !unavailable && toggleAllServerTools(server)}
                                    disabled={toolGrantsDisabled || unavailable}
                                />
                            </ServerTopRow>

                            {isExpanded && (
                                <ToolList
                                    id={toolsPanelId}
                                    role='region'
                                    aria-label={server.name}
                                >
                                    {adminEnabledTools.length === 0 && emptyToolsNotice(status, {
                                        wildcardOn,
                                        useServiceAccountAuth,
                                        canConnect,
                                    })}
                                    {(() => {
                                        // Strip the pluginmcp "<pluginID>__" prefix for display
                                        // only; wire tool.name remains the enable/disable identity.
                                        const pluginID = pluginIDFromServerOrigin(server.serverOrigin);
                                        const toolsDisabled = toolGrantsDisabled || wildcardOn || unavailable;
                                        return adminEnabledTools.map((tool) => {
                                            const toolOn = !unavailable && isToolEnabled(server.serverOrigin, tool.name);
                                            const displayName = pluginID ? stripPluginPrefix(tool.name, pluginID) : tool.name;
                                            return (
                                                <ToolRow key={tool.name}>
                                                    <ToolInfo>
                                                        <ToolName>{displayName}</ToolName>
                                                        {tool.description && (
                                                            <ToolDescription>{tool.description}</ToolDescription>
                                                        )}
                                                    </ToolInfo>
                                                    <ToggleSwitch
                                                        size='small'
                                                        ariaLabel={toolOn ? intl.formatMessage(
                                                            {defaultMessage: 'Disable tool {toolName} on {serverName}'},
                                                            {toolName: displayName, serverName: server.name},
                                                        ) : intl.formatMessage(
                                                            {defaultMessage: 'Enable tool {toolName} on {serverName}'},
                                                            {toolName: displayName, serverName: server.name},
                                                        )}
                                                        checked={toolOn}
                                                        onChange={() => !toolsDisabled && toggleTool(server.serverOrigin, tool.name)}
                                                        disabled={toolsDisabled}
                                                    />
                                                </ToolRow>
                                            );
                                        });
                                    })()}
                                </ToolList>
                            )}
                        </ServerBlock>
                    );
                })}
            </ServerList>
        </Container>
    );
};

// --- Styled Components ---

const Container = styled.div`
    display: flex;
    flex-direction: column;
    gap: 16px;
`;

const ServiceAccountSection = styled.div`
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding-bottom: 16px;
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;

const CheckboxRow = styled.div`
    display: flex;
    align-items: flex-start;
    gap: 10px;
`;

const CheckboxText = styled.span`
    display: flex;
    flex-direction: column;
    gap: 2px;
`;

const CheckboxTitle = styled.span`
    font-size: 14px;
    font-weight: 600;
    color: var(--center-channel-color);
`;

const CheckboxHint = styled.span`
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const ServerList = styled.div`
    display: flex;
    flex-direction: column;
    gap: 8px;
`;

const ServerBlock = styled.div<{$unavailable?: boolean}>`
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    border-radius: 4px;
    overflow: hidden;
    opacity: ${(p) => (p.$unavailable ? 0.64 : 1)};
`;

const ServerTopRow = styled.div`
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 12px 16px 12px 16px;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.04);
    }
`;

const ServerHeaderButton = styled.button`
    display: flex;
    align-items: center;
    gap: 12px;
    flex: 1;
    min-width: 0;
    cursor: pointer;
    border: none;
    background: transparent;
    padding: 0;
    text-align: left;
    font-family: inherit;
    font-size: inherit;
    color: inherit;

    &:focus-visible {
        outline: 2px solid var(--button-bg);
        outline-offset: 2px;
        border-radius: 4px;
    }
`;

const ChevronContainer = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.56);
    display: flex;
    align-items: center;
    flex-shrink: 0;
`;

const ServerInfo = styled.div`
    display: flex;
    flex-direction: column;
    flex: 1;
    min-width: 0;
`;

const ServerName = styled.div`
    font-size: 14px;
    font-weight: 600;
    color: var(--center-channel-color);
`;

const ServerMeta = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const ToolList = styled.div`
    border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;

const EmptyToolsNotice = styled.div`
    padding: 12px 16px;
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const ConnectButton = styled(Button)`
    flex-shrink: 0;
`;

const ToolRow = styled.div`
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 10px 16px 10px 44px;

    &:not(:last-child) {
        border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.04);
    }

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.02);
    }
`;

const ToolInfo = styled.div`
    display: flex;
    flex-direction: column;
    flex: 1;
    min-width: 0;
`;

const ToolName = styled.div`
    font-size: 13px;
    font-weight: 600;
    color: var(--center-channel-color);
    font-family: monospace;
`;

const ToolDescription = styled.div`
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
`;

const LoadingContainer = styled.div`
    display: flex;
    justify-content: center;
    align-items: center;
    gap: 8px;
    padding: 40px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

export default McpsTab;
