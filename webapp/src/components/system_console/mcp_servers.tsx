// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import styled from 'styled-components';
import {PlusIcon, TrashCanOutlineIcon} from '@mattermost/compass-icons/components';
import {FormattedMessage, useIntl} from 'react-intl';

import {TertiaryButton} from '../assets/buttons';

import {BooleanItem, ItemList, TextItem} from './item';

export type MCPServerConfig = {
    baseURL: string;
    headers: {[key: string]: string};
};

export type MCPConfig = {
    enabled: boolean;
    servers: {[key: string]: MCPServerConfig};
    idleTimeout?: number;
};

type Props = {
    mcpConfig: MCPConfig;
    onChange: (config: MCPConfig) => void;
};

// Default configuration for a new MCP server
const defaultServerConfig: MCPServerConfig = {
    baseURL: '',
    headers: {},
};

// Component for a single MCP server configuration
const MCPServer = ({
    serverID,
    serverConfig,
    onChange,
    onDelete,
    onRename,
}: {
    serverID: string;
    serverConfig: MCPServerConfig;
    onChange: (serverID: string, config: MCPServerConfig) => void;
    onDelete: () => void;
    onRename: (oldID: string, newID: string, config: MCPServerConfig) => void;
}) => {
    const intl = useIntl();
    const [isEditingName, setIsEditingName] = useState(false);
    const [serverName, setServerName] = useState(serverID);

    // Ensure server config has all required properties
    const config = {
        ...defaultServerConfig,
        ...serverConfig,
    };

    // Update server URL
    const updateServerURL = (baseURL: string) => {
        onChange(serverID, {
            ...config,
            baseURL,
        });
    };

    // Add a new header
    const addHeader = () => {
        const headers = config.headers || {};
        onChange(serverID, {
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
        if (oldKey !== newKey && oldKey !== '') {
            delete headers[oldKey];
        }

        // Set the new key-value pair
        headers[newKey] = value;

        onChange(serverID, {
            ...config,
            headers,
        });
    };

    // Remove a header
    const removeHeader = (key: string) => {
        const headers = {...(config.headers || {})};
        delete headers[key];

        onChange(serverID, {
            ...config,
            headers,
        });
    };

    // Handle renaming the server
    const handleRename = () => {
        const newName = serverName.trim();

        if (newName && newName !== serverID) {
            onRename(serverID, newName, config);
        }

        setIsEditingName(false);
    };

    // Handle keyboard events for the name input
    const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
        if (e.key === 'Enter') {
            handleRename();
        } else if (e.key === 'Escape') {
            setServerName(serverID);
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
                        {serverID}
                    </ServerTitle>
                )}
                <DeleteButton onClick={onDelete}>
                    <TrashCanOutlineIcon size={16}/>
                    <FormattedMessage defaultMessage='Delete Server'/>
                </DeleteButton>
            </ServerHeader>

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
                    {Object.entries(config.headers || {}).map(([key, value]) => (
                        <HeaderRow key={key}>
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

    // Ensure servers object is initialized
    if (!mcpConfig.servers) {
        mcpConfig.servers = {};
    }

    // Generate a server name
    const generateServerName = () => {
        const prefix = 'mcp-server-';
        let counter = Object.keys(mcpConfig.servers).length + 1;
        let serverID = `${prefix}${counter}`;

        // Make sure the ID is unique
        while (mcpConfig.servers[serverID]) {
            counter++;
            serverID = `${prefix}${counter}`;
        }

        return serverID;
    };

    // Add a new server
    const addServer = () => {
        // Use the auto-generated name
        const serverID = generateServerName();

        onChange({
            ...mcpConfig,
            servers: {
                ...mcpConfig.servers,
                [serverID]: {...defaultServerConfig},
            },
        });
    };

    // Update a server's name
    const renameServer = (oldID: string, originalNewID: string, config: MCPServerConfig) => {
        // Skip if the ID hasn't changed
        if (oldID === originalNewID) {
            return;
        }

        // Make the ID safe for use (remove spaces, special chars)
        const newID = originalNewID.toLowerCase().replace(/[^a-z0-9-_]/g, '-');

        // Skip if the new ID is empty or already exists
        if (!newID || (newID !== oldID && mcpConfig.servers[newID])) {
            return;
        }

        // Create a copy of the servers object with the renamed server
        const updatedServers = {...mcpConfig.servers};
        delete updatedServers[oldID];
        updatedServers[newID] = config;

        onChange({
            ...mcpConfig,
            servers: updatedServers,
        });
    };

    // Update a server's configuration
    const updateServer = (serverID: string, serverConfig: MCPServerConfig) => {
        onChange({
            ...mcpConfig,
            servers: {
                ...mcpConfig.servers,
                [serverID]: serverConfig,
            },
        });
    };

    // Delete a server
    const deleteServer = (serverID: string) => {
        const newServers = {...mcpConfig.servers};
        delete newServers[serverID];

        onChange({
            ...mcpConfig,
            servers: newServers,
        });
    };

    const serverCount = Object.keys(mcpConfig.servers).length;

    // Let's hide the configuration from the system console until the MCP implmentation is more mature
    if (!mcpConfig.enabled) {
        return null;
    }

    return (
        <div>
            <ItemList title={intl.formatMessage({defaultMessage: 'MCP Configuration'})}>
                <BooleanItem
                    label={intl.formatMessage({defaultMessage: 'Enable MCP'})}
                    value={mcpConfig.enabled}
                    onChange={(enabled) => onChange({...mcpConfig, enabled})}
                    helpText={intl.formatMessage({defaultMessage: 'Enable the Model Context Protocol (MCP) integration to access tools from MCP servers.'})}
                />
                {mcpConfig.enabled && (
                    <TextItem
                        label={intl.formatMessage({defaultMessage: 'Connection Idle Timeout (minutes)'})}
                        value={mcpConfig.idleTimeout?.toString() || '30'}
                        type='number'
                        onChange={(e) => {
                            const idleTimeout = parseInt(e.target.value, 10);
                            onChange({
                                ...mcpConfig,
                                idleTimeout: isNaN(idleTimeout) ? 30 : Math.max(1, idleTimeout),
                            });
                        }}
                        helptext={intl.formatMessage({defaultMessage: 'How long to keep an inactive user connection open before closing it automatically. Lower values save resources, higher values improve response times.'})}
                    />
                )}
            </ItemList>

            {mcpConfig.enabled && (
                <>
                    <ServersList>
                        {serverCount === 0 ? (
                            <EmptyState>
                                <FormattedMessage defaultMessage='No MCP servers configured. Add a server to enable MCP tools.'/>
                            </EmptyState>
                        ) : (
                            Object.entries(mcpConfig.servers).map(([serverID, serverConfig]) => (
                                <MCPServer
                                    key={serverID}
                                    serverID={serverID}
                                    serverConfig={serverConfig}
                                    onChange={updateServer}
                                    onDelete={() => deleteServer(serverID)}
                                    onRename={renameServer}
                                />
                            ))
                        )}
                    </ServersList>

                    <AddServerContainer>
                        <TertiaryButton
                            onClick={addServer}
                        >
                            <PlusServerIcon/>
                            <FormattedMessage defaultMessage='Add MCP Server'/>
                        </TertiaryButton>
                    </AddServerContainer>
                </>
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

export default MCPServers;
