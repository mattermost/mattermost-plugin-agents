// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef} from 'react';

import {PopoverMenu} from '@mattermost/compass-ui/components/popover-menu';

import ChatActionsMenuItems from '@/components/agents/chat_actions_menu_items';
import Dropdown from '@/components/dropdown';
import {useMenuTransition} from '@/hooks/use_menu_transition';

interface ChatActionsPopoverProps {

    // The actions button of a history item the menu opens from; null while it is closed.
    anchor: HTMLElement | null;
    onClose: () => void;
    onRename: () => void;
}

// The chat actions menu of the RHS history, opened from the compass Thread List Item's own
// overflow button (the menu belongs to the list, not to the item).
const ChatActionsPopover = ({anchor, onClose, onRename}: ChatActionsPopoverProps) => {
    const open = anchor !== null;
    const menu = useMenuTransition(open);

    // The exit animation runs after `anchor` is gone, so it keeps the last one.
    const lastAnchor = useRef<HTMLElement | null>(null);
    useEffect(() => {
        if (anchor) {
            lastAnchor.current = anchor;
        }
    }, [anchor]);

    return (
        <Dropdown
            isOpen={menu.rendered}
            anchor={anchor ?? lastAnchor.current}
            placement='bottom-end'
            onOpenChange={(next) => {
                if (!next) {
                    onClose();
                }
            }}
        >
            <PopoverMenu
                open={open}
                onExited={menu.onExited}
                role='menu'
                onClick={(e: React.MouseEvent) => {
                    e.stopPropagation();
                    onClose();
                }}
            >
                <ChatActionsMenuItems onRename={onRename}/>
            </PopoverMenu>
        </Dropdown>
    );
};

export default ChatActionsPopover;
