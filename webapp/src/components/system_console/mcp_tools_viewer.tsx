// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useState} from 'react';
import styled from 'styled-components';
import {RefreshIcon, ChevronDownIcon, ChevronRightIcon, ExclamationThickIcon} from '@mattermost/compass-icons/components';
import {FormattedMessage} from 'react-intl';

import {TertiaryButton} from '../assets/buttons';
import {getMCPTools} from '../../client';

// Type definitions matching the backend API response
export type MCPToolInfo = {
    name: string;
    description: string;
    inputSchema: {[key: string]: any} | null;
};

export type MCPServerInfo = {
    id: string;
    name: string;
    url: string;
    tools: MCPToolInfo[];
    needsOAuth: boolean;
    oauthURL?: string;
    error: string | null;
};

export type MCPToolsResponse = {
    servers: MCPServerInfo[];
};

// Component for displaying a single tool
const ToolItem = ({tool}: {tool: MCPToolInfo}) => (
    <ToolContainer>
        <ToolName>{tool.name}</ToolName>
        <ToolDescription>{tool.description}</ToolDescription>
    </ToolContainer>
);

// Component for displaying a single server and its tools
const ServerItem = ({server}: {server: MCPServerInfo}) => {
    const [isExpanded, setIsExpanded] = useState(true);

    const toggleExpanded = () => {
        setIsExpanded(!isExpanded);
    };

    return (
        <ServerContainer>
            <ServerHeader onClick={toggleExpanded}>
                <ServerHeaderLeft>
                    <ExpandIcon>
                        {isExpanded ? <ChevronDownIcon size={16}/> : <ChevronRightIcon size={16}/>}
                    </ExpandIcon>
                    <ServerInfo>
                        <ServerName>{server.name}</ServerName>
                        <ServerUrl>{server.url}</ServerUrl>
                    </ServerInfo>
                </ServerHeaderLeft>
                <ServerStats>
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
                                defaultMessage='Total: {count} tools'
                                values={{count: server.tools.length}}
                            />
                        </ToolCount>
                    )}
                </ServerStats>
            </ServerHeader>

            {isExpanded && (
                <ServerContent>
                    {server.error && (
                        <ErrorMessage>
                            <ExclamationThickIcon size={20}/>
                            <div>
                                <ErrorTitle>
                                    <FormattedMessage defaultMessage='Connection Error'/>
                                </ErrorTitle>
                                <ErrorDescription>{server.error}</ErrorDescription>
                            </div>
                        </ErrorMessage>
                    )}
                    {!server.error && server.needsOAuth && server.oauthURL && (
                        <OAuthMessage>
                            <div>
                                <OAuthTitle>
                                    <FormattedMessage defaultMessage='OAuth Required'/>
                                </OAuthTitle>
                                <OAuthDescription>
                                    <FormattedMessage defaultMessage='This server requires OAuth authentication to access its tools.'/>
                                </OAuthDescription>
                            </div>
                            <OAuthButton
                                onClick={() => window.open(server.oauthURL, '_blank')}
                            >
                                <FormattedMessage defaultMessage='Connect Account'/>
                            </OAuthButton>
                        </OAuthMessage>
                    )}
                    {!server.error && !server.needsOAuth && server.tools.length === 0 && (
                        <EmptyTools>
                            <FormattedMessage defaultMessage='No tools available from this server'/>
                        </EmptyTools>
                    )}
                    {!server.error && !server.needsOAuth && server.tools.length > 0 && (
                        <ToolsList>
                            {server.tools.map((tool) => (
                                <ToolItem
                                    key={tool.name}
                                    tool={tool}
                                />
                            ))}
                        </ToolsList>
                    )}
                </ServerContent>
            )}
        </ServerContainer>
    );
};

// Main component for MCP Tools viewer
const MCPToolsViewer = () => {
    const [toolsData, setToolsData] = useState<MCPToolsResponse | null>(null);
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState<string | null>(null);

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

    // Fetch tools on component mount
    useEffect(() => {
        fetchTools();
    }, []);

    // Calculate total tools across all servers
    const totalTools = toolsData?.servers.reduce((sum, server) => sum + server.tools.length, 0) || 0;
    const serversWithErrors = toolsData?.servers.filter((server) => server.error).length || 0;

    return (
        <Container>
            <Header>
                <HeaderInfo>
                    <Title>
                        <FormattedMessage defaultMessage='Available MCP Tools'/>
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
                <RefreshButton
                    onClick={fetchTools}
                    disabled={loading}
                >
                    <RefreshIcon
                        size={16}
                    />
                    <FormattedMessage defaultMessage='Refresh Tools'/>
                </RefreshButton>
            </Header>

            <Content>
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
                            <ServerItem
                                key={server.id}
                                server={server}
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

const ServerContainer = styled.div`
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    border-radius: 4px;
    background-color: var(--center-channel-bg);
    overflow: hidden;
`;

const ServerHeader = styled.div`
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 16px;
    cursor: pointer;
    background-color: rgba(var(--center-channel-color-rgb), 0.02);
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.08);

    &:hover {
        background-color: rgba(var(--center-channel-color-rgb), 0.04);
    }
`;

const ServerHeaderLeft = styled.div`
    display: flex;
    align-items: center;
    gap: 12px;
    flex: 1;
`;

const ExpandIcon = styled.div`
    display: flex;
    align-items: center;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const ServerInfo = styled.div`
    display: flex;
    flex-direction: column;
    gap: 2px;
`;

const ServerName = styled.div`
    font-weight: 600;
    font-size: 16px;
    color: var(--center-channel-color);
`;

const ServerUrl = styled.div`
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    font-family: monospace;
`;

const ServerStats = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
`;

const ToolCount = styled.div`
    font-size: 12px;
    font-weight: 600;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    padding: 4px 8px;
    background-color: rgba(var(--center-channel-color-rgb), 0.08);
    border-radius: 4px;
`;

const ErrorIndicator = styled.div`
    display: flex;
    align-items: center;
    gap: 4px;
    font-size: 12px;
    font-weight: 600;
    color: var(--error-text);
    padding: 4px 8px;
    background-color: rgba(var(--error-text-color-rgb), 0.08);
    border-radius: 4px;
`;

const OAuthIndicator = styled.div`
    display: flex;
    align-items: center;
    gap: 4px;
    font-size: 12px;
    font-weight: 600;
    color: var(--button-bg);
    padding: 4px 8px;
    background-color: rgba(var(--button-bg-rgb), 0.08);
    border-radius: 4px;
`;

const ServerContent = styled.div`
    padding: 16px;
`;

const ErrorMessage = styled.div`
    display: flex;
    align-items: flex-start;
    gap: 12px;
    padding: 16px;
    color: var(--error-text);
    background-color: rgba(var(--error-text-color-rgb), 0.04);
    border-radius: 4px;
`;

const ErrorTitle = styled.div`
    font-weight: 600;
    margin-bottom: 4px;
`;

const ErrorDescription = styled.div`
    font-size: 12px;
    opacity: 0.8;
`;

const OAuthMessage = styled.div`
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    padding: 16px;
    color: var(--center-channel-color);
    background-color: rgba(var(--button-bg-rgb), 0.04);
    border: 1px solid rgba(var(--button-bg-rgb), 0.16);
    border-radius: 4px;
`;

const OAuthTitle = styled.div`
    font-weight: 600;
    margin-bottom: 4px;
    color: var(--button-bg);
`;

const OAuthDescription = styled.div`
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const OAuthButton = styled(TertiaryButton)`
    white-space: nowrap;
    background-color: var(--button-bg);
    color: var(--button-color);
    border: 1px solid var(--button-bg);

    &:hover {
        background-color: rgba(var(--button-bg-rgb), 0.88);
    }
`;

const EmptyTools = styled.div`
    text-align: center;
    padding: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    background-color: rgba(var(--center-channel-color-rgb), 0.04);
    border-radius: 4px;
`;

const ToolsList = styled.div`
    display: flex;
    flex-direction: column;
    gap: 8px;
`;

const ToolContainer = styled.div`
    padding: 12px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    border-radius: 4px;
    background-color: rgba(var(--center-channel-color-rgb), 0.02);
`;

const ToolName = styled.div`
    font-weight: 600;
    font-size: 14px;
    color: var(--center-channel-color);
    margin-bottom: 4px;
    font-family: monospace;
`;

const ToolDescription = styled.div`
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    line-height: 1.4;
`;

export default MCPToolsViewer;
