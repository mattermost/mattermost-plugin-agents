// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import styled from 'styled-components';
import {PlusIcon, TrashCanOutlineIcon} from '@mattermost/compass-icons/components';
import {FormattedMessage, useIntl} from 'react-intl';

import {TertiaryButton} from '../assets/buttons';

import MCPToolsViewer from './mcp_tools_viewer';

import {BooleanItem, ItemList, TextItem} from './item';

export type MCPServerConfig = {
    name: string;
    enabled: boolean;
    baseURL: string;
    headers: {[key: string]: string};
};

export type MCPEmbeddedServerConfig = {
    enabled: boolean;
};

export type MCPConfig = {
    enabled: boolean;
    servers: MCPServerConfig[];
    embeddedServer: MCPEmbeddedServerConfig;
    idleTimeoutMinutes?: number;
};

type Props = {
    mcpConfig: MCPConfig;
    onChange: (config: MCPConfig) => void;
};

// Default configuration for a new MCP server
const defaultServerConfig: MCPServerConfig = {
    name: '',
    enabled: true,
    baseURL: '',
    headers: {},
};

// Component for a single MCP server configuration
const MCPServer = ({
    serverIndex,
    serverConfig,
    onChange,
    onDelete,
}: {
    serverIndex: number;
    serverConfig: MCPServerConfig;
    onChange: (serverIndex: number, config: MCPServerConfig) => void;
    onDelete: () => void;
}) => {
    const intl = useIntl();
    const [isEditingName, setIsEditingName] = useState(false);
    const [serverName, setServerName] = useState(serverConfig.name);

    // Ensure server config has all required properties
    const config = {
        name: serverConfig.name || '',
        enabled: serverConfig.enabled ?? false,
        baseURL: serverConfig.baseURL || '',
        headers: serverConfig.headers || {},
    };

    // Update server URL
    const updateServerURL = (baseURL: string) => {
        onChange(serverIndex, {
            ...config,
            baseURL,
        });
    };

    // Update server enabled state
    const updateServerEnabled = (enabled: boolean) => {
        onChange(serverIndex, {
            ...config,
            enabled,
        });
    };

    // Update server name
    const updateServerName = (name: string) => {
        onChange(serverIndex, {
            ...config,
            name,
        });
    };

    // Add a new header
    const addHeader = () => {
        const headers = config.headers || {};
        onChange(serverIndex, {
            ...config,
            headers: {
                ...headers,
                '': '',
            },
        });
    };

    // Update a header's key or value
    const updateHeader = (oldKey: string, newKey: string, value: string) => {
        const headers = {...(config.headers || {})};

        // If the key has changed, remove the old one
        if (oldKey !== newKey) {
            delete headers[oldKey];
        }

        // Set the new key-value pair
        headers[newKey] = value;

        onChange(serverIndex, {
            ...config,
            headers,
        });
    };

    // Remove a header
    const removeHeader = (key: string) => {
        const headers = {...(config.headers || {})};
        delete headers[key];

        onChange(serverIndex, {
            ...config,
            headers,
        });
    };

    // Handle renaming the server
    const handleRename = () => {
        const newName = serverName.trim();

        if (newName && newName !== config.name) {
            updateServerName(newName);
        }

        setIsEditingName(false);
    };

    // Handle keyboard events for the name input
    const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
        if (e.key === 'Enter') {
            handleRename();
        } else if (e.key === 'Escape') {
            setServerName(config.name);
            setIsEditingName(false);
        }
    };

    return (
        <ServerContainer>
            <ServerHeader>
                {isEditingName ? (
                    <ServerNameEditContainer>
                        <ServerNameInput
                            value={serverName}
                            onChange={(e) => setServerName(e.target.value)}
                            onBlur={handleRename}
                            onKeyDown={handleKeyDown}
                            autoFocus={true}
                            placeholder={intl.formatMessage({defaultMessage: 'Server name'})}
                        />
                    </ServerNameEditContainer>
                ) : (
                    <ServerTitle onClick={() => setIsEditingName(true)}>
                        {config.name || `Server ${serverIndex + 1}`}
                    </ServerTitle>
                )}
                <DeleteButton onClick={onDelete}>
                    <TrashCanOutlineIcon size={16}/>
                    <FormattedMessage defaultMessage='Delete Server'/>
                </DeleteButton>
            </ServerHeader>

            <BooleanItem
                label={intl.formatMessage({defaultMessage: 'Enable Server'})}
                value={config.enabled}
                onChange={updateServerEnabled}
                helpText={intl.formatMessage({defaultMessage: 'Enable or disable this MCP server.'})}
            />

            <TextItem
                label={intl.formatMessage({defaultMessage: 'Server URL'})}
                placeholder='https://mcp.example.com'
                value={config.baseURL}
                onChange={(e) => updateServerURL(e.target.value)}
                helptext={intl.formatMessage({defaultMessage: 'The base URL of the MCP server.'})}
            />

            <HeadersSection>
                <HeadersSectionTitle>
                    {intl.formatMessage({defaultMessage: 'Headers'})}
                </HeadersSectionTitle>

                <HeadersList>
                    {Object.entries(config.headers || {}).map(([key, value], index) => (
                        <HeaderRow key={index}>
                            <HeaderInput
                                placeholder={intl.formatMessage({defaultMessage: 'Header name'})}
                                value={key}
                                onChange={(e) => updateHeader(key, e.target.value, value)}
                            />
                            <HeaderInput
                                placeholder={intl.formatMessage({defaultMessage: 'Value'})}
                                value={value}
                                onChange={(e) => updateHeader(key, key, e.target.value)}
                            />
                            <RemoveHeaderButton
                                onClick={() => removeHeader(key)}
                            >
                                <TrashCanOutlineIcon size={14}/>
                            </RemoveHeaderButton>
                        </HeaderRow>
                    ))}
                </HeadersList>

                <AddHeaderButton
                    onClick={addHeader}
                >
                    <PlusIcon size={14}/>
                    <FormattedMessage defaultMessage='Add Header'/>
                </AddHeaderButton>
            </HeadersSection>
        </ServerContainer>
    );
};

// Main component for MCP servers configuration
const MCPServers = ({mcpConfig, onChange}: Props) => {
    const intl = useIntl();
    const [activeTab, setActiveTab] = useState<'config' | 'tools'>('config');

    // Create a properly initialized config object
    const config: MCPConfig = {
        enabled: mcpConfig?.enabled || false,
        servers: Array.isArray(mcpConfig?.servers) ? mcpConfig.servers : [],
        embeddedServer: mcpConfig?.embeddedServer || {
            enabled: !mcpConfig?.enabled,
        },
        idleTimeoutMinutes: mcpConfig?.idleTimeoutMinutes || 30,
    };

    // Generate a server name
    const generateServerName = () => {
        const prefix = 'MCP Server ';
        let counter = config.servers.length + 1;

        // Make sure the name is unique
        const isNameTaken = (name: string) => config.servers.some((server) => server.name === name);

        while (isNameTaken(`${prefix}${counter}`)) {
            counter++;
        }

        return `${prefix}${counter}`;
    };

    // Add a new server
    const addServer = () => {
        // Use the auto-generated name
        const serverName = generateServerName();

        onChange({
            ...config,
            servers: [
                ...config.servers,
                {
                    ...defaultServerConfig,
                    name: serverName,
                },
            ],
        });
    };

    // Update a server's configuration
    const updateServer = (serverIndex: number, serverConfig: MCPServerConfig) => {
        const updatedServers = [...config.servers];
        updatedServers[serverIndex] = serverConfig;

        onChange({
            ...config,
            servers: updatedServers,
        });
    };

    // Delete a server
    const deleteServer = (serverIndex: number) => {
        const newServers = config.servers.filter((_, index) => index !== serverIndex);

        onChange({
            ...config,
            servers: newServers,
        });
    };

    return (
        <div>
            {config.enabled && (
                <>
                    <TabsContainer>
                        <TabButton
                            active={activeTab === 'config'}
                            onClick={() => setActiveTab('config')}
                        >
                            <FormattedMessage defaultMessage='Configuration'/>
                        </TabButton>
                        <TabButton
                            active={activeTab === 'tools'}
                            onClick={() => setActiveTab('tools')}
                        >
                            <FormattedMessage defaultMessage='Tools'/>
                        </TabButton>
                    </TabsContainer>

                    <TabContent>
                        {activeTab === 'config' && (
                            <>
                                <ItemList title={intl.formatMessage({defaultMessage: 'MCP Configuration'})}>
                                    <BooleanItem
                                        label={intl.formatMessage({defaultMessage: 'Enable MCP'})}
                                        value={config.enabled}
                                        onChange={(enabled) => onChange({...config, enabled})}
                                        helpText={intl.formatMessage({defaultMessage: 'Enable the Model Context Protocol (MCP) integration to access tools from MCP servers.'})}
                                    />
                                    <TextItem
                                        label={intl.formatMessage({defaultMessage: 'Connection Idle Timeout (minutes)'})}
                                        value={config.idleTimeoutMinutes?.toString() || '30'}
                                        type='number'
                                        onChange={(e) => {
                                            const idleTimeoutMinutes = parseInt(e.target.value, 10);
                                            onChange({
                                                ...config,
                                                idleTimeoutMinutes: isNaN(idleTimeoutMinutes) ? 30 : Math.max(1, idleTimeoutMinutes),
                                            });
                                        }}
                                        helptext={intl.formatMessage({defaultMessage: 'How long to keep an inactive user connection open before closing it automatically. Lower values save resources, higher values improve response times.'})}
                                    />
                                    <BooleanItem
                                        label={intl.formatMessage({defaultMessage: 'Enable Embedded Server'})}
                                        value={config.embeddedServer.enabled}
                                        onChange={(enabled) => onChange({
                                            ...config,
                                            embeddedServer: {
                                                ...config.embeddedServer,
                                                enabled,
                                            },
                                        })}
                                        helpText={intl.formatMessage({defaultMessage: 'Enable the built-in Mattermost MCP server that provides AI tools for reading/creating channels, posts, searching content, and managing users and teams. Tools operate with the permissions of the user who invokes them.'})}
                                    />
                                </ItemList>
                                <ServersList>
                                    {!Array.isArray(config.servers) || config.servers.length < 1 ? (
                                        <EmptyState>
                                            <FormattedMessage defaultMessage='No remote MCP servers configured. Add a server to connect to external MCP tools.'/>
                                        </EmptyState>
                                    ) : (
                                        config.servers.map((serverConfig, index) => (
                                            <MCPServer
                                                key={index}
                                                serverIndex={index}
                                                serverConfig={serverConfig}
                                                onChange={updateServer}
                                                onDelete={() => deleteServer(index)}
                                            />
                                        ))
                                    )}
                                </ServersList>

                                <AddServerContainer>
                                    <TertiaryButton
                                        onClick={addServer}
                                    >
                                        <PlusServerIcon/>
                                        <FormattedMessage defaultMessage='Add Remote MCP Server'/>
                                    </TertiaryButton>
                                </AddServerContainer>
                            </>
                        )}

                        {activeTab === 'tools' && (
                            <MCPToolsViewer/>
                        )}
                    </TabContent>
                </>
            )}

            {!config.enabled && (
                <ItemList title={intl.formatMessage({defaultMessage: 'MCP Configuration'})}>
                    <BooleanItem
                        label={intl.formatMessage({defaultMessage: 'Enable MCP'})}
                        value={config.enabled}
                        onChange={(enabled) => onChange({...config, enabled})}
                        helpText={intl.formatMessage({defaultMessage: 'Enable the Model Context Protocol (MCP) integration to access tools from MCP servers.'})}
                    />
                </ItemList>
            )}
        </div>
    );
};

// Styled components
const ServersList = styled.div`
    display: flex;
    flex-direction: column;
    gap: 16px;
    margin-top: 16px;
    margin-bottom: 16px;
`;

const ServerContainer = styled.div`
    display: flex;
    flex-direction: column;
    gap: 16px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    border-radius: 4px;
    padding: 16px;
    background-color: var(--center-channel-bg);
`;

const ServerHeader = styled.div`
    display: flex;
    justify-content: space-between;
    align-items: center;
`;

const ServerTitle = styled.div`
    font-weight: 600;
    font-size: 16px;
    color: var(--center-channel-color);
    cursor: pointer;
    padding: 4px 8px;
    border-radius: 4px;

    &:hover {
        background-color: rgba(var(--center-channel-color-rgb), 0.08);
    }

    &::after {
        content: '✎';
        font-size: 12px;
        margin-left: 8px;
        opacity: 0;
        transition: opacity 0.2s ease;
    }

    &:hover::after {
        opacity: 0.7;
    }
`;

const DeleteButton = styled.button`
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 8px 12px;
    background: none;
    border: none;
    border-radius: 4px;
    color: var(--error-text);
    cursor: pointer;
    font-size: 12px;
    font-weight: 600;

    &:hover {
        background: rgba(var(--error-text-color-rgb), 0.08);
    }
`;

const HeadersSection = styled.div`
    display: flex;
    flex-direction: column;
    gap: 12px;
`;

const HeadersSectionTitle = styled.div`
    font-weight: 600;
    font-size: 14px;
    color: var(--center-channel-color);
    margin-bottom: 4px;
`;

const HeadersList = styled.div`
    display: flex;
    flex-direction: column;
    gap: 8px;
`;

const HeaderRow = styled.div`
    display: flex;
    gap: 8px;
    align-items: center;
`;

const HeaderInput = styled.input`
    flex: 1;
    padding: 8px 12px;
    border-radius: 4px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    background: var(--center-channel-bg);
    font-size: 14px;

    &:focus {
        border-color: var(--button-bg);
        outline: none;
    }
`;

const RemoveHeaderButton = styled.button`
    display: flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    background: none;
    border: none;
    border-radius: 4px;
    color: var(--error-text);
    cursor: pointer;

    &:hover {
        background: rgba(var(--error-text-color-rgb), 0.08);
    }
`;

const AddHeaderButton = styled.button`
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 6px 12px;
    background: none;
    border: none;
    border-radius: 4px;
    color: var(--button-bg);
    cursor: pointer;
    font-size: 12px;
    font-weight: 600;
    align-self: flex-start;

    &:hover {
        background: rgba(var(--button-bg-rgb), 0.08);
    }
`;

const AddServerContainer = styled.div`
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: 12px;
    margin-bottom: 16px;
    margin-top: 8px;
`;

const PlusServerIcon = styled(PlusIcon)`
    width: 18px;
    height: 18px;
    margin-right: 8px;
`;

const EmptyState = styled.div`
    padding: 24px;
    text-align: center;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    background-color: rgba(var(--center-channel-color-rgb), 0.04);
    border-radius: 4px;
`;

const ServerNameInput = styled.input`
    flex: 1;
    padding: 8px 12px;
    border-radius: 4px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    background: var(--center-channel-bg);
    font-size: 14px;
    min-width: 200px;
    max-width: 300px;

    &:focus {
        border-color: var(--button-bg);
        outline: none;
    }
`;

const ServerNameEditContainer = styled.div`
    display: flex;
    align-items: center;
    width: 100%;
    max-width: 300px;
`;

const TabsContainer = styled.div`
    display: flex;
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    margin-bottom: 24px;
`;

const TabButton = styled.button<{active: boolean}>`
    padding: 12px 16px;
    border: none;
    background: none;
    cursor: pointer;
    font-size: 14px;
    font-weight: 600;
    color: ${(props) => (props.active ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.64)')};
    border-bottom: 2px solid ${(props) => (props.active ? 'var(--button-bg)' : 'transparent')};
    transition: color 0.2s ease, border-color 0.2s ease;

    &:hover {
        color: ${(props) => (props.active ? 'var(--button-bg)' : 'var(--center-channel-color)')};
    }

    &:first-child {
        padding-left: 0;
    }
`;

const TabContent = styled.div`
    /* Tab content styling */
`;

export default MCPServers;
