// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import {useIntl} from 'react-intl';
import DotsHorizontalIcon from '@mattermost/compass-icons/components/dots-horizontal';
import PencilOutlineIcon from '@mattermost/compass-icons/components/pencil-outline';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {PopoverMenu} from '@mattermost/compass-ui/components/popover-menu';

import Dropdown from '@/components/dropdown';
import {handleMenuNavigation} from '@/components/menu/menu_keyboard';
import {IconMenuItem} from '@/components/menu/menu_parts';
import {useMenuTransition} from '@/hooks/use_menu_transition';

import type {ChatActions, ChatActionTarget} from './use_chat_actions';

interface ChatRowActionsMenuProps {

    // The chat the actions apply to.
    chat: ChatActionTarget;

    // The list's chat actions (useChatActions); the list renders their dialogs.
    actions: ChatActions;

    // The trigger's accessible name.
    label: string;

    // 'sidebar' is the Agents sidebar's inverted x-small button (with a tooltip); 'panel' the RHS
    // history's small button.
    appearance: 'sidebar' | 'panel';
    onOpenChange?: (open: boolean) => void;
    testId?: string;
}

// The "..." actions menu of a chat row, shared by the Agents sidebar and the RHS chat history.
const ChatRowActionsMenu = ({chat, actions, label, appearance, onOpenChange, testId}: ChatRowActionsMenuProps) => {
    const intl = useIntl();
    const [open, setOpenState] = useState(false);
    const {rendered, onExited} = useMenuTransition(open);
    const setOpen = (next: boolean) => {
        setOpenState(next);
        onOpenChange?.(next);
    };

    // Every item closes the menu first.
    const closing = (action: () => void) => () => {
        setOpen(false);
        action();
    };

    const sidebar = appearance === 'sidebar';
    return (
        <Dropdown
            isOpen={rendered}
            onOpenChange={setOpen}
            placement='bottom-end'
            {...(sidebar ? {tooltip: label} : {})}
            target={(
                <IconButton
                    size={sidebar ? 'x-small' : 'small'}
                    {...(sidebar ? {style: 'inverted' as const} : {padding: 'compact' as const})}
                    icon={<Icon glyph={<DotsHorizontalIcon/>}/>}
                    aria-label={label}
                    aria-haspopup='menu'
                    aria-expanded={open}
                    active={open}
                    data-testid={testId}
                    onClick={(e) => {
                        // The row itself opens the chat.
                        e.stopPropagation();
                        setOpen(!open);
                    }}
                />
            )}
        >
            <PopoverMenu
                open={open}
                onExited={onExited}
                role='menu'
                aria-label={label}
                onKeyDown={handleMenuNavigation}

                // The menu is portaled, but React still bubbles its clicks to the row.
                onClick={(e: React.MouseEvent) => e.stopPropagation()}
            >
                <IconMenuItem
                    label={intl.formatMessage({defaultMessage: 'Rename chat'})}
                    icon={<PencilOutlineIcon/>}
                    onClick={closing(() => actions.startRename(chat))}
                />
            </PopoverMenu>
        </Dropdown>
    );
};

export default ChatRowActionsMenu;
