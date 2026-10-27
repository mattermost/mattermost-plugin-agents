// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useId, useState} from 'react';
import {useIntl} from 'react-intl';
import {useSelector} from 'react-redux';
import styled from 'styled-components';
import MessagePlusOutlineIcon from '@mattermost/compass-icons/components/message-plus-outline';
import PlusIcon from '@mattermost/compass-icons/components/plus';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {MenuItem} from '@mattermost/compass-ui/components/menu-item';
import {PopoverMenu, PopoverMenuGroup, PopoverMenuScroll} from '@mattermost/compass-ui/components/popover-menu';
import {UserAvatar} from '@mattermost/compass-ui/components/user-avatar';

import {useBotlist} from '@/bots';
import {getProfilePictureUrl} from '@/client';
import ComposerSubmenu from '@/components/agent_chat/composer_submenu';
import {focusFirstItem, handleMenuNavigation} from '@/components/agent_chat/composer_menu_keyboard';
import {LoadingRow, menuIcon} from '@/components/agent_chat/composer_menu_parts';
import Dropdown from '@/components/dropdown';
import {useMenuTransition} from '@/hooks/use_menu_transition';
import {userHasSystemPermission} from '@/utils/permissions';

import {canCreateAgent} from './agent_access';
import {useAgentsRoute} from './use_agents_route';

// No compass token for a scroll cap: the agents submenu scrolls past about ten rows
// (same reason and size class as the chat header's recent-chats list).
export const AGENTS_SUBMENU_MAX_HEIGHT = 320;

// No compass token for the resting and hover washes of sidebar-text on the header's "+"
// (the prototype forces the same 8% / 12% with !important).
const PLUS_FILL = 'rgba(var(--sidebar-text-rgb), 0.08)';
const PLUS_FILL_HOVER = 'rgba(var(--sidebar-text-rgb), 0.12)';

const PlusButton = styled(IconButton)`
    && {
        background-color: ${PLUS_FILL};
    }

    &&:hover:not(:disabled) {
        background-color: ${PLUS_FILL_HOVER};
    }
`;

// Fails closed: a store without the user or their roles yields false (no "New agent").
const useMayCreateAgent = (): boolean => useSelector((state: any) => {
    const userId = state?.entities?.users?.currentUserId;
    if (!userId) {
        return false;
    }
    return canCreateAgent({
        hasManageOwnAgent: userHasSystemPermission(state, userId, 'manage_own_agent'),
        hasManageSystem: userHasSystemPermission(state, userId, 'manage_system'),
    });
});

// The "+" at the right of the sidebar header (as the Channels category's): "New agent"
// opens the All agents create flow, "New chat" lists the agents to start a chat with.
// Needs the plugin's redux store (bots and permissions).
const SidebarAddMenu = () => {
    const intl = useIntl();
    const {toNewAgent, toChat} = useAgentsRoute();
    const {bots} = useBotlist();
    const mayCreate = useMayCreateAgent();
    const [open, setOpen] = useState(false);
    const menu = useMenuTransition(open);
    const [submenu, setSubmenu] = useState<{focus: boolean} | null>(null);

    const baseId = `sidebar-add-menu-${useId().replace(/:/g, '')}`;
    const menuId = `${baseId}-menu`;

    const setMenuOpen = useCallback((next: boolean) => {
        setOpen(next);
        if (!next) {
            setSubmenu(null);
        }
    }, []);

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

    const label = intl.formatMessage({defaultMessage: 'Add agent or chat'});
    const newChatLabel = intl.formatMessage({defaultMessage: 'New chat'});

    const handleTriggerKeyDown = (e: React.KeyboardEvent<HTMLButtonElement>) => {
        if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && !open) {
            e.preventDefault();
            setMenuOpen(true);
        }
    };

    let agentItems: React.ReactNode;
    if (!bots) {
        agentItems = <LoadingRow label={intl.formatMessage({defaultMessage: 'Loading agents…'})}/>;
    } else if (bots.length === 0) {
        agentItems = (
            <MenuItem
                role='menuitem'
                label={intl.formatMessage({defaultMessage: 'No agents available'})}
                disabled={true}
            />
        );
    } else {
        agentItems = bots.map((bot) => (
            <MenuItem
                key={bot.id}
                role='menuitem'
                label={bot.displayName}
                leadingElement={true}
                leadingVisual={(
                    <UserAvatar
                        src={getProfilePictureUrl(bot.id, bot.lastIconUpdate)}
                        alt=''
                        aria-hidden='true'
                        name={bot.displayName}
                        size='20'
                    />
                )}
                onClick={() => {
                    setMenuOpen(false);
                    toChat(bot.id);
                }}
            />
        ));
    }

    return (
        <Dropdown
            isOpen={menu.rendered}
            onOpenChange={setMenuOpen}
            placement='bottom-start'
            tooltip={label}
            target={(
                <PlusButton
                    size='small'
                    padding='compact'
                    rounded={true}
                    // eslint-disable-next-line react/style-prop-object -- compass IconButton `style` is a variant prop, not CSS
                    style='inverted'
                    icon={(
                        <Icon
                            glyph={<PlusIcon/>}
                            size='16'
                        />
                    )}
                    aria-label={label}
                    aria-haspopup='menu'
                    aria-expanded={open}
                    active={open}
                    data-testid='sidebar-add-menu-button'
                    onClick={() => setMenuOpen(!open)}
                    onKeyDown={handleTriggerKeyDown}
                />
            )}
        >
            <PopoverMenu
                id={menuId}
                open={open}
                onExited={menu.onExited}
                role='menu'
                aria-label={label}
                onKeyDown={handleMenuNavigation}
            >
                <PopoverMenuGroup>
                    <ComposerSubmenu
                        label={newChatLabel}
                        icon={<MessagePlusOutlineIcon/>}
                        menuLabel={intl.formatMessage({defaultMessage: 'Choose an agent'})}
                        grow='down'
                        open={submenu !== null}
                        focusOnOpen={submenu?.focus ?? false}
                        onOpen={(focus) => setSubmenu({focus})}
                        onClose={() => setSubmenu(null)}
                        data-testid='sidebar-add-menu-new-chat'
                    >
                        <PopoverMenuScroll maxHeight={AGENTS_SUBMENU_MAX_HEIGHT}>
                            <PopoverMenuGroup>
                                {agentItems}
                            </PopoverMenuGroup>
                        </PopoverMenuScroll>
                    </ComposerSubmenu>
                    {mayCreate && (
                        <MenuItem
                            role='menuitem'
                            label={intl.formatMessage({defaultMessage: 'Create new agent'})}
                            leadingElement={true}
                            leadingVisual={menuIcon(<PlusIcon/>)}
                            data-testid='sidebar-add-menu-new-agent'
                            onMouseEnter={() => setSubmenu(null)}
                            onClick={() => {
                                setMenuOpen(false);
                                toNewAgent();
                            }}
                        />
                    )}
                </PopoverMenuGroup>
            </PopoverMenu>
        </Dropdown>
    );
};

export default SidebarAddMenu;
