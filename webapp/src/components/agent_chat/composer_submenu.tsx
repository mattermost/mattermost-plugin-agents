// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useId, useLayoutEffect, useRef, useState} from 'react';
import styled from 'styled-components';
import ChevronRightIcon from '@mattermost/compass-icons/components/chevron-right';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {MenuItem} from '@mattermost/compass-ui/components/menu-item';
import {PopoverMenu} from '@mattermost/compass-ui/components/popover-menu';

import {useMenuTransition} from '@/hooks/use_menu_transition';

import {focusFirstItem, handleMenuNavigation, isTextField} from './composer_menu_keyboard';

export interface ComposerSubmenuProps {

    // The parent row.
    label: string;
    icon: React.ReactNode;
    secondaryLabel?: React.ReactNode;
    disabled?: boolean;

    // Names the child menu.
    menuLabel: string;
    open: boolean;

    // Which way the child grows from the parent row: 'up' (default, for menus
    // opening above their trigger, like the composer's) or 'down'.
    grow?: 'up' | 'down';

    // Keyboard opens move focus into the child menu; hover leaves it on the row.
    focusOnOpen: boolean;
    onOpen: (focus: boolean) => void;

    // Closes the child menu only; focus goes back to the parent row.
    onClose: () => void;
    children: React.ReactNode;
    'data-testid'?: string;
}

// How long the pointer may be outside the row and its child menu before a menu that hover opened
// closes (covers the gap between them and a quick slip). No compass token for this.
const HOVER_CLOSE_DELAY_MS = 150;

// A parent menu row with a cascading child menu, as the host's AI actions menu
// has: it opens on click, Enter, Space, ArrowRight or hover, and ArrowLeft or
// Escape close it again. A menu that hover opened also closes when the pointer
// leaves the row and the child menu; one opened by click or keyboard stays until closed. The child renders inside the parent menu's floating
// element (not a second portal), so the parent's outside-click dismissal and
// focus trap treat it as part of the menu.
const ComposerSubmenu = ({label, icon, secondaryLabel, disabled, menuLabel, open, grow = 'up', focusOnOpen, onOpen, onClose, children, ...rest}: ComposerSubmenuProps) => {
    const menuId = `composer-submenu-${useId().replace(/:/g, '')}`;
    const rowRef = useRef<HTMLButtonElement>(null);
    const anchorRef = useRef<HTMLDivElement>(null);
    const {rendered, onExited} = useMenuTransition(open);

    // Which side of the parent the child opens on: right and growing upward
    // (the composer sits at the bottom), flipped where that leaves the viewport.
    const [side, setSide] = useState<{x: 'right' | 'left'; y: 'up' | 'down'}>({x: 'right', y: grow});
    useLayoutEffect(() => {
        if (!open) {
            setSide({x: 'right', y: grow});
            return;
        }
        const rect = anchorRef.current?.getBoundingClientRect();
        if (!rect) {
            return;
        }
        setSide((prev) => ({
            x: prev.x === 'right' && rect.right > window.innerWidth ? 'left' : prev.x,
            y: prev.y === 'up' && rect.top < 0 ? 'down' : prev.y,
        }));
    }, [open, grow]);

    useEffect(() => {
        if (open && focusOnOpen) {
            focusFirstItem(anchorRef.current?.querySelector<HTMLElement>('[role="menu"]') ?? null);
        }
    }, [open, focusOnOpen]);

    const close = () => {
        onClose();
        rowRef.current?.focus();
    };

    const hoverCloseTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
    const cancelHoverClose = () => {
        if (hoverCloseTimer.current) {
            clearTimeout(hoverCloseTimer.current);
            hoverCloseTimer.current = null;
        }
    };
    useEffect(() => cancelHoverClose, []);
    const handleEntryMouseLeave = () => {
        if (!open || focusOnOpen) {
            return;
        }
        cancelHoverClose();
        hoverCloseTimer.current = setTimeout(onClose, HOVER_CLOSE_DELAY_MS);
    };

    const handleRowKeyDown = (e: React.KeyboardEvent<HTMLButtonElement>) => {
        if (e.key === 'ArrowRight' && !disabled) {
            e.preventDefault();
            e.stopPropagation();
            onOpen(true);
        }
    };

    const handleMenuKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
        if (e.key === 'Escape' || (e.key === 'ArrowLeft' && !isTextField(e.target))) {
            // Before the parent Dropdown's document listener closes the whole menu.
            e.preventDefault();
            e.stopPropagation();
            close();
            return;
        }
        if (handleMenuNavigation(e)) {
            e.stopPropagation();
        }
    };

    return (
        <Entry
            onMouseEnter={cancelHoverClose}
            onMouseLeave={handleEntryMouseLeave}
        >
            <MenuItem
                ref={rowRef}
                role='menuitem'
                aria-haspopup='menu'
                aria-expanded={open}
                {...(open ? {'aria-controls': menuId} : {})}
                label={label}
                secondaryLabel={secondaryLabel}
                secondaryLabelPosition='inline'
                leadingElement={true}
                leadingVisual={(
                    <Icon
                        glyph={icon}
                        size='16'
                    />
                )}
                trailingElement={true}
                trailingVisual={(
                    <Icon
                        glyph={<ChevronRightIcon/>}
                        size='16'
                    />
                )}
                active={open}
                disabled={disabled}
                data-testid={rest['data-testid']}
                onClick={() => onOpen(true)}
                onMouseEnter={() => {
                    if (!disabled && !open) {
                        rowRef.current?.focus();
                        onOpen(false);
                    }
                }}
                onKeyDown={handleRowKeyDown}
            />
            {rendered && (
                <ChildAnchor
                    ref={anchorRef}
                    data-x={side.x}
                    data-y={side.y}
                >
                    <ChildMenu
                        id={menuId}
                        variant='child'
                        open={open}
                        onExited={onExited}
                        role='menu'
                        aria-label={menuLabel}
                        tabIndex={-1}
                        onKeyDown={handleMenuKeyDown}
                    >
                        {children}
                    </ChildMenu>
                </ChildAnchor>
            )}
        </Entry>
    );
};

const Entry = styled.div`
    position: relative;
    display: flex;
    flex-direction: column;
`;

// Beside the parent row: past the parent menu's edge, aligned with the row's
// bottom (growing upward) or, flipped, its top (growing downward).
const ChildAnchor = styled.div`
    position: absolute;
    z-index: 1;

    &[data-x='right'] {
        left: 100%;
        padding-left: var(--spacing-xxs);
    }

    &[data-x='left'] {
        right: 100%;
        padding-right: var(--spacing-xxs);
    }

    &[data-y='up'] {
        bottom: calc(-1 * var(--spacing-xs));
    }

    &[data-y='down'] {
        top: calc(-1 * var(--spacing-xs));
    }
`;

const ChildMenu = styled(PopoverMenu)`
    &:focus {
        outline: none;
    }
`;

export default ComposerSubmenu;
