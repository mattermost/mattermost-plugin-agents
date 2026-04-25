// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import styled from 'styled-components';
import {ChevronDownIcon, ExclamationThickIcon} from '@mattermost/compass-icons/components';
import {FormattedMessage, useIntl} from 'react-intl';

import {TertiaryButton} from '../assets/buttons';
import {ToggleSwitch} from '../toggle_switch';
import {pluginIDFromServerOrigin, stripPluginPrefix} from '../../utils/tool_names';

import {MCPServerConfig, MCPToolConfig} from './mcp_servers';
import {MCPServerInfo} from './mcp_tools_viewer';
import MCPToolConfigRow from './mcp_tool_config_row';

type MCPServerToolRowProps = {
    server: MCPServerInfo;
    serverConfig: MCPServerConfig | null;
    onServerConfigChange: (config: MCPServerConfig) => void;
};

const MCPServerToolRow = ({server, serverConfig, onServerConfigChange}: MCPServerToolRowProps) => {
    const [expanded, setExpanded] = useState(false);
    const intl = useIntl();

    const getToolConfig = (toolName: string): MCPToolConfig => {
        const existing = serverConfig?.tool_configs?.find((tc) => tc.name === toolName);
        if (existing) {
            return existing;
        }

        // Default for unconfigured tools: enabled, ask policy
        return {name: toolName, policy: 'ask', enabled: true};
    };

    const enabledToolCount = server.tools.filter((tool) => {
        const tc = getToolConfig(tool.name);
        return tc.enabled;
    }).length;

    const serverEnabled = serverConfig?.enabled ?? false;

    const handleServerToggle = (enabled: boolean) => {
        if (serverConfig) {
            onServerConfigChange({...serverConfig, enabled});
        }
    };

    const handleToolConfigChange = (toolName: string, updatedToolConfig: MCPToolConfig) => {
        if (!serverConfig) {
            return;
        }
        const existingConfigs = serverConfig.tool_configs || [];
        const existingIndex = existingConfigs.findIndex((tc) => tc.name === toolName);
        let updatedConfigs: MCPToolConfig[];
        if (existingIndex >= 0) {
            updatedConfigs = [...existingConfigs];
            updatedConfigs[existingIndex] = updatedToolConfig;
        } else {
            updatedConfigs = [...existingConfigs, updatedToolConfig];
        }
        onServerConfigChange({...serverConfig, tool_configs: updatedConfigs});
    };

    return (
        <ServerRowContainer>
            <ServerRowHeader>
                <ServerRowExpandButton
                    type='button'
                    onClick={() => setExpanded(!expanded)}
                    aria-expanded={expanded}
                >
                    <ServerAvatar>
                        {server.name.charAt(0).toUpperCase()}
                    </ServerAvatar>
                    <ServerInfo>
                        <ServerName>{server.name}</ServerName>
                        <ServerMeta>
                            {server.serverType === 'plugin' && (
                                <PluginBadge>
                                    <FormattedMessage defaultMessage='Plugin'/>
                                </PluginBadge>
                            )}
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
                                        defaultMessage='{enabled}/{total} tools enabled'
                                        values={{enabled: enabledToolCount, total: server.tools.length}}
                                    />
                                </ToolCount>
                            )}
                        </ServerMeta>
                    </ServerInfo>
                    <ExpandChevron>
                        <StyledChevron $expanded={expanded}>
                            <ChevronDownIcon size={16}/>
                        </StyledChevron>
                    </ExpandChevron>
                </ServerRowExpandButton>
                {serverConfig && (
                    <ToggleWrapper>
                        <ToggleSwitch
                            checked={serverEnabled}
                            onChange={handleServerToggle}
                            size='medium'
                            ariaLabel={intl.formatMessage(
                                {defaultMessage: 'Enable {serverName}'},
                                {serverName: server.name},
                            )}
                        />
                    </ToggleWrapper>
                )}
            </ServerRowHeader>

            {expanded && (
                <ToolsContainer>
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
                                    <FormattedMessage defaultMessage="You must authenticate to fetch this server's tool list and configure per-tool approval policies. This only connects your account — each user must authenticate separately."/>
                                </OAuthDescription>
                            </div>
                            <OAuthButton
                                onClick={() => window.open(server.oauthURL, '_blank', 'noopener,noreferrer')}
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
                        server.tools.map((tool) => {
                            const isPlugin = server.serverType === 'plugin';
                            const pluginID = isPlugin ? pluginIDFromServerOrigin(server.url) : '';
                            const displayName = isPlugin ? stripPluginPrefix(tool.name, pluginID) : tool.name;

                            return (
                                <MCPToolConfigRow
                                    key={tool.name}
                                    tool={tool}
                                    toolConfig={getToolConfig(tool.name)}
                                    onToolConfigChange={(updatedConfig) =>
                                        handleToolConfigChange(tool.name, updatedConfig)
                                    }
                                    serverDisabled={!serverEnabled}
                                    displayName={displayName}
                                />
                            );
                        })
                    )}
                </ToolsContainer>
            )}
        </ServerRowContainer>
    );
};

// Styled components
const ServerRowContainer = styled.div`
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    border-radius: 4px;
    background-color: var(--center-channel-bg);
    overflow: hidden;
`;

const ServerRowHeader = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
    padding-right: 16px;
`;

const ServerRowExpandButton = styled.button`
    display: flex;
    align-items: center;
    flex: 1;
    min-width: 0;
    padding: 12px 8px 12px 16px;
    cursor: pointer;
    gap: 8px;
    border: none;
    background: none;
    text-align: left;
    font: inherit;
    color: inherit;

    &:hover {
        background-color: rgba(var(--center-channel-color-rgb), 0.04);
    }
`;

const ServerAvatar = styled.div`
    width: 40px;
    height: 40px;
    border-radius: 50%;
    background: rgba(var(--center-channel-color-rgb), 0.08);
    display: flex;
    align-items: center;
    justify-content: center;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    font-size: 14px;
    font-weight: 600;
    flex-shrink: 0;
`;

const ServerInfo = styled.div`
    display: flex;
    flex-direction: column;
    flex: 1;
    min-width: 0;
    gap: 2px;
`;

const ServerName = styled.div`
    font-family: 'Open Sans', sans-serif;
    font-weight: 600;
    font-size: 14px;
    line-height: 20px;
    color: var(--center-channel-color);
`;

const ServerMeta = styled.div`
    display: flex;
    align-items: center;
`;

const ToolCount = styled.span`
    font-family: 'Open Sans', sans-serif;
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.75);
`;

const ToggleWrapper = styled.div`
    display: flex;
    align-items: center;
    flex-shrink: 0;
`;

const ExpandChevron = styled.div`
    display: flex;
    align-items: center;
    justify-content: center;
    margin-left: auto;
    padding: 8px;
    flex-shrink: 0;
`;

const StyledChevron = styled.div<{$expanded: boolean}>`
    display: flex;
    align-items: center;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    transform: ${(props) => (props.$expanded ? 'rotate(0deg)' : 'rotate(-90deg)')};
    transition: transform 0.2s;
`;

const ToolsContainer = styled.div`
    background-color: rgba(var(--center-channel-color-rgb), 0.04);
    border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    display: flex;
    flex-direction: column;
    gap: 16px;
    padding: 12px 0;
`;

const ErrorIndicator = styled.div`
    display: flex;
    align-items: center;
    gap: 4px;
    font-size: 12px;
    font-weight: 600;
    color: var(--error-text);
`;

const OAuthIndicator = styled.div`
    display: flex;
    align-items: center;
    gap: 4px;
    font-size: 12px;
    font-weight: 600;
    color: var(--button-bg);
`;

const PluginBadge = styled.span`
    display: inline-flex;
    align-items: center;
    padding: 2px 8px;
    margin-right: 8px;
    font-size: 11px;
    font-weight: 600;
    color: var(--center-channel-bg);
    background-color: rgba(var(--center-channel-color-rgb), 0.56);
    border-radius: 10px;
`;

const ErrorMessage = styled.div`
    display: flex;
    align-items: flex-start;
    gap: 12px;
    padding: 16px;
    color: var(--error-text);
    background-color: rgba(var(--error-text-color-rgb), 0.04);
    border-radius: 4px;
    margin: 0 16px;
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
    margin: 0 16px;
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
`;

export default MCPServerToolRow;
