// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {ChevronDownIcon, ChevronRightIcon} from '@mattermost/compass-icons/components';

import {getUserMCPTools} from '@/client';
import {EnabledTool} from '@/types/agents';

// Types matching the getUserMCPTools() response shape (from api/api_mcp.go)
type UserMCPToolInfo = {
    name: string;
    description: string;
    enabled: boolean;   // admin-level enabled state
    policy: string;     // "auto_run" | "ask"
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
    onChange: (tools: EnabledTool[]) => void;
}

const McpsTab = (props: Props) => {
    const {enabledTools, onChange} = props;
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
        return enabledTools.some(
            (t) => t.server_origin === serverOrigin && t.tool_name === toolName,
        );
    }, [enabledTools]);

    const toggleTool = useCallback((serverOrigin: string, toolName: string) => {
        const exists = enabledTools.some(
            (t) => t.server_origin === serverOrigin && t.tool_name === toolName,
        );
        if (exists) {
            onChange(enabledTools.filter(
                (t) => !(t.server_origin === serverOrigin && t.tool_name === toolName),
            ));
        } else {
            onChange([...enabledTools, {server_origin: serverOrigin, tool_name: toolName}]);
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
        const allEnabled = serverTools.every((t) => isToolEnabled(server.serverOrigin, t.name));

        if (allEnabled) {
            // Remove all tools for this server
            onChange(enabledTools.filter((t) => t.server_origin !== server.serverOrigin));
        } else {
            // Add all enabled tools for this server
            const existing = enabledTools.filter((t) => t.server_origin !== server.serverOrigin);
            const newTools = serverTools.map((t) => ({
                server_origin: server.serverOrigin,
                tool_name: t.name,
            }));
            onChange([...existing, ...newTools]);
        }
    }, [enabledTools, isToolEnabled, onChange]);

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
            <SearchInput
                type='text'
                placeholder={intl.formatMessage({defaultMessage: 'Search servers and tools...'})}
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
            />

            <ServerList>
                {filteredServers.map((server) => {
                    const isExpanded = expandedServers.has(server.serverOrigin);
                    const enabledCount = server.tools.filter(
                        (t) => t.enabled && isToolEnabled(server.serverOrigin, t.name),
                    ).length;
                    const totalCount = server.tools.filter((t) => t.enabled).length;

                    return (
                        <ServerBlock key={server.serverOrigin}>
                            <ServerHeader onClick={() => toggleServer(server.serverOrigin)}>
                                <ChevronContainer>
                                    {isExpanded ? <ChevronDownIcon size={16}/> : <ChevronRightIcon size={16}/>}
                                </ChevronContainer>
                                <ServerInfo>
                                    <ServerName>{server.name}</ServerName>
                                    <ServerMeta>
                                        {enabledCount > 0
                                            ? intl.formatMessage(
                                                {defaultMessage: '{enabled} of {total} tools enabled'},
                                                {enabled: enabledCount, total: totalCount},
                                            )
                                            : intl.formatMessage(
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
                                <ServerToggle
                                    onClick={(e) => {
                                        e.stopPropagation();
                                        toggleAllServerTools(server);
                                    }}
                                    $enabled={enabledCount === totalCount && totalCount > 0}
                                >
                                    <ToggleKnob $enabled={enabledCount === totalCount && totalCount > 0}/>
                                </ServerToggle>
                            </ServerHeader>

                            {isExpanded && (
                                <ToolList>
                                    {server.tools.filter((t) => t.enabled).map((tool) => (
                                        <ToolRow key={tool.name}>
                                            <ToolInfo>
                                                <ToolName>{tool.name}</ToolName>
                                                {tool.description && (
                                                    <ToolDescription>{tool.description}</ToolDescription>
                                                )}
                                            </ToolInfo>
                                            <ToolToggle
                                                onClick={() => toggleTool(server.serverOrigin, tool.name)}
                                                $enabled={isToolEnabled(server.serverOrigin, tool.name)}
                                            >
                                                <ToggleKnob $enabled={isToolEnabled(server.serverOrigin, tool.name)}/>
                                            </ToolToggle>
                                        </ToolRow>
                                    ))}
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

const ServerHeader = styled.div`
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 12px 16px;
    cursor: pointer;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.04);
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
    background: ${(p) => p.$enabled ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.24)'};
`;

const ToggleKnob = styled.div<{$enabled: boolean}>`
    width: 18px;
    height: 18px;
    border-radius: 50%;
    background: white;
    position: absolute;
    top: 2px;
    transition: left 0.2s ease;
    left: ${(p) => p.$enabled ? '20px' : '2px'};
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
