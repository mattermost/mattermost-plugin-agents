// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import styled from 'styled-components';
import {RefreshIcon, ExclamationThickIcon} from '@mattermost/compass-icons/components';
import {FormattedMessage, useIntl} from 'react-intl';

import {TertiaryButton, SecondaryButton} from '../assets/buttons';
import {getMCPTools, clearMCPToolsCache, getVettedToolSeed, updatePluginServer} from '../../client';
import {useMCPConnectionEvents} from '../../hooks/use_mcp_connection_events';
import {pluginIDFromServerOrigin} from '../../utils/tool_names';

import {MCPConfig, MCPServerConfig, MCPToolConfig} from './mcp_servers';
import MCPServerToolRow from './mcp_server_tool_row';
import {EMBEDDED_MATTERMOST_BASE_URL} from './vetted_tool_configs';

export type MCPToolInfo = {
    name: string;
    description: string;
    inputSchema: Record<string, unknown> | null;
};

export type MCPServerInfo = {
    name: string;
    url: string;
    tools: MCPToolInfo[];
    needsOAuth: boolean;
    oauthURL?: string;
    error: string | null;

    // Plugin-server fields; remote and embedded rows read state from mcpConfig.
    serverType?: string;
    enabled?: boolean;
    toolConfigs?: MCPToolConfig[];
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
    const intl = useIntl();
    const [toolsData, setToolsData] = useState<MCPToolsResponse | null>(initialToolsData || null);
    const [loading, setLoading] = useState(false);
    const [clearing, setClearing] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const [clearSuccess, setClearSuccess] = useState<string | null>(null);
    const seededRef = useRef(false);

    const fetchTools = useCallback(async (opts: {showLoading?: boolean; propagateError?: boolean} = {}) => {
        try {
            if (opts.showLoading) {
                setLoading(true);
            }
            const response = await getMCPTools();
            setToolsData(response);
            setError(null);
        } catch (err) {
            if (opts.propagateError) {
                throw err;
            }
            if (opts.showLoading) {
                setError(err instanceof Error ? err.message : 'Failed to fetch MCP tools');
            } else {
                // eslint-disable-next-line no-console
                console.error('Background refresh of MCP tools failed:', err);
            }
        } finally {
            if (opts.showLoading) {
                setLoading(false);
            }
        }
    }, []);

    // Clear the MCP tools cache
    const handleClearCache = async () => {
        setClearing(true);
        setError(null);
        setClearSuccess(null);

        try {
            const response = await clearMCPToolsCache();
            setClearSuccess(response.message);

            // Automatically refresh tools after clearing cache
            await fetchTools({showLoading: true});
        } catch (err) {
            setError(err instanceof Error ? err.message : 'Failed to clear cache');
        } finally {
            setClearing(false);
        }
    };

    useEffect(() => {
        if (!initialToolsData) {
            fetchTools({showLoading: true});
        }
    }, [initialToolsData, fetchTools]);

    useMCPConnectionEvents(
        useCallback(() => {
            fetchTools();
        }, [fetchTools]),
    );

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

    // Find the matching ServerConfig for a discovered server
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
                tool_configs: server.toolConfigs ?? [],
            };
        }

        return mcpConfig.servers.find((sc) =>
            sc.name === server.name || sc.baseURL === server.url,
        ) || null;
    };

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

        if (serverInfo.serverType === 'plugin') {
            const pluginID = pluginIDFromServerOrigin(serverInfo.url);
            if (!pluginID) {
                return;
            }

            const prev = findServerConfig(serverInfo);
            const update: {enabled?: boolean; tool_configs?: MCPToolConfig[]} = {};
            if (!prev || prev.enabled !== updatedServerConfig.enabled) {
                update.enabled = updatedServerConfig.enabled;
            }
            const prevConfigs = prev?.tool_configs ?? [];
            const nextConfigs = updatedServerConfig.tool_configs ?? [];
            if (JSON.stringify(prevConfigs) !== JSON.stringify(nextConfigs)) {
                update.tool_configs = nextConfigs;
            }

            if (Object.keys(update).length === 0) {
                return;
            }

            updatePluginServer(pluginID, update).
                then(() => fetchTools({propagateError: true})).
                catch((err) => {
                    setError(
                        err instanceof Error ?
                            err.message :
                            intl.formatMessage({id: 'mcp_tools.update_plugin_server_failed', defaultMessage: 'Failed to update plugin server'}),
                    );
                });
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
                        onClick={() => fetchTools({showLoading: true})}
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
