// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect} from 'react';
import {useIntl} from 'react-intl';
import HammerIcon from '@mattermost/compass-icons/components/hammer';
import RefreshIcon from '@mattermost/compass-icons/components/refresh';

import {MenuItem} from '@mattermost/compass-ui/components/menu-item';
import {PopoverMenuDivider, PopoverMenuGroup} from '@mattermost/compass-ui/components/popover-menu';

import type {LLMBot} from '@/bots';
import {useToolProviders} from '@/components/rhs/tool_provider_popover';
import {mcpServerStatus} from '@/utils/mcp_availability';

import ComposerSubmenu from './composer_submenu';
import {ErrorRow, LoadingRow, StatusRow, menuIcon} from './composer_menu_parts';
import {useComposerToolPreferences} from './use_composer_tool_preferences';

// The composer menu shows Tools only where the RHS and the Agents header show
// their tools popover: a new chat with an agent that does not use service
// account auth (those have no per-user connections or preferences).
export function showComposerTools(bot: LLMBot | null | undefined, rootId: string): bot is LLMBot {
    return Boolean(bot) && !rootId && bot?.useServiceAccountAuth !== true;
}

interface Props {
    bot: LLMBot;
    open: boolean;
    focusOnOpen: boolean;
    onOpen: (focus: boolean) => void;
    onClose: () => void;
    onError: (message: string) => void;
}

// The tools popover's providers as checkable menu rows, through the same
// useToolProviders data and actions.
const ComposerToolsSubmenu = ({bot, open, focusOnOpen, onOpen, onClose, onError}: Props) => {
    const intl = useIntl();
    const prefs = useComposerToolPreferences();
    const {servers, loading, error, remoteMcpLicensed, fetchServers, toggle, connect, disconnect} = useToolProviders({
        disabledServers: prefs.disabledServers,
        onDisabledServersChange: prefs.setDisabledServers,
        enabledMCPTools: bot.enabledMCPTools,
        autoEnableNewMCPTools: bot.autoEnableNewMCPTools,
    });

    // As the popover does when it opens.
    useEffect(() => {
        fetchServers();
    }, [fetchServers]);

    const label = intl.formatMessage({defaultMessage: 'Tools'});
    const refreshLabel = intl.formatMessage({defaultMessage: 'Refresh tool providers'});

    let body: React.ReactNode;
    if (prefs.status === 'error') {
        body = (
            <>
                <ErrorRow role='alert'>
                    {intl.formatMessage({defaultMessage: 'Couldn\'t load your tool settings.'})}
                </ErrorRow>
                <MenuItem
                    role='menuitem'
                    label={intl.formatMessage({defaultMessage: 'Try again'})}
                    leadingElement={true}
                    leadingVisual={menuIcon(<RefreshIcon/>)}
                    onClick={prefs.reload}
                />
            </>
        );
    } else if (prefs.status === 'loading' || (loading && servers.length === 0)) {
        body = <LoadingRow label={intl.formatMessage({defaultMessage: 'Loading providers...'})}/>;
    } else {
        const disconnectable = servers.filter((s) => s.authenticated && s.needsOAuth && mcpServerStatus(s, false) !== 'sa-only-unavailable');
        body = (
            <>
                {error && (
                    <ErrorRow role='alert'>
                        {intl.formatMessage({defaultMessage: 'Couldn\'t load tool providers.'})}
                    </ErrorRow>
                )}
                {!error && servers.length === 0 && (
                    <StatusRow role='status'>
                        {intl.formatMessage({defaultMessage: 'No tool providers available'})}
                    </StatusRow>
                )}
                {servers.length > 0 && (
                    <PopoverMenuGroup aria-label={intl.formatMessage({defaultMessage: 'Tool providers'})}>
                        {servers.map((server) => {
                            const unavailable = mcpServerStatus(server, false) === 'sa-only-unavailable';
                            const showConnect = remoteMcpLicensed && !server.authenticated && server.needsOAuth && !unavailable;
                            if (showConnect) {
                                return (
                                    <MenuItem
                                        key={server.serverOrigin}
                                        role='menuitem'
                                        label={server.name}
                                        secondaryLabel={intl.formatMessage({defaultMessage: 'Connect'})}
                                        secondaryLabelPosition='inline'
                                        leadingElement={false}
                                        disabled={!server.authURL}
                                        onClick={() => server.authURL && connect(server.authURL)}
                                    />
                                );
                            }
                            const enabled = !unavailable && !prefs.disabledServers.includes(server.serverOrigin);
                            return (
                                <MenuItem
                                    key={server.serverOrigin}
                                    role='menuitemcheckbox'
                                    aria-checked={enabled}
                                    label={server.name}
                                    {...(unavailable ? {secondaryLabel: intl.formatMessage({defaultMessage: 'Unavailable'})} : {})}
                                    secondaryLabelPosition='inline'
                                    leadingElement={false}
                                    trailingElement={enabled}
                                    disabled={unavailable}
                                    onClick={async () => {
                                        const saved = await toggle(server.serverOrigin, !enabled);
                                        if (!saved) {
                                            onError(intl.formatMessage({defaultMessage: 'Couldn\'t update {name}. Try again.'}, {name: server.name}));
                                        }
                                    }}
                                />
                            );
                        })}
                    </PopoverMenuGroup>
                )}
                <PopoverMenuDivider/>
                <PopoverMenuGroup>
                    {disconnectable.map((server) => (
                        <MenuItem
                            key={server.serverOrigin}
                            role='menuitem'
                            label={intl.formatMessage({defaultMessage: 'Disconnect {name}'}, {name: server.name})}
                            leadingElement={false}
                            destructive={true}
                            onClick={async () => {
                                const done = await disconnect(server.name);
                                if (!done) {
                                    onError(intl.formatMessage({defaultMessage: 'Couldn\'t disconnect {name}. Try again.'}, {name: server.name}));
                                }
                            }}
                        />
                    ))}
                    <MenuItem
                        role='menuitem'
                        label={refreshLabel}
                        leadingElement={true}
                        leadingVisual={menuIcon(<RefreshIcon/>)}
                        onClick={() => {
                            if (!loading) {
                                fetchServers({showLoading: true, forceRefresh: true});
                            }
                        }}
                    />
                </PopoverMenuGroup>
            </>
        );
    }

    return (
        <ComposerSubmenu
            label={label}
            icon={<HammerIcon/>}
            menuLabel={label}
            open={open}
            focusOnOpen={focusOnOpen}
            onOpen={onOpen}
            onClose={onClose}
            data-testid='composer-plus-menu-tools'
        >
            {body}
        </ComposerSubmenu>
    );
};

export default ComposerToolsSubmenu;
