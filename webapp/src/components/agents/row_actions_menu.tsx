// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';
import {
    DotsHorizontalIcon,
    PencilOutlineIcon,
    TrashCanOutlineIcon,
} from '@mattermost/compass-icons/components';

type Props = {
    ariaLabel: string;
    onEdit: () => void;
    onDelete: () => void;
    onOpenChange?: (open: boolean) => void;
};

const RowActionsMenu = ({ariaLabel, onEdit, onDelete, onOpenChange}: Props) => {
    const [open, setOpen] = useState(false);
    const menuRef = useRef<HTMLDivElement>(null);

    const updateOpen = useCallback((next: boolean) => {
        setOpen(next);
        onOpenChange?.(next);
    }, [onOpenChange]);

    useEffect(() => {
        if (!open) {
            return () => {
                // No mousedown listener while menu is closed
            };
        }
        const handler = (e: MouseEvent) => {
            if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
                updateOpen(false);
            }
        };
        document.addEventListener('mousedown', handler);
        return () => document.removeEventListener('mousedown', handler);
    }, [open, updateOpen]);

    const handleButtonClick = useCallback((e: React.MouseEvent) => {
        e.stopPropagation();
        setOpen((prev) => {
            const next = !prev;
            onOpenChange?.(next);
            return next;
        });
    }, [onOpenChange]);

    const handleEdit = useCallback((e: React.MouseEvent) => {
        e.stopPropagation();
        updateOpen(false);
        onEdit();
    }, [onEdit, updateOpen]);

    const handleDelete = useCallback((e: React.MouseEvent) => {
        e.stopPropagation();
        updateOpen(false);
        onDelete();
    }, [onDelete, updateOpen]);

    return (
        <ActionsColumn ref={menuRef}>
            <MenuButton
                type='button'
                onClick={handleButtonClick}
                aria-label={ariaLabel}
            >
                <DotsHorizontalIcon size={18}/>
            </MenuButton>
            {open && (
                <DropdownMenu>
                    <MenuItem
                        type='button'
                        onClick={handleEdit}
                    >
                        <PencilOutlineIcon size={16}/>
                        <FormattedMessage defaultMessage='Edit'/>
                    </MenuItem>
                    <MenuItemDanger
                        type='button'
                        onClick={handleDelete}
                    >
                        <TrashCanOutlineIcon size={16}/>
                        <FormattedMessage defaultMessage='Delete'/>
                    </MenuItemDanger>
                </DropdownMenu>
            )}
        </ActionsColumn>
    );
};

const ActionsColumn = styled.div`
    position: relative;
    flex-shrink: 0;
`;

const MenuButton = styled.button`
    width: 32px;
    height: 32px;
    padding: 8px;
    border: none;
    background: transparent;
    border-radius: 4px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.08);
        color: rgba(var(--center-channel-color-rgb), 0.72);
    }
`;

const DropdownMenu = styled.div`
    position: absolute;
    top: 100%;
    right: 0;
    z-index: 10;
    min-width: 160px;
    padding: 4px 0;
    margin-top: 4px;
    background: var(--center-channel-bg, #fff);
    border-radius: 4px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    box-shadow: 0px 8px 24px rgba(0, 0, 0, 0.12);
`;

const MenuItem = styled.button`
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    padding: 8px 16px;
    border: none;
    background: transparent;
    font-size: 14px;
    color: var(--center-channel-color);
    cursor: pointer;
    text-align: left;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.08);
    }
`;

const MenuItemDanger = styled(MenuItem)`
    color: var(--dnd-indicator, #D24B4E);

    &:hover {
        background: rgba(var(--dnd-indicator-rgb, 210, 75, 78), 0.08);
    }
`;

export default RowActionsMenu;
