// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useMemo, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {ChevronDownIcon, ChevronRightIcon} from '@mattermost/compass-icons/components';

import {getUserMCPTools} from '@/client';
import {EnabledTool} from '@/types/agents';
import {useMCPConnectionEvents} from '@/hooks/use_mcp_connection_events';
import {pluginIDFromServerOrigin, stripPluginPrefix} from '@/utils/tool_names';

import {filterMcpsServersBySearchQuery} from './mcp_servers_filter';

// Same sentinel as llm.MCPServerToolWildcard ('*' = all tools from that origin).
const MCPServerToolWildcard = '*';

// Types matching the getUserMCPTools() response shape (from api/api_mcp.go)
type UserMCPToolInfo = {
    name: string;
    description: string;
    enabled: boolean; // admin-level enabled state
    policy: string; // "auto_run" | "ask"
}

type UserMCPServerInfo = {
    name: string;
    serverOrigin: string;
    authenticated: boolean;
    needsOAuth: boolean;
    authEmail: string;
    authURL?: string;
    tools: UserMCPToolInfo[];
}

type Props = {
    enabledTools: EnabledTool[];
    autoEnableNewMCPTools: boolean;
    onChange: (updates: {enabledTools?: EnabledTool[]; autoEnableNewMCPTools?: boolean}) => void;
}

function serverToolsPanelId(serverOrigin: string): string {
    return `mcp-tools-${serverOrigin.replace(/[^a-zA-Z0-9_-]/g, '_')}`;
}

const McpsTab = (props: Props) => {
    const {enabledTools, autoEnableNewMCPTools, onChange} = props;
    const intl = useIntl();
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
            const response = await getUserMCPTools();
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
    }, [intl]);

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
            return s.tools.some((t) => t.name === et.tool_name);
        });
    }, [servers]);

    const orphanedTools = useMemo(() => {
        if (autoEnableNewMCPTools || servers.length === 0) {
            return [];
        }
        return enabledTools.filter((et) => !isEntryAvailable(et));
    }, [autoEnableNewMCPTools, enabledTools, servers, isEntryAvailable]);

    useEffect(() => {
        if (!autoEnableNewMCPTools && orphanedTools.length > 0 && servers.length > 0) {
            const cleaned = enabledTools.filter((et) => isEntryAvailable(et));
            onChange({enabledTools: cleaned});
        }
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [autoEnableNewMCPTools, enabledTools, servers]);

    // Search is implemented in mcp_servers_filter (see unit tests for query length rules).
    const filteredServers = useMemo(
        () => filterMcpsServersBySearchQuery(servers, searchQuery),
        [servers, searchQuery],
    );

    if (loading) {
        return (
            <LoadingContainer>
                <FormattedMessage defaultMessage='Loading MCP tools...'/>
            </LoadingContainer>
        );
    }

    if (error) {
        return <ErrorContainer>{error}</ErrorContainer>;
    }

    if (servers.length === 0) {
        return (
            <EmptyContainer>
                <FormattedMessage defaultMessage='No MCP servers are configured. Ask your system administrator to configure MCP servers in the system console.'/>
            </EmptyContainer>
        );
    }

    return (
        <Container>
            <AutoEnableRow>
                <AutoEnableCheckbox
                    type='checkbox'
                    id='mcp-auto-enable'
                    checked={autoEnableNewMCPTools}
                    onChange={(e) => onChange({autoEnableNewMCPTools: e.target.checked})}
                />
                <AutoEnableLabel htmlFor='mcp-auto-enable'>
                    <AutoEnableTitle>
                        <FormattedMessage defaultMessage='Automatically enable all MCP tools'/>
                    </AutoEnableTitle>
                    <AutoEnableHint>
                        <FormattedMessage defaultMessage='Give this agent access to every currently available MCP tool and any added in the future.'/>
                    </AutoEnableHint>
                </AutoEnableLabel>
            </AutoEnableRow>

            <SearchInput
                type='text'
                placeholder={intl.formatMessage({defaultMessage: 'Search servers and tools...'})}
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                disabled={autoEnableNewMCPTools}
            />

            {autoEnableNewMCPTools && (
                <AutoEnableBanner>
                    <FormattedMessage defaultMessage='Every MCP tool is enabled for this agent. Disable "Automatically enable all MCP tools" above to pick specific tools.'/>
                </AutoEnableBanner>
            )}

            {orphanedTools.length > 0 && (
                <OrphanedToolsWarning>
                    <FormattedMessage
                        defaultMessage='{count, plural, one {# tool is} other {# tools are}} from servers that are no longer available. They will be removed on save.'
                        values={{count: orphanedTools.length}}
                    />
                </OrphanedToolsWarning>
            )}

            <ServerList>
                {filteredServers.map((server) => {
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
                    const serverToggleLabel = allOn ? intl.formatMessage(
                        {defaultMessage: 'Disable all tools for {serverName}'},
                        {serverName: server.name},
                    ) : intl.formatMessage(
                        {defaultMessage: 'Enable all tools for {serverName}'},
                        {serverName: server.name},
                    );
                    const canConnect = !server.authenticated && Boolean(server.authURL);
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
                        <ServerBlock key={server.serverOrigin}>
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
                                            {server.authenticated && (
                                                <AuthBadge>
                                                    <FormattedMessage defaultMessage='Connected'/>
                                                </AuthBadge>
                                            )}
                                            {!server.authenticated && server.authEmail === '' && server.tools.length === 0 && (
                                                <NotConnectedBadge>
                                                    <FormattedMessage defaultMessage='Not connected'/>
                                                </NotConnectedBadge>
                                            )}
                                        </ServerMeta>
                                    </ServerInfo>
                                </ServerHeaderButton>
                                {canConnect && (
                                    <ConnectButton
                                        type='button'
                                        onClick={() => {
                                            window.open(server.authURL!, '_blank', 'noopener,noreferrer');
                                        }}
                                    >
                                        <FormattedMessage defaultMessage='Connect'/>
                                    </ConnectButton>
                                )}
                                <ServerToggle
                                    type='button'
                                    aria-label={serverToggleLabel}
                                    aria-checked={allOn}
                                    onClick={() => !autoEnableNewMCPTools && toggleAllServerTools(server)}
                                    disabled={autoEnableNewMCPTools}
                                    $enabled={allOn}
                                >
                                    <ToggleKnob $enabled={allOn}/>
                                </ServerToggle>
                            </ServerTopRow>

                            {isExpanded && (
                                <ToolList
                                    id={toolsPanelId}
                                    role='region'
                                    aria-label={server.name}
                                >
                                    {adminEnabledTools.length === 0 && wildcardOn && (
                                        <EmptyToolsNotice>
                                            <FormattedMessage defaultMessage='This server has no tools available right now. When a user of this agent authenticates, every tool this server exposes will be enabled.'/>
                                        </EmptyToolsNotice>
                                    )}
                                    {adminEnabledTools.length === 0 && !wildcardOn && canConnect && (
                                        <EmptyToolsNotice>
                                            <FormattedMessage defaultMessage='Connect this server to see and pick individual tools, or toggle it on to give the agent access to every tool the server exposes once a user connects.'/>
                                        </EmptyToolsNotice>
                                    )}
                                    {(() => {
                                        // Strip the pluginmcp "<pluginID>__" prefix for display
                                        // only; wire tool.name remains the enable/disable identity.
                                        const pluginID = pluginIDFromServerOrigin(server.serverOrigin);
                                        const toolsDisabled = autoEnableNewMCPTools || wildcardOn;
                                        return adminEnabledTools.map((tool) => {
                                            const toolOn = isToolEnabled(server.serverOrigin, tool.name);
                                            const displayName = pluginID ? stripPluginPrefix(tool.name, pluginID) : tool.name;
                                            return (
                                                <ToolRow key={tool.name}>
                                                    <ToolInfo>
                                                        <ToolName>{displayName}</ToolName>
                                                        {tool.description && (
                                                            <ToolDescription>{tool.description}</ToolDescription>
                                                        )}
                                                    </ToolInfo>
                                                    <ToolToggle
                                                        type='button'
                                                        aria-label={toolOn ? intl.formatMessage(
                                                            {defaultMessage: 'Disable tool {toolName} on {serverName}'},
                                                            {toolName: displayName, serverName: server.name},
                                                        ) : intl.formatMessage(
                                                            {defaultMessage: 'Enable tool {toolName} on {serverName}'},
                                                            {toolName: displayName, serverName: server.name},
                                                        )}
                                                        onClick={() => !toolsDisabled && toggleTool(server.serverOrigin, tool.name)}
                                                        disabled={toolsDisabled}
                                                        $enabled={toolOn}
                                                    >
                                                        <ToolToggleKnob $enabled={toolOn}/>
                                                    </ToolToggle>
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

const AutoEnableRow = styled.div`
    display: flex;
    align-items: flex-start;
    gap: 10px;
`;

const AutoEnableCheckbox = styled.input`
    margin-top: 2px;
    cursor: pointer;
`;

const AutoEnableLabel = styled.label`
    display: flex;
    flex-direction: column;
    gap: 2px;
    cursor: pointer;
    user-select: none;
`;

const AutoEnableTitle = styled.span`
    font-size: 14px;
    font-weight: 600;
    color: var(--center-channel-color);
`;

const AutoEnableHint = styled.span`
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const AutoEnableBanner = styled.div`
    padding: 8px 12px;
    background: rgba(var(--button-bg-rgb), 0.08);
    border-radius: 4px;
    border: 1px solid rgba(var(--button-bg-rgb), 0.24);
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 13px;
`;

const SearchInput = styled.input`
    width: 100%;
    padding: 8px 12px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    background: var(--center-channel-bg);
    color: var(--center-channel-color);
    font-size: 14px;

    &:focus {
        border-color: var(--button-bg);
        outline: none;
    }

    &::placeholder {
        color: rgba(var(--center-channel-color-rgb), 0.48);
    }
`;

const ServerList = styled.div`
    display: flex;
    flex-direction: column;
    gap: 8px;
`;

const ServerBlock = styled.div`
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    border-radius: 4px;
    overflow: hidden;
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

const AuthBadge = styled.span`
    display: inline-flex;
    align-items: center;
    padding: 1px 6px;
    border-radius: 10px;
    background: rgba(var(--online-indicator-rgb, 61, 184, 135), 0.12);
    color: var(--online-indicator, #3DB887);
    font-size: 11px;
    font-weight: 600;
`;

const NotConnectedBadge = styled.span`
    display: inline-flex;
    align-items: center;
    padding: 1px 6px;
    border-radius: 10px;
    background: rgba(var(--center-channel-color-rgb), 0.08);
    color: rgba(var(--center-channel-color-rgb), 0.56);
    font-size: 11px;
    font-weight: 600;
`;

// Toggle switch — styled to match Mattermost toggle patterns
const ServerToggle = styled.button<{$enabled: boolean}>`
    width: 40px;
    height: 22px;
    border-radius: 11px;
    border: none;
    cursor: pointer;
    position: relative;
    flex-shrink: 0;
    transition: background 0.2s ease;
    background: ${(p) => (p.$enabled ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.24)')};

    &:disabled {
        cursor: not-allowed;
        opacity: 0.5;
    }
`;

const ToggleKnob = styled.div<{$enabled: boolean}>`
    width: 18px;
    height: 18px;
    border-radius: 50%;
    background: white;
    position: absolute;
    top: 2px;
    transition: left 0.2s ease;
    left: ${(p) => (p.$enabled ? '20px' : '2px')};
`;

const ToolList = styled.div`
    border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;

const EmptyToolsNotice = styled.div`
    padding: 12px 16px;
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const ConnectButton = styled.button`
    padding: 4px 10px;
    border-radius: 4px;
    border: none;
    font-size: 12px;
    font-weight: 600;
    white-space: nowrap;
    cursor: pointer;
    flex-shrink: 0;
    background: var(--button-bg);
    color: var(--button-color);

    &:hover:not(:disabled) {
        background: rgba(var(--button-bg-rgb), 0.88);
    }

    &:disabled {
        opacity: 0.5;
        cursor: default;
    }

    &:focus-visible {
        outline: 2px solid var(--button-bg);
        outline-offset: 2px;
    }
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

const ToolToggle = styled(ServerToggle)`
    width: 36px;
    height: 20px;
    border-radius: 10px;
`;

const ToolToggleKnob = styled(ToggleKnob)<{$enabled: boolean}>`
    top: 1px;
    left: ${(p) => (p.$enabled ? '17px' : '1px')};
`;

const OrphanedToolsWarning = styled.div`
    padding: 8px 12px;
    background: rgba(var(--away-indicator-rgb, 255, 188, 66), 0.08);
    border-radius: 4px;
    border: 1px solid rgba(var(--away-indicator-rgb, 255, 188, 66), 0.3);
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 13px;
`;

const LoadingContainer = styled.div`
    display: flex;
    justify-content: center;
    padding: 40px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const ErrorContainer = styled.div`
    padding: 10px 12px;
    background: rgba(var(--dnd-indicator-rgb, 210, 75, 78), 0.08);
    border-radius: 4px;
    border: 1px solid rgba(var(--dnd-indicator-rgb, 210, 75, 78), 0.3);
    color: var(--dnd-indicator, #D24B4E);
    font-size: 14px;
`;

const EmptyContainer = styled.div`
    display: flex;
    justify-content: center;
    padding: 40px 20px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    font-size: 14px;
    text-align: center;
`;

export default McpsTab;
