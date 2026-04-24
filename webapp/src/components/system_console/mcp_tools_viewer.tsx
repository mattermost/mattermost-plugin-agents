// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef, useState} from 'react';
import styled from 'styled-components';
import {RefreshIcon, ExclamationThickIcon} from '@mattermost/compass-icons/components';
import {FormattedMessage} from 'react-intl';

import {TertiaryButton, SecondaryButton} from '../assets/buttons';
import {getMCPTools, clearMCPToolsCache, getVettedToolSeed, updatePluginServerEnabled} from '../../client';

import {MCPConfig, MCPServerConfig, MCPToolConfig} from './mcp_servers';
import MCPServerToolRow from './mcp_server_tool_row';
import {EMBEDDED_MATTERMOST_BASE_URL} from './vetted_tool_configs';

// Type definitions matching the backend API response
export type MCPToolInfo = {
    name: string;
    description: string;
    inputSchema: {[key: string]: any} | null;
};

export type MCPServerInfo = {
    name: string;
    url: string;
    tools: MCPToolInfo[];
    needsOAuth: boolean;
    oauthURL?: string;
    error: string | null;

    // Discriminator: "embedded" | "remote" | "plugin". Optional for back-compat
    // with older server builds; post-Phase-1F the backend always sets it.
    serverType?: string;

    // Server-side Enabled state. Authoritative for plugin entries (whose config
    // lives in the agents-plugin registry, not mcpConfig). For embedded always
    // true; for remote mirrors mcpConfig.servers[i].enabled.
    enabled?: boolean;
};

export type MCPToolsResponse = {
    servers: MCPServerInfo[];
};

type MCPToolsViewerProps = {
    mcpConfig: MCPConfig;
    onConfigChange: (config: MCPConfig) => void;
    initialToolsData?: MCPToolsResponse | null;
};

// Main component for MCP Tools viewer
const MCPToolsViewer = ({mcpConfig, onConfigChange, initialToolsData}: MCPToolsViewerProps) => {
    const [toolsData, setToolsData] = useState<MCPToolsResponse | null>(initialToolsData || null);
    const [loading, setLoading] = useState(false);
    const [clearing, setClearing] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const [clearSuccess, setClearSuccess] = useState<string | null>(null);
    const seededRef = useRef(false);

    // Fetch tools data from the API
    const fetchTools = async () => {
        setLoading(true);
        setError(null);

        try {
            const response = await getMCPTools();
            setToolsData(response);
        } catch (err) {
            setError(err instanceof Error ? err.message : 'Failed to fetch MCP tools');
        } finally {
            setLoading(false);
        }
    };

    // Clear the MCP tools cache
    const handleClearCache = async () => {
        setClearing(true);
        setError(null);
        setClearSuccess(null);

        try {
            const response = await clearMCPToolsCache();
            setClearSuccess(response.message);

            // Automatically refresh tools after clearing cache
            await fetchTools();
        } catch (err) {
            setError(err instanceof Error ? err.message : 'Failed to clear cache');
        } finally {
            setClearing(false);
        }
    };

    // Fetch tools on component mount (skip if pre-loaded data is available)
    useEffect(() => {
        if (!initialToolsData) {
            fetchTools();
        }
    }, []); // eslint-disable-line react-hooks/exhaustive-deps

    // Retroactively seed vetted tool configs for existing servers.
    // This runs once after tools are first fetched, to fix servers configured before
    // the vetted-tools feature was added. It merges missing vetted configs into any
    // existing tool_configs rather than skipping servers that already have partial configs.
    useEffect(() => {
        if (!toolsData || seededRef.current) {
            return;
        }
        seededRef.current = true;

        (async () => {
            // Plugin-registered MCP servers are intentionally NOT seeded here:
            // their tool_configs aren't persisted in mcpConfig (Phase 1F scope;
            // plugin tools default-allow via filterToolsByConfig's synthetic
            // entry at mcp/client_manager.go).
            let updatedConfig = mcpConfig;
            let changed = false;

            const updatedServers = await Promise.all(
                updatedConfig.servers.map(async (sc) => {
                    let seeded: MCPToolConfig[] = [];
                    try {
                        seeded = await getVettedToolSeed(sc.baseURL);
                    } catch {
                        return sc;
                    }
                    if (seeded.length === 0) {
                        return sc;
                    }
                    const existing = sc.tool_configs || [];
                    const existingNames = new Set(existing.map((tc) => tc.name));
                    const missing = seeded.filter((tc) => !existingNames.has(tc.name));
                    if (missing.length === 0) {
                        return sc;
                    }
                    changed = true;
                    return {...sc, tool_configs: [...existing, ...missing]};
                }),
            );
            if (changed) {
                updatedConfig = {...updatedConfig, servers: updatedServers};
            }

            const embeddedCfg = updatedConfig.embeddedServer;
            {
                let seeded: MCPToolConfig[] = [];
                try {
                    seeded = await getVettedToolSeed(EMBEDDED_MATTERMOST_BASE_URL);
                } catch {
                    seeded = [];
                }
                if (seeded.length > 0) {
                    const existing = embeddedCfg.tool_configs || [];
                    const existingNames = new Set(existing.map((tc) => tc.name));
                    const missing = seeded.filter((tc) => !existingNames.has(tc.name));
                    if (missing.length > 0) {
                        changed = true;
                        updatedConfig = {
                            ...updatedConfig,
                            embeddedServer: {...embeddedCfg, tool_configs: [...existing, ...missing]},
                        };
                    }
                }
            }

            if (changed) {
                onConfigChange(updatedConfig);
            }
        })().catch(() => null);
    }, [toolsData]); // eslint-disable-line react-hooks/exhaustive-deps

    // Calculate total tools across all servers
    const totalTools = toolsData?.servers.reduce((sum, server) => sum + server.tools.length, 0) || 0;
    const serversWithErrors = toolsData?.servers.filter((server) => server.error).length || 0;

    // The embedded server uses this key as its origin/URL
    const embeddedClientKey = EMBEDDED_MATTERMOST_BASE_URL;

    // Find the matching ServerConfig for a discovered server.
    //
    // Three branches:
    //   - Embedded: synthesized from mcpConfig.embeddedServer.
    //   - Plugin (serverType === 'plugin'): synthesized from the MCPServerInfo
    //     payload itself. Plugin-server config lives server-side (registry, not
    //     mcpConfig); the authoritative Enabled bit arrives via server.enabled.
    //     tool_configs is intentionally left empty — Phase 1F default-allows
    //     every plugin tool via filterToolsByConfig's synthetic-entry path.
    //   - Remote: looked up in mcpConfig.servers by name or baseURL.
    //
    // Before the plugin branch existed, plugin entries returned null, hiding
    // the toggle (mcp_server_tool_row.tsx:106) and silently dropping tool-config
    // writes (mcp_server_tool_row.tsx:50-52).
    const findServerConfig = (server: MCPServerInfo): MCPServerConfig | null => {
        if (server.url === embeddedClientKey) {
            return {
                name: server.name,
                enabled: mcpConfig.embeddedServer.enabled,
                baseURL: embeddedClientKey,
                headers: {},
                tool_configs: mcpConfig.embeddedServer.tool_configs,
            };
        }

        if (server.serverType === 'plugin') {
            return {
                name: server.name,
                enabled: server.enabled ?? false,
                baseURL: server.url,
                headers: {},
                tool_configs: [],
            };
        }

        return mcpConfig.servers.find((sc) =>
            sc.name === server.name || sc.baseURL === server.url,
        ) || null;
    };

    // Update a specific server's config.
    //
    // Plugin entries are special: their Enabled state lives in the agents-plugin
    // registry, not in mcpConfig. Toggling them routes through the admin-only
    // PUT /admin/mcp/plugin-servers/:pluginID endpoint (see api_admin.go).
    // tool_configs mutations for plugin entries are NOT persisted in Phase 1F;
    // plugin tools default-allow via filterToolsByConfig's synthetic entry, and
    // per-plugin-tool config is deferred to a later phase.
    const handleServerConfigChange = (
        serverInfo: MCPServerInfo,
        updatedServerConfig: MCPServerConfig,
    ) => {
        // Handle the embedded server: write changes back to embeddedServer config
        if (updatedServerConfig.baseURL === embeddedClientKey) {
            onConfigChange({
                ...mcpConfig,
                embeddedServer: {
                    ...mcpConfig.embeddedServer,
                    tool_configs: updatedServerConfig.tool_configs,
                },
            });
            return;
        }

        // Handle plugin-registered entries: only the Enabled bit is persistable
        // in Phase 1F, and only via the admin endpoint.
        if (serverInfo.serverType === 'plugin') {
            // pluginID is the first segment after "plugin://". The backend
            // generates the synthetic URL "plugin://<pluginID><path>" in
            // handleGetMCPTools; keep parsing defensive.
            const pluginID = serverInfo.url.replace(/^plugin:\/\//, '').split('/')[0];
            if (pluginID) {
                updatePluginServerEnabled(pluginID, updatedServerConfig.enabled).
                    then(() => fetchTools()).
                    catch((err) => {
                        setError(err instanceof Error ? err.message : 'Failed to update plugin server');
                    });
            }
            return;
        }

        const updatedServers = mcpConfig.servers.map((sc) => {
            if (sc.name === updatedServerConfig.name || sc.baseURL === updatedServerConfig.baseURL) {
                return updatedServerConfig;
            }
            return sc;
        });
        onConfigChange({...mcpConfig, servers: updatedServers});
    };

    return (
        <Container>
            <Header>
                <HeaderInfo>
                    <Title>
                        <FormattedMessage defaultMessage='MCP Tools Configuration'/>
                    </Title>
                    {toolsData && (
                        <Summary>
                            <FormattedMessage
                                defaultMessage='{totalTools} tools from {serverCount} servers'
                                values={{
                                    totalTools,
                                    serverCount: toolsData.servers.length,
                                }}
                            />
                            {serversWithErrors > 0 && (
                                <ErrorCount>
                                    <FormattedMessage
                                        defaultMessage=' ({errorCount} with errors)'
                                        values={{errorCount: serversWithErrors}}
                                    />
                                </ErrorCount>
                            )}
                        </Summary>
                    )}
                </HeaderInfo>
                <ButtonGroup>
                    <SecondaryButton
                        onClick={handleClearCache}
                        disabled={clearing || loading}
                    >
                        <FormattedMessage defaultMessage='Clear Cache'/>
                    </SecondaryButton>
                    <RefreshButton
                        onClick={fetchTools}
                        disabled={loading || clearing}
                    >
                        <RefreshIcon
                            size={16}
                        />
                        <FormattedMessage defaultMessage='Refresh Tools'/>
                    </RefreshButton>
                </ButtonGroup>
            </Header>

            <Content>
                {clearSuccess && (
                    <SuccessState>
                        <FormattedMessage defaultMessage='Cache cleared successfully'/>
                    </SuccessState>
                )}

                {loading && !toolsData && (
                    <LoadingState>
                        <FormattedMessage defaultMessage='Loading tools...'/>
                    </LoadingState>
                )}

                {error && (
                    <ErrorState>
                        <ExclamationThickIcon size={24}/>
                        <div>
                            <FormattedMessage defaultMessage='Failed to load MCP tools'/>
                            <div>{error}</div>
                        </div>
                    </ErrorState>
                )}

                {toolsData && toolsData.servers.length === 0 && (
                    <EmptyState>
                        <FormattedMessage defaultMessage='No MCP servers configured'/>
                    </EmptyState>
                )}

                {toolsData && toolsData.servers.length > 0 && (
                    <ServersList>
                        {toolsData.servers.map((server) => (
                            <MCPServerToolRow
                                key={server.url}
                                server={server}
                                serverConfig={findServerConfig(server)}
                                onServerConfigChange={(updatedConfig) =>
                                    handleServerConfigChange(server, updatedConfig)
                                }
                            />
                        ))}
                    </ServersList>
                )}
            </Content>
        </Container>
    );
};

// Styled components
const Container = styled.div`
    display: flex;
    flex-direction: column;
    gap: 16px;
`;

const Header = styled.div`
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: 16px;
`;

const HeaderInfo = styled.div`
    display: flex;
    flex-direction: column;
    gap: 4px;
`;

const Title = styled.h3`
    margin: 0;
    font-size: 18px;
    font-weight: 600;
    color: var(--center-channel-color);
`;

const Summary = styled.div`
    font-size: 14px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    display: flex;
    align-items: center;
    gap: 4px;
`;

const ErrorCount = styled.span`
    color: var(--error-text);
`;

const ButtonGroup = styled.div`
    display: flex;
    gap: 8px;
    align-items: center;
`;

const RefreshButton = styled(TertiaryButton)`
    white-space: nowrap;

    @keyframes spin {
        from {
            transform: rotate(0deg);
        }
        to {
            transform: rotate(360deg);
        }
    }
`;

const Content = styled.div`
    display: flex;
    flex-direction: column;
    gap: 16px;
`;

const SuccessState = styled.div`
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 12px 16px;
    color: var(--online-indicator);
    background-color: rgba(var(--online-indicator-rgb), 0.08);
    border: 1px solid rgba(var(--online-indicator-rgb), 0.16);
    border-radius: 4px;
    font-weight: 600;
`;

const LoadingState = styled.div`
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 32px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    background-color: rgba(var(--center-channel-color-rgb), 0.04);
    border-radius: 4px;
`;

const ErrorState = styled.div`
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 16px;
    color: var(--error-text);
    background-color: rgba(var(--error-text-color-rgb), 0.08);
    border: 1px solid rgba(var(--error-text-color-rgb), 0.16);
    border-radius: 4px;
`;

const EmptyState = styled.div`
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 32px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    background-color: rgba(var(--center-channel-color-rgb), 0.04);
    border-radius: 4px;
`;

const ServersList = styled.div`
    display: flex;
    flex-direction: column;
    gap: 12px;
`;

export default MCPToolsViewer;
