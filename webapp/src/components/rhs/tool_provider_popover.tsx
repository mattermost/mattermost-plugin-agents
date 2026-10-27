// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useCallback, useEffect, useRef} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {ChevronDownIcon, RefreshIcon} from '@mattermost/compass-icons/components';

import {Button} from '@mattermost/compass-ui/components/button';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {PopoverMenuTitle} from '@mattermost/compass-ui/components/popover-menu';
import {Spinner} from '@mattermost/compass-ui/components/spinner';
import {WithTooltip} from '@mattermost/shared/components/tooltip';

import {disconnectMCPOAuth, getUserMCPTools, refreshUserMCPTools, updateUserToolPreferences, type UserMCPServerInfo} from '@/client';
import {EnabledMCPTool} from '@/bots';
import {useMCPConnectionEvents} from '@/hooks/use_mcp_connection_events';
import {mcpServerStatus} from '@/utils/mcp_availability';
import {useIsLicensedFor} from '@/license';

import DotMenu, {DotMenuButton, DropdownMenu} from '../dot_menu';
import MCPUnavailableBadge from '../mcp_unavailable_badge';
import {ToggleSwitch} from '../toggle_switch';

export type {UserMCPServerInfo};

export type ToolProviderPopoverProps = {
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

export type UseToolProvidersArgs = ToolProviderPopoverProps;

export interface ToolProviders {

    // Servers the agent may use (see filterServersByEnabledTools).
    servers: UserMCPServerInfo[];
    loading: boolean;

    // Set when the last fetch failed; cleared by the next successful one.
    error: boolean;
    remoteMcpLicensed: boolean;
    fetchServers: (opts?: {showLoading?: boolean; forceRefresh?: boolean}) => Promise<void>;

    // Optimistic; reverts and resolves false when the preference save fails.
    toggle: (serverOrigin: string, enabled: boolean) => Promise<boolean>;
    connect: (authURL: string) => void;

    // Resolves false when the disconnect fails.
    disconnect: (serverName: string) => Promise<boolean>;
}

// The disabled-provider list is one per-user preference, but several controls
// hold a copy (the RHS and Agents header popovers, the composer's "+" menu).
// A toggle in one is published here so every other mounted copy follows
// without a reload. In memory only; the server stays the source of truth.
type DisabledServersListener = (servers: string[], source: symbol) => void;
const disabledServersListeners = new Set<DisabledServersListener>();

export function publishDisabledServers(servers: string[], source: symbol) {
    for (const listener of Array.from(disabledServersListeners)) {
        listener(servers, source);
    }
}

export function subscribeDisabledServers(listener: DisabledServersListener): () => void {
    disabledServersListeners.add(listener);
    return () => {
        disabledServersListeners.delete(listener);
    };
}

// The tool provider data and actions behind ToolProviderPopover, shared with
// the agent chat composer's "+" menu so both behave the same.
export function useToolProviders({disabledServers, onDisabledServersChange, preloadedServers, enabledMCPTools, autoEnableNewMCPTools}: UseToolProvidersArgs): ToolProviders {
    const remoteMcpLicensed = useIsLicensedFor('remote_mcp');
    const [allServers, setAllServers] = useState<UserMCPServerInfo[]>(preloadedServers || []);
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState(false);

    useEffect(() => {
        if (preloadedServers && preloadedServers.length > 0) {
            setAllServers(preloadedServers);
        }
    }, [preloadedServers]);

    const servers = filterServersByEnabledTools(allServers, enabledMCPTools, autoEnableNewMCPTools);

    // Only the latest fetch may write, so a slow earlier response never
    // overwrites a newer list.
    const latestFetch = useRef(0);
    const fetchServers = useCallback(async (opts: {showLoading?: boolean; forceRefresh?: boolean} = {showLoading: true}) => {
        const id = ++latestFetch.current;
        if (opts.showLoading) {
            setLoading(true);
        }
        try {
            const response = opts.forceRefresh ? await refreshUserMCPTools() : await getUserMCPTools();
            if (id === latestFetch.current) {
                setAllServers(response.servers);
                setError(false);
            }
        } catch (e) {
            // eslint-disable-next-line no-console
            console.error('Failed to fetch MCP tools for RHS popover:', e);
            if (id === latestFetch.current) {
                setError(true);
            }
        } finally {
            if (opts.showLoading && id === latestFetch.current) {
                setLoading(false);
            }
        }
    }, []);

    useMCPConnectionEvents(useCallback(() => {
        fetchServers({showLoading: false});
    }, [fetchServers]));

    // Other copies' changes land here; this copy's own are not echoed back.
    const [source] = useState(() => Symbol('tool-providers'));
    const onChangeRef = useRef(onDisabledServersChange);
    onChangeRef.current = onDisabledServersChange;
    useEffect(() => subscribeDisabledServers((servers, from) => {
        if (from !== source) {
            onChangeRef.current(servers);
        }
    }), [source]);

    const toggle = useCallback(async (serverOrigin: string, enabled: boolean) => {
        let updatedDisabled: string[];
        if (enabled) {
            updatedDisabled = disabledServers.filter((s) => s !== serverOrigin);
        } else {
            updatedDisabled = [...disabledServers, serverOrigin];
        }
        onDisabledServersChange(updatedDisabled);
        publishDisabledServers(updatedDisabled, source);

        try {
            await updateUserToolPreferences({disabled_servers: updatedDisabled});
            return true;
        } catch {
            // Revert on error
            onDisabledServersChange(disabledServers);
            publishDisabledServers(disabledServers, source);
            return false;
        }
    }, [disabledServers, onDisabledServersChange, source]);

    const connect = useCallback((authURL: string) => {
        window.open(authURL, '_blank', 'noopener,noreferrer');
    }, []);

    const disconnect = useCallback(async (serverName: string) => {
        try {
            await disconnectMCPOAuth(serverName);
            await fetchServers();
            return true;
        } catch (e) {
            // eslint-disable-next-line no-console
            console.error(`Failed to disconnect MCP OAuth for ${serverName}:`, e);
            return false;
        }
    }, [fetchServers]);

    return {servers, loading, error, remoteMcpLicensed, fetchServers, toggle, connect, disconnect};
}

const ToolProviderPopover = ({disabledServers, onDisabledServersChange, preloadedServers, enabledMCPTools, autoEnableNewMCPTools}: ToolProviderPopoverProps) => {
    const intl = useIntl();
    const refreshLabel = intl.formatMessage({defaultMessage: 'Refresh tool providers'});
    const {servers, loading, remoteMcpLicensed, fetchServers, toggle: handleToggle, connect: handleConnect, disconnect: handleDisconnect} = useToolProviders({
        disabledServers,
        onDisabledServersChange,
        preloadedServers,
        enabledMCPTools,
        autoEnableNewMCPTools,
    });

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
                <WithTooltip title={refreshLabel}>
                    <IconButton
                        size='x-small'
                        icon={<Icon glyph={<RefreshIcon/>}/>}
                        aria-label={refreshLabel}
                        loading={loading}
                        onMouseDown={(e) => {
                            e.preventDefault();
                            e.stopPropagation();
                        }}
                        onClick={(e) => {
                            e.preventDefault();
                            e.stopPropagation();
                            fetchServers({showLoading: true, forceRefresh: true});
                        }}
                    />
                </WithTooltip>
            </PopoverHeader>
            {loading && servers.length === 0 && (
                <LoadingRow>
                    <Spinner size='16'/>
                    <FormattedMessage defaultMessage='Loading providers...'/>
                </LoadingRow>
            )}
            {!loading && servers.length === 0 && (
                <EmptyRow>
                    <FormattedMessage defaultMessage='No tool providers available'/>
                </EmptyRow>
            )}
            {servers.map((server) => {
                const status = mcpServerStatus(server, false);
                const unavailable = status === 'sa-only-unavailable';
                const showConnect = remoteMcpLicensed && !server.authenticated && server.needsOAuth && !unavailable;
                return (
                    <ProviderRow
                        key={server.serverOrigin}
                        $unavailable={unavailable}
                    >
                        <ProviderAvatar>
                            {server.name.charAt(0).toUpperCase()}
                        </ProviderAvatar>
                        <ProviderName>{server.name}</ProviderName>
                        {showConnect ? (
                            <Button
                                emphasis='primary'
                                size='x-small'
                                onClick={() => server.authURL && handleConnect(server.authURL)}
                                disabled={!server.authURL}
                            >
                                <FormattedMessage defaultMessage='Connect'/>
                            </Button>
                        ) : (
                            <ProviderActions>
                                {unavailable && <MCPUnavailableBadge/>}
                                {!unavailable && server.authenticated && server.needsOAuth && (
                                    <Button
                                        emphasis='link'
                                        destructive={true}
                                        size='x-small'
                                        onClick={() => handleDisconnect(server.name)}
                                    >
                                        <FormattedMessage defaultMessage='Disconnect'/>
                                    </Button>
                                )}
                                <ToggleSwitch
                                    checked={!unavailable && !disabledServers.includes(server.serverOrigin)}
                                    disabled={unavailable}
                                    onChange={(checked) => handleToggle(server.serverOrigin, checked)}
                                />
                            </ProviderActions>
                        )}
                    </ProviderRow>
                );
            })}
        </DotMenu>
    );
};

const ToolProviderButton = styled(DotMenuButton)<{$isActive: boolean}>`
    display: flex;
    align-items: center;
    padding: 2px 4px 2px 6px;
    border-radius: 4px;
    height: 20px;
    width: auto;
    font-size: 11px;
    font-weight: 600;
    line-height: 16px;
    color: ${(props) => (props.$isActive ? 'var(--button-bg)' : 'var(--center-channel-color-rgb)')};
    background-color: ${(props) => (props.$isActive ? 'rgba(var(--button-bg-rgb), 0.16)' : 'rgba(var(--center-channel-color-rgb), 0.08)')};

    &:hover {
        color: ${(props) => (props.$isActive ? 'var(--button-bg)' : 'var(--center-channel-color-rgb)')};
        background-color: ${(props) => (props.$isActive ? 'rgba(var(--button-bg-rgb), 0.16)' : 'rgba(var(--center-channel-color-rgb), 0.16)')};
    }
`;

const ToolProviderButtonContent = styled.div`
    display: flex;
    align-items: center;
    gap: 4px;
`;

const ProviderDropdownMenu = styled(DropdownMenu)`
    && {
        width: 262px;
    }
`;

const PopoverHeader = styled.div`
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding-right: 8px;
`;

const PopoverHeaderTitle = styled(PopoverMenuTitle)`
    && {
        padding-bottom: var(--spacing-xxs);
    }
`;

const ProviderRow = styled.div<{$unavailable?: boolean}>`
    display: grid;
    grid-template-columns: 24px minmax(0, 1fr) auto;
    align-items: center;
    column-gap: 8px;
    padding: 6px 16px;
    opacity: ${(p) => (p.$unavailable ? 0.64 : 1)};

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

const LoadingRow = styled.div`
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    padding: 12px 16px;
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const EmptyRow = styled(LoadingRow)``;

export default ToolProviderPopover;
