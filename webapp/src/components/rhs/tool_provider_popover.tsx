// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useCallback, useEffect, useId, useRef} from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';

import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import ChevronUpIcon from '@mattermost/compass-icons/components/chevron-up';
import {Button} from '@mattermost/compass-ui/components/button';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {PopoverMenu, PopoverMenuTitle} from '@mattermost/compass-ui/components/popover-menu';

import {disconnectMCPOAuth, getUserMCPTools, refreshUserMCPTools, updateUserToolPreferences, type UserMCPServerInfo} from '@/client';
import {EnabledMCPTool} from '@/bots';
import {useMCPConnectionEvents} from '@/hooks/use_mcp_connection_events';
import {useMenuTransition} from '@/hooks/use_menu_transition';
import {useIsLicensedFor} from '@/license';

import {focusFirstItem, handleMenuNavigation} from '../agent_chat/composer_menu_keyboard';
import {ErrorRow} from '../agent_chat/composer_menu_parts';
import ToolProviderMenuBody from '../agent_chat/tool_provider_menu_body';
import Dropdown from '../dropdown';

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

// The Tools dropdown of the RHS header: a compass Button trigger opening a menu of the tool
// providers (the same rows as the composer's "+" menu).
const ToolProviderPopover = ({disabledServers, onDisabledServersChange, preloadedServers, enabledMCPTools, autoEnableNewMCPTools}: ToolProviderPopoverProps) => {
    const intl = useIntl();
    const providers = useToolProviders({
        disabledServers,
        onDisabledServersChange,
        preloadedServers,
        enabledMCPTools,
        autoEnableNewMCPTools,
    });
    const {fetchServers} = providers;
    const [open, setOpen] = useState(false);
    const menu = useMenuTransition(open);
    const [errorMessage, setErrorMessage] = useState('');
    const menuId = `tool-providers-${useId().replace(/:/g, '')}`;

    const setMenuOpen = useCallback((next: boolean) => {
        setOpen(next);
        if (next) {
            setErrorMessage('');
            fetchServers();
        }
    }, [fetchServers]);

    // The Dropdown's focus manager moves focus in on mount; a reopen during the exit
    // transition keeps it mounted, so move focus here too.
    useEffect(() => {
        if (!open) {
            return () => null;
        }
        const frame = requestAnimationFrame(() => {
            const el = document.getElementById(menuId);
            if (el && !el.contains(document.activeElement)) {
                focusFirstItem(el);
            }
        });
        return () => cancelAnimationFrame(frame);
    }, [open, menuId]);

    const title = intl.formatMessage({defaultMessage: 'Tool Providers'});
    return (
        <Dropdown
            isOpen={menu.rendered}
            onOpenChange={setMenuOpen}
            placement='bottom-end'
            target={(
                <TriggerWrap>
                    <Button
                        size='x-small'
                        emphasis='dim'
                        trailingIcon={<Icon glyph={open ? <ChevronUpIcon/> : <ChevronDownIcon/>}/>}
                        aria-haspopup='menu'
                        aria-expanded={open}
                        onClick={() => setMenuOpen(!open)}
                    >
                        {intl.formatMessage({defaultMessage: 'Tools'})}
                    </Button>
                </TriggerWrap>
            )}
        >
            <PopoverMenu
                id={menuId}
                open={open}
                onExited={menu.onExited}
                role='menu'
                aria-label={title}
                data-testid='dropdownmenu'
                onKeyDown={handleMenuNavigation}
            >
                <PopoverMenuTitle>{title}</PopoverMenuTitle>
                {errorMessage && <ErrorRow role='alert'>{errorMessage}</ErrorRow>}
                <ToolProviderMenuBody
                    status='ready'
                    onRetry={fetchServers}
                    disabledServers={disabledServers}
                    providers={providers}
                    onError={setErrorMessage}
                />
            </PopoverMenu>
        </Dropdown>
    );
};

// The floating reference needs an element that takes a ref; the compass Dropdown does not.
const TriggerWrap = styled.span`
    display: inline-flex;
`;

export default ToolProviderPopover;
