// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useMemo, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {ChevronDownIcon, ChevronRightIcon} from '@mattermost/compass-icons/components';

import {getUserMCPTools} from '@/client';
import {EnabledTool} from '@/types/agents';

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
    authEmail: string;
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

    // Fetch available MCP tools on mount
    useEffect(() => {
        const load = async () => {
            try {
                setLoading(true);
                const response = await getUserMCPTools();
                setServers(response.servers || []);
            } catch {
                setError(intl.formatMessage({defaultMessage: 'Failed to load MCP tools.'}));
            } finally {
                setLoading(false);
            }
        };
        load();
    }, [intl]);

    const isToolEnabled = useCallback((serverOrigin: string, toolName: string) => {
        if (autoEnableNewMCPTools) {
            return true;
        }
        return enabledTools.some(
            (t) => t.server_origin === serverOrigin && t.tool_name === toolName,
        );
    }, [autoEnableNewMCPTools, enabledTools]);

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
        const allEnabled = serverTools.every((t) =>
            enabledTools.some(
                (e) => e.server_origin === server.serverOrigin && e.tool_name === t.name,
            ),
        );

        if (allEnabled) {
            onChange({enabledTools: enabledTools.filter((t) => t.server_origin !== server.serverOrigin)});
        } else {
            const existing = enabledTools.filter((t) => t.server_origin !== server.serverOrigin);
            const newTools = serverTools.map((t) => ({
                server_origin: server.serverOrigin,
                tool_name: t.name,
            }));
            onChange({enabledTools: [...existing, ...newTools]});
        }
    }, [enabledTools, onChange]);

    // Detect orphaned tools (enabled but no longer available)
    const orphanedTools = useMemo(() => {
        if (autoEnableNewMCPTools || servers.length === 0) {
            return [];
        }
        return enabledTools.filter((et) =>
            !servers.some((s) =>
                s.serverOrigin === et.server_origin &&
                s.tools.some((t) => t.name === et.tool_name),
            ),
        );
    }, [autoEnableNewMCPTools, enabledTools, servers]);

    // Auto-remove orphaned tools from enabledTools so they're cleaned on save
    useEffect(() => {
        if (!autoEnableNewMCPTools && orphanedTools.length > 0 && servers.length > 0) {
            const cleaned = enabledTools.filter((et) =>
                servers.some((s) =>
                    s.serverOrigin === et.server_origin &&
                    s.tools.some((t) => t.name === et.tool_name),
                ),
            );
            onChange({enabledTools: cleaned});
        }
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [autoEnableNewMCPTools, enabledTools, servers]);

    // Filter servers/tools by search
    const filteredServers = servers.filter((server) => {
        if (!searchQuery) {
            return true;
        }
        const q = searchQuery.toLowerCase();
        return server.name.toLowerCase().includes(q) ||
            server.tools.some((t) => t.name.toLowerCase().includes(q) || t.description.toLowerCase().includes(q));
    });

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
                    const enabledCount = server.tools.filter(
                        (t) => t.enabled && isToolEnabled(server.serverOrigin, t.name),
                    ).length;
                    const totalCount = server.tools.filter((t) => t.enabled).length;

                    const toolsPanelId = serverToolsPanelId(server.serverOrigin);
                    const allOn = enabledCount === totalCount && totalCount > 0;
                    const serverToggleLabel = allOn ?
                        intl.formatMessage(
                            {defaultMessage: 'Disable all tools for {serverName}'},
                            {serverName: server.name},
                        ) :
                        intl.formatMessage(
                            {defaultMessage: 'Enable all tools for {serverName}'},
                            {serverName: server.name},
                        );

                    return (
                        <ServerBlock key={server.serverOrigin}>
                            <ServerTopRow>
                                <ServerHeaderButton
                                    type='button'
                                    aria-expanded={isExpanded}
                                    aria-controls={toolsPanelId}
                                    aria-label={intl.formatMessage(
                                        {defaultMessage: '{serverName}, {detail}. Press to expand or collapse tools.'},
                                        {
                                            serverName: server.name,
                                            detail: enabledCount > 0 ?
                                                intl.formatMessage(
                                                    {defaultMessage: '{enabled} of {total} tools enabled'},
                                                    {enabled: enabledCount, total: totalCount},
                                                ) :
                                                intl.formatMessage(
                                                    {defaultMessage: '{total} tools available'},
                                                    {total: totalCount},
                                                ),
                                        },
                                    )}
                                    onClick={() => toggleServer(server.serverOrigin)}
                                >
                                    <ChevronContainer aria-hidden={true}>
                                        {isExpanded ? <ChevronDownIcon size={16}/> : <ChevronRightIcon size={16}/>}
                                    </ChevronContainer>
                                    <ServerInfo>
                                        <ServerName>{server.name}</ServerName>
                                        <ServerMeta>
                                            {enabledCount > 0 ?
                                                intl.formatMessage(
                                                    {defaultMessage: '{enabled} of {total} tools enabled'},
                                                    {enabled: enabledCount, total: totalCount},
                                                ) :
                                                intl.formatMessage(
                                                    {defaultMessage: '{total} tools available'},
                                                    {total: totalCount},
                                                )
                                            }
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
                                <ServerToggle
                                    type='button'
                                    aria-label={serverToggleLabel}
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
                                    {server.tools.filter((t) => t.enabled).map((tool) => {
                                        const toolOn = isToolEnabled(server.serverOrigin, tool.name);
                                        return (
                                            <ToolRow key={tool.name}>
                                                <ToolInfo>
                                                    <ToolName>{tool.name}</ToolName>
                                                    {tool.description && (
                                                        <ToolDescription>{tool.description}</ToolDescription>
                                                    )}
                                                </ToolInfo>
                                                <ToolToggle
                                                    type='button'
                                                    aria-label={toolOn ?
                                                        intl.formatMessage(
                                                            {defaultMessage: 'Disable tool {toolName} on {serverName}'},
                                                            {toolName: tool.name, serverName: server.name},
                                                        ) :
                                                        intl.formatMessage(
                                                            {defaultMessage: 'Enable tool {toolName} on {serverName}'},
                                                            {toolName: tool.name, serverName: server.name},
                                                        )}
                                                    onClick={() => !autoEnableNewMCPTools && toggleTool(server.serverOrigin, tool.name)}
                                                    disabled={autoEnableNewMCPTools}
                                                    $enabled={toolOn}
                                                >
                                                    <ToolToggleKnob $enabled={toolOn}/>
                                                </ToolToggle>
                                            </ToolRow>
                                        );
                                    })}
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
