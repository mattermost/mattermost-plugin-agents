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
import type {EnabledMCPTool, LLMBot} from '@/bots';
import {useMCPConnectionEvents} from '@/hooks/use_mcp_connection_events';
import {useMenuTransition} from '@/hooks/use_menu_transition';
import {useIsLicensedFor} from '@/license';

import Dropdown from '@/components/dropdown';
import {handleMenuNavigation} from '@/components/menu/menu_keyboard';
import {ErrorRow} from '@/components/menu/menu_parts';
import {useFocusMenuOnOpen} from '@/components/menu/use_focus_menu_on_open';

import ToolProviderMenuBody, {ProviderRefreshButton} from './tool_provider_menu_body';
import {useToolPreferences, type ToolPreferences} from './use_tool_preferences';

export type {UserMCPServerInfo};

export type ToolProviderPopoverProps = {

    // The agent whose tool providers are shown (and whose agent change reloads the preferences).
    bot: LLMBot;
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

export interface ToolProviders {

    // The user's saved preferences; providers can only be toggled once they are loaded.
    prefs: ToolPreferences;

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

// The tool provider data and actions behind ToolProviderPopover.
export function useToolProviders(bot: LLMBot): ToolProviders {
    const remoteMcpLicensed = useIsLicensedFor('remote_mcp');
    const prefs = useToolPreferences(bot.id);
    const [allServers, setAllServers] = useState<UserMCPServerInfo[]>([]);
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState(false);

    const servers = filterServersByEnabledTools(allServers, bot.enabledMCPTools, bot.autoEnableNewMCPTools);

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

    // The saved list is replaced as a whole, so a toggle before it loaded would overwrite it with
    // just that one provider: refused until the preferences are ready.
    const {status, disabledServers, setDisabledServers} = prefs;
    const toggle = useCallback(async (serverOrigin: string, enabled: boolean) => {
        if (status !== 'ready') {
            return false;
        }
        const updatedDisabled = enabled ? disabledServers.filter((s) => s !== serverOrigin) : [...disabledServers, serverOrigin];
        setDisabledServers(updatedDisabled);

        try {
            await updateUserToolPreferences({disabled_servers: updatedDisabled});
            return true;
        } catch {
            // Revert on error
            setDisabledServers(disabledServers);
            return false;
        }
    }, [status, disabledServers, setDisabledServers]);

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

    return {prefs, servers, loading, error, remoteMcpLicensed, fetchServers, toggle, connect, disconnect};
}

// The Tools dropdown of an agent chat's title: a compass Button trigger opening a menu of the tool
// providers.
const ToolProviderPopover = ({bot}: ToolProviderPopoverProps) => {
    const intl = useIntl();
    const providers = useToolProviders(bot);
    const {fetchServers} = providers;

    // Loaded quietly on mount, so the menu usually opens on the list (opening it fetches again).
    useEffect(() => {
        fetchServers({showLoading: false});
    }, [fetchServers]);

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

    useFocusMenuOnOpen(open, menuId);

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
                <TitleRow>
                    <PopoverMenuTitle>{title}</PopoverMenuTitle>
                    <ProviderRefreshButton providers={providers}/>
                </TitleRow>
                {errorMessage && <ErrorRow role='alert'>{errorMessage}</ErrorRow>}
                <ToolProviderMenuBody
                    providers={providers}
                    onError={setErrorMessage}
                />
            </PopoverMenu>
        </Dropdown>
    );
};

// The refresh button sits at the end of the title row.
const TitleRow = styled.div`
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding-right: var(--spacing-xs);
`;

// The floating reference needs an element that takes a ref; the compass Dropdown does not.
const TriggerWrap = styled.span`
    display: inline-flex;
`;

export default ToolProviderPopover;
