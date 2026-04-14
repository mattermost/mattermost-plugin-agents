// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useCallback, useEffect, useRef} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';
import {ChevronDownIcon} from '@mattermost/compass-icons/components';

import {disconnectUserMCPServerAuth, getUserMCPTools, updateUserToolPreferences} from '@/client';

import {TertiaryButton} from '../assets/buttons';
import DotMenu, {DotMenuButton, DropdownMenu} from '../dot_menu';
import {ToggleSwitch} from '../toggle_switch';

export type UserMCPServerInfo = {
    name: string;
    serverOrigin: string;
    authenticated: boolean;
    authURL?: string;
    canDisconnect: boolean;
    tools: Array<{
        name: string;
        description: string;
        enabled: boolean;
        policy: string;
    }>;
};

type ToolProviderPopoverProps = {
    disabledServers: string[];
    onDisabledServersChange: (servers: string[]) => void;
    preloadedServers?: UserMCPServerInfo[];
};

const ToolProviderPopover = ({disabledServers, onDisabledServersChange, preloadedServers}: ToolProviderPopoverProps) => {
    const [servers, setServers] = useState<UserMCPServerInfo[]>(preloadedServers || []);
    const [loading, setLoading] = useState(false);
    const [connectingServerOrigin, setConnectingServerOrigin] = useState<string | null>(null);
    const [disconnectingServerOrigin, setDisconnectingServerOrigin] = useState<string | null>(null);
    const authRefreshIntervalRef = useRef<number | null>(null);

    useEffect(() => {
        if (preloadedServers && preloadedServers.length > 0) {
            setServers(preloadedServers);
        }
    }, [preloadedServers]);

    const stopAuthRefreshPolling = useCallback(() => {
        if (authRefreshIntervalRef.current !== null) {
            window.clearInterval(authRefreshIntervalRef.current);
            authRefreshIntervalRef.current = null;
        }
        setConnectingServerOrigin(null);
    }, []);

    useEffect(() => {
        return () => {
            if (authRefreshIntervalRef.current !== null) {
                window.clearInterval(authRefreshIntervalRef.current);
            }
        };
    }, []);

    const fetchServers = useCallback(async () => {
        setLoading(true);
        try {
            const response = await getUserMCPTools();
            setServers(response.servers);
        } catch {
            // Silently fail - servers stay empty
        }
        setLoading(false);
    }, []);

    useEffect(() => {
        if (connectingServerOrigin && servers.some((server) => (
            server.serverOrigin === connectingServerOrigin && server.authenticated
        ))) {
            stopAuthRefreshPolling();
        }
    }, [connectingServerOrigin, servers, stopAuthRefreshPolling]);

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

    const startAuthRefreshPolling = useCallback((serverOrigin: string) => {
        stopAuthRefreshPolling();
        setConnectingServerOrigin(serverOrigin);

        let attempts = 0;
        authRefreshIntervalRef.current = window.setInterval(async () => {
            attempts += 1;

            try {
                const response = await getUserMCPTools();
                setServers(response.servers);

                const isAuthenticated = response.servers.some((server) => (
                    server.serverOrigin === serverOrigin && server.authenticated
                ));
                if (isAuthenticated || attempts >= 15) {
                    stopAuthRefreshPolling();
                }
            } catch {
                if (attempts >= 15) {
                    stopAuthRefreshPolling();
                }
            }
        }, 2000);
    }, [stopAuthRefreshPolling]);

    const handleConnect = useCallback((serverOrigin: string, authURL?: string) => {
        if (!authURL) {
            return;
        }

        window.open(authURL, '_blank', 'noopener,noreferrer');
        startAuthRefreshPolling(serverOrigin);
    }, [startAuthRefreshPolling]);

    const handleDisconnect = useCallback(async (serverOrigin: string) => {
        setDisconnectingServerOrigin(serverOrigin);

        try {
            await disconnectUserMCPServerAuth(serverOrigin);
            await fetchServers();
        } catch {
            // Silently fail - current state remains visible
        } finally {
            setDisconnectingServerOrigin(null);
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
                <FormattedMessage defaultMessage='Tool Providers'/>
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
                    <ProviderIdentity>
                        <ProviderAvatar>
                            {server.name.charAt(0).toUpperCase()}
                        </ProviderAvatar>
                        <ProviderText>
                            <ProviderName>{server.name}</ProviderName>
                            {(server.canDisconnect || server.authURL) && (
                                <ProviderStatus $authenticated={server.authenticated}>
                                    {server.authenticated ? (
                                        <FormattedMessage defaultMessage='Connected'/>
                                    ) : (
                                        <FormattedMessage defaultMessage='Disconnected'/>
                                    )}
                                </ProviderStatus>
                            )}
                        </ProviderText>
                    </ProviderIdentity>
                    <ProviderActions>
                        {(server.canDisconnect || server.authURL) && (
                            <ProviderAuthButton
                                type='button'
                                onClick={() => {
                                    if (server.canDisconnect) {
                                        handleDisconnect(server.serverOrigin);
                                        return;
                                    }

                                    handleConnect(server.serverOrigin, server.authURL);
                                }}
                                disabled={connectingServerOrigin === server.serverOrigin || disconnectingServerOrigin === server.serverOrigin}
                            >
                                {connectingServerOrigin === server.serverOrigin && (
                                    <FormattedMessage defaultMessage='Connecting...'/>
                                )}
                                {disconnectingServerOrigin === server.serverOrigin && (
                                    <FormattedMessage defaultMessage='Disconnecting...'/>
                                )}
                                {connectingServerOrigin !== server.serverOrigin && disconnectingServerOrigin !== server.serverOrigin && (
                                    server.canDisconnect ? (
                                        <FormattedMessage defaultMessage='Disconnect'/>
                                    ) : (
                                        <FormattedMessage defaultMessage='Connect'/>
                                    )
                                )}
                            </ProviderAuthButton>
                        )}
                        <ToggleSwitch
                            checked={!disabledServers.includes(server.serverOrigin)}
                            onChange={(checked) => handleToggle(server.serverOrigin, checked)}
                            ariaLabel={server.name}
                        />
                    </ProviderActions>
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
    padding: 8px 16px;
    font-size: 12px;
    font-weight: 600;
    line-height: 16px;
    letter-spacing: 0.48px;
    text-transform: uppercase;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const ProviderRow = styled.div`
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 8px 16px;
    gap: 12px;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.08);
    }
`;

const ProviderIdentity = styled.div`
    display: flex;
    align-items: center;
    min-width: 0;
    gap: 8px;
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
    font-size: 14px;
    font-weight: 400;
    color: var(--center-channel-color);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const ProviderText = styled.div`
    display: flex;
    flex-direction: column;
    min-width: 0;
`;

const ProviderStatus = styled.div<{$authenticated: boolean}>`
    font-size: 12px;
    line-height: 16px;
    color: ${({$authenticated}) => (
        $authenticated ? 'rgba(var(--center-channel-color-rgb), 0.64)' : 'var(--button-bg)'
    )};
`;

const ProviderActions = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
    flex-shrink: 0;
`;

const ProviderAuthButton = styled(TertiaryButton)`
    height: 28px;
    padding: 0 10px;
    font-size: 12px;
    white-space: nowrap;
`;

const LoadingRow = styled.div`
    padding: 12px 16px;
    text-align: center;
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const EmptyRow = styled(LoadingRow)``;

export default ToolProviderPopover;
