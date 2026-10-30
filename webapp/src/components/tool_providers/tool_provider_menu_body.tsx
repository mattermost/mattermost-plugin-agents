// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';
import RefreshIcon from '@mattermost/compass-icons/components/refresh';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {MenuItem} from '@mattermost/compass-ui/components/menu-item';
import {PopoverMenuDivider, PopoverMenuGroup} from '@mattermost/compass-ui/components/popover-menu';
import {Switch} from '@mattermost/compass-ui/components/switch';

import {TooltipAboveModals} from '@/components/dropdown';
import {ErrorRow, LoadingRow, StatusRow, menuIcon} from '@/components/menu/menu_parts';
import {mcpServerStatus} from '@/utils/mcp_availability';

import type {ToolProviders} from './tool_provider_popover';

export interface ToolProviderMenuBodyProps {
    providers: ToolProviders;
    onError: (message: string) => void;
}

// Reloads the providers from the servers (OAuth state included), as the old "Refresh" menu row did.
export const ProviderRefreshButton = ({providers}: {providers: ToolProviders}) => {
    const intl = useIntl();
    const label = intl.formatMessage({defaultMessage: 'Refresh tool providers'});
    return (
        <TooltipAboveModals title={label}>
            <TooltipTrigger>
                <IconButton
                    size='x-small'
                    icon={<Icon glyph={<RefreshIcon/>}/>}
                    aria-label={label}
                    disabled={providers.loading}
                    onClick={() => providers.fetchServers({showLoading: true, forceRefresh: true})}
                />
            </TooltipTrigger>
        </TooltipAboveModals>
    );
};

// The tool providers as compass menu rows, for the Tools dropdown (ToolProviderPopover) through
// its useToolProviders data and actions.
const ToolProviderMenuBody = ({providers, onError}: ToolProviderMenuBodyProps) => {
    const intl = useIntl();
    const {prefs, servers, loading, error, remoteMcpLicensed, toggle, connect, disconnect} = providers;
    const {status, disabledServers} = prefs;

    // Until the saved preferences load, no provider rows: the toggles would show every provider as
    // enabled, and a toggle would save over the saved list. A failed load offers a retry instead.
    if (status === 'error') {
        return (
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
    }
    if (status === 'loading' || (loading && servers.length === 0)) {
        return <LoadingRow label={intl.formatMessage({defaultMessage: 'Loading providers...'})}/>;
    }

    const disconnectable = servers.filter((s) => s.authenticated && s.needsOAuth && mcpServerStatus(s, false) !== 'sa-only-unavailable');
    return (
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
                                    leadingElement={false}
                                    trailingElement={true}
                                    trailingVisual={(
                                        <ConnectLabel>
                                            {intl.formatMessage({defaultMessage: 'Connect'})}
                                        </ConnectLabel>
                                    )}
                                    disabled={!server.authURL}
                                    onClick={() => server.authURL && connect(server.authURL)}
                                />
                            );
                        }
                        const enabled = !unavailable && !disabledServers.includes(server.serverOrigin);
                        return (
                            <MenuItem
                                key={server.serverOrigin}
                                role='menuitemcheckbox'
                                aria-checked={enabled}
                                label={server.name}
                                {...(unavailable ? {secondaryLabel: intl.formatMessage({defaultMessage: 'Unavailable'})} : {})}
                                secondaryLabelPosition='inline'
                                leadingElement={false}
                                trailingElement={true}
                                trailingVisual={(
                                    <RowSwitch
                                        size='small'
                                        checked={enabled}
                                        disabled={unavailable}
                                        readOnly={true}
                                        tabIndex={-1}
                                        aria-hidden={true}
                                    />
                                )}
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
            {disconnectable.length > 0 && (
                <>
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
                    </PopoverMenuGroup>
                </>
            )}
        </>
    );
};

export default ToolProviderMenuBody;

// The menu row is the control (role=menuitemcheckbox, aria-checked, click toggles); the switch on
// the right only shows its state, so it is out of the tab order and does not take pointer events.
const RowSwitch = styled(Switch)`
    && {
        width: auto;
        flex-shrink: 0;
        align-self: center;
        pointer-events: none;
    }
`;

// A not-yet-connected provider shows its Connect call to action as a primary button, as it did before the
// menu rows. The row is the control (a menuitem; a click opens the auth URL), and a button cannot
// sit inside it, so this only carries the look (an x-small primary button's type and padding).
const ConnectLabel = styled.span`
    flex-shrink: 0;
    padding: var(--spacing-xxxxs) var(--spacing-xs);
    border-radius: var(--radius-s);
    background: var(--button-bg);
    color: var(--button-color);
    font-size: var(--font-size-75);
    font-weight: var(--font-weight-semibold);
    line-height: var(--line-height-75);
`;

const TooltipTrigger = styled.span`
    display: inline-flex;
`;
