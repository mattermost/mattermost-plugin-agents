// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useId, useState} from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';
import MessagePlusOutlineIcon from '@mattermost/compass-icons/components/message-plus-outline';
import PlusIcon from '@mattermost/compass-icons/components/plus';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {MenuItem} from '@mattermost/compass-ui/components/menu-item';
import {PopoverMenu, PopoverMenuGroup, PopoverMenuScroll} from '@mattermost/compass-ui/components/popover-menu';

import {useBotlist} from '@/bots';
import Dropdown from '@/components/dropdown';
import {handleMenuNavigation} from '@/components/menu/menu_keyboard';
import {AgentMenuItem, IconMenuItem, LoadingRow} from '@/components/menu/menu_parts';
import Submenu from '@/components/menu/submenu';
import {useFocusMenuOnOpen} from '@/components/menu/use_focus_menu_on_open';
import {useMenuTransition} from '@/hooks/use_menu_transition';

import {useAgentPermissions} from './agent_access';
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

// The "+" at the right of the sidebar header (as the Channels category's): "New agent"
// opens the All agents create flow, "New chat" lists the agents to start a chat with.
// Needs the plugin's redux store (bots and permissions).
const SidebarAddMenu = () => {
    const intl = useIntl();
    const {toNewAgent, toChat} = useAgentsRoute();
    const {bots} = useBotlist();
    const mayCreate = useAgentPermissions().canCreate;
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

    useFocusMenuOnOpen(open, menuId);

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
            <AgentMenuItem
                key={bot.id}
                bot={bot}
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
                    <Submenu
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
                    </Submenu>
                    {mayCreate && (
                        <IconMenuItem
                            label={intl.formatMessage({defaultMessage: 'Create new agent'})}
                            icon={<PlusIcon/>}
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
