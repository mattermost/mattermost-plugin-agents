// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useCallback, useEffect} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {ChevronDownIcon, RefreshIcon} from '@mattermost/compass-icons/components';

import {disconnectMCPOAuth, getUserMCPTools, refreshUserMCPTools, updateUserToolPreferences, type UserMCPServerInfo} from '@/client';
import {EnabledMCPTool} from '@/bots';
import {useMCPConnectionEvents} from '@/hooks/use_mcp_connection_events';

import DotMenu, {DotMenuButton, DropdownMenu} from '../dot_menu';
import {ToggleSwitch} from '../toggle_switch';

export type {UserMCPServerInfo};

type ToolProviderPopoverProps = {
    disabledServers: string[];
    onDisabledServersChange: (servers: string[]) => void;
    preloadedServers?: UserMCPServerInfo[];
    enabledMCPTools?: EnabledMCPTool[] | null;
    autoEnableNewMCPTools?: boolean;
};

// filterServersByEnabledTools filters the server list to only show servers
// that the active agent is allowed to use. When autoEnableNewMCPTools is true,
// every server is shown. Otherwise only servers appearing in enabledTools are kept.
function filterServersByEnabledTools(
    servers: UserMCPServerInfo[],
    enabledTools: EnabledMCPTool[] | undefined | null,
    autoEnableNewMCPTools: boolean | undefined,
): UserMCPServerInfo[] {
    if (autoEnableNewMCPTools) {
        return servers;
    }
    const allowedOrigins = new Set((enabledTools ?? []).map((t) => t.server_origin));
    return servers.filter((s) => allowedOrigins.has(s.serverOrigin));
}

const ToolProviderPopover = ({disabledServers, onDisabledServersChange, preloadedServers, enabledMCPTools, autoEnableNewMCPTools}: ToolProviderPopoverProps) => {
    const intl = useIntl();
    const [allServers, setAllServers] = useState<UserMCPServerInfo[]>(preloadedServers || []);
    const [loading, setLoading] = useState(false);
    const refreshLabel = intl.formatMessage({defaultMessage: 'Refresh tool providers'});

    useEffect(() => {
        if (preloadedServers && preloadedServers.length > 0) {
            setAllServers(preloadedServers);
        }
    }, [preloadedServers]);

    const servers = filterServersByEnabledTools(allServers, enabledMCPTools, autoEnableNewMCPTools);

    const fetchServers = useCallback(async (opts: {showLoading?: boolean; forceRefresh?: boolean} = {showLoading: true}) => {
        if (opts.showLoading) {
            setLoading(true);
        }
        try {
            const response = opts.forceRefresh ? await refreshUserMCPTools() : await getUserMCPTools();
            setAllServers(response.servers);
        } catch (error) {
            // eslint-disable-next-line no-console
            console.error('Failed to fetch MCP tools for RHS popover:', error);
        } finally {
            if (opts.showLoading) {
                setLoading(false);
            }
        }
    }, []);

    useMCPConnectionEvents(useCallback(() => {
        fetchServers({showLoading: false});
    }, [fetchServers]));

    const handleToggle = useCallback(async (serverOrigin: string, enabled: boolean) => {
        let updatedDisabled: string[];
        if (enabled) {
            updatedDisabled = disabledServers.filter((s) => s !== serverOrigin);
        } else {
            updatedDisabled = [...disabledServers, serverOrigin];
        }
        onDisabledServersChange(updatedDisabled);

        try {
            await updateUserToolPreferences({disabled_servers: updatedDisabled});
        } catch {
            // Revert on error
            onDisabledServersChange(disabledServers);
        }
    }, [disabledServers, onDisabledServersChange]);

    const handleConnect = useCallback((authURL: string) => {
        window.open(authURL, '_blank', 'noopener,noreferrer');
    }, []);

    const handleDisconnect = useCallback(async (serverName: string) => {
        try {
            await disconnectMCPOAuth(serverName);
            await fetchServers();
        } catch (error) {
            // eslint-disable-next-line no-console
            console.error(`Failed to disconnect MCP OAuth for ${serverName}:`, error);
        }
    }, [fetchServers]);

    return (
        <DotMenu
            icon={
                <ToolProviderButtonContent>
                    <FormattedMessage defaultMessage='Tools'/>
                    <ChevronDownIcon size={12}/>
                </ToolProviderButtonContent>
            }
            dotMenuButton={ToolProviderButton}
            dropdownMenu={ProviderDropdownMenu}
            portal={false}
            placement='bottom-end'
            onOpenChange={(isOpen) => {
                if (isOpen) {
                    fetchServers();
                }
            }}
            closeOnClick={false}
        >
            <PopoverHeader>
                <PopoverHeaderTitle>
                    <FormattedMessage defaultMessage='Tool Providers'/>
                </PopoverHeaderTitle>
                <RefreshToolsButton
                    type='button'
                    aria-label={refreshLabel}
                    title={refreshLabel}
                    disabled={loading}
                    onMouseDown={(e) => {
                        e.preventDefault();
                        e.stopPropagation();
                    }}
                    onClick={(e) => {
                        e.preventDefault();
                        e.stopPropagation();
                        fetchServers({showLoading: true, forceRefresh: true});
                    }}
                >
                    <RefreshIcon size={14}/>
                </RefreshToolsButton>
            </PopoverHeader>
            {loading && servers.length === 0 && (
                <LoadingRow>
                    <FormattedMessage defaultMessage='Loading providers...'/>
                </LoadingRow>
            )}
            {!loading && servers.length === 0 && (
                <EmptyRow>
                    <FormattedMessage defaultMessage='No tool providers available'/>
                </EmptyRow>
            )}
            {servers.map((server) => (
                <ProviderRow key={server.serverOrigin}>
                    <ProviderAvatar>
                        {server.name.charAt(0).toUpperCase()}
                    </ProviderAvatar>
                    <ProviderName>{server.name}</ProviderName>
                    {!server.authenticated && server.needsOAuth ? (
                        <ConnectButton
                            onClick={() => server.authURL && handleConnect(server.authURL)}
                            disabled={!server.authURL}
                        >
                            <FormattedMessage defaultMessage='Connect'/>
                        </ConnectButton>
                    ) : (
                        <ProviderActions>
                            {server.needsOAuth && (
                                <DisconnectButton onClick={() => handleDisconnect(server.name)}>
                                    <FormattedMessage defaultMessage='Disconnect'/>
                                </DisconnectButton>
                            )}
                            <ToggleSwitch
                                checked={!disabledServers.includes(server.serverOrigin)}
                                onChange={(checked) => handleToggle(server.serverOrigin, checked)}
                            />
                        </ProviderActions>
                    )}
                </ProviderRow>
            ))}
        </DotMenu>
    );
};

const ToolProviderButton = styled(DotMenuButton)<{isActive: boolean}>`
    display: flex;
    align-items: center;
    padding: 2px 4px 2px 6px;
    border-radius: 4px;
    height: 20px;
    width: auto;
    font-size: 11px;
    font-weight: 600;
    line-height: 16px;
    color: ${(props) => (props.isActive ? 'var(--button-bg)' : 'var(--center-channel-color-rgb)')};
    background-color: ${(props) => (props.isActive ? 'rgba(var(--button-bg-rgb), 0.16)' : 'rgba(var(--center-channel-color-rgb), 0.08)')};

    &:hover {
        color: ${(props) => (props.isActive ? 'var(--button-bg)' : 'var(--center-channel-color-rgb)')};
        background-color: ${(props) => (props.isActive ? 'rgba(var(--button-bg-rgb), 0.16)' : 'rgba(var(--center-channel-color-rgb), 0.16)')};
    }
`;

const ToolProviderButtonContent = styled.div`
    display: flex;
    align-items: center;
    gap: 4px;
`;

const ProviderDropdownMenu = styled(DropdownMenu)`
    width: 262px;
    padding: 8px 0;
`;

const PopoverHeader = styled.div`
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 4px 8px 4px 16px;
`;

const PopoverHeaderTitle = styled.div`
    font-size: 12px;
    font-weight: 600;
    line-height: 16px;
    letter-spacing: 0.48px;
    text-transform: uppercase;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const RefreshToolsButton = styled.button`
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 24px;
    height: 24px;
    padding: 0;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    cursor: pointer;

    &:hover:not(:disabled) {
        background: rgba(var(--center-channel-color-rgb), 0.08);
        color: var(--center-channel-color);
    }

    &:disabled {
        cursor: default;
        opacity: 0.5;
    }
`;

const ProviderRow = styled.div`
    display: grid;
    grid-template-columns: 24px minmax(0, 1fr) auto;
    align-items: center;
    column-gap: 8px;
    padding: 6px 16px;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.08);
    }
`;

const ProviderAvatar = styled.div`
    width: 24px;
    height: 24px;
    border-radius: 4px;
    background: rgba(var(--center-channel-color-rgb), 0.08);
    display: flex;
    align-items: center;
    justify-content: center;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    font-size: 11px;
    font-weight: 600;
    flex-shrink: 0;
`;

const ProviderName = styled.div`
    min-width: 0;
    font-size: 14px;
    font-weight: 400;
    color: var(--center-channel-color);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const ProviderActions = styled.div`
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 8px;
    flex-shrink: 0;
    box-sizing: border-box;
    height: 24px;
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
`;

const DisconnectButton = styled.button`
    display: inline-flex;
    align-items: center;
    justify-content: center;
    box-sizing: border-box;
    margin: 0;
    padding: 0;
    border: none;
    min-width: 0;
    min-height: 0;
    height: 24px;
    background: none;
    font-family: inherit;
    font-size: 11px;
    font-weight: 600;
    line-height: 1;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    cursor: pointer;
    white-space: nowrap;
    flex-shrink: 0;

    &:hover {
        color: var(--error-text);
    }
`;

const LoadingRow = styled.div`
    padding: 12px 16px;
    text-align: center;
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const EmptyRow = styled(LoadingRow)``;

export default ToolProviderPopover;
