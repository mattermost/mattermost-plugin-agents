// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {ComponentProps, useState} from 'react';
import styled from 'styled-components';

import {useUpdateEffect} from 'react-use';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton, type IconButtonPadding, type IconButtonSize} from '@mattermost/compass-ui/components/icon-button';
import {MenuItem} from '@mattermost/compass-ui/components/menu-item';
import {PopoverMenu} from '@mattermost/compass-ui/components/popover-menu';

import Dropdown from './dropdown';

// Base for custom text-and-icon triggers passed as `dotMenuButton`.
export const DotMenuButton = styled.div<{$isActive: boolean}>`
    display: inline-flex;
    padding: 0;
    border: none;
    border-radius: 4px;
    width: 28px;
    height: 28px;
    align-items: center;
    justify-content: center;
    fill: rgba(var(--center-channel-color-rgb), 0.56);
    cursor: pointer;

    color: ${(props) => (props.$isActive ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.56)')};
    background-color: ${(props) => (props.$isActive ? 'rgba(var(--button-bg-rgb), 0.08)' : 'transparent')};

    &:hover {
        color: ${(props) => (props.$isActive ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.56)')};
        background-color: ${(props) => (props.$isActive ? 'rgba(var(--button-bg-rgb), 0.08)' : 'rgba(var(--center-channel-color-rgb), 0.08)')};
    }
`;

export const DropdownMenu = PopoverMenu;

type DotMenuProps = {
    children: React.ReactNode;
    icon: React.ReactNode;
    dotMenuButton?: React.ReactNode;
    dropdownMenu?: React.ReactNode;
    title?: string;
    disabled?: boolean;
    className?: string;
    isActive?: boolean;
    onOpenChange?: (isOpen: boolean) => void;
    closeOnClick?: boolean;
    testId?: string;
    size?: IconButtonSize;
    padding?: IconButtonPadding;
};

type DropdownProps = Omit<ComponentProps<typeof Dropdown>, 'target' | 'children' | 'isOpen'>;

const DotMenu = ({
    children,
    icon,
    title,
    className,
    disabled,
    isActive,
    closeOnClick = true,
    dotMenuButton,
    dropdownMenu,
    onOpenChange,
    testId,
    size = 'small',
    padding = 'default',
    ...props
}: DotMenuProps & DropdownProps) => {
    const [isOpen, setOpen] = useState(false);
    const toggleOpen = () => {
        setOpen(!isOpen);
    };
    useUpdateEffect(() => {
        onOpenChange?.(isOpen);
    }, [isOpen]);

    const Menu = dropdownMenu || DropdownMenu;
    const active = (isActive ?? false) || isOpen;
    const handleClick = (e: React.MouseEvent) => {
        e.preventDefault();
        e.stopPropagation();
        toggleOpen();
    };

    let button;
    if (dotMenuButton) {
        const MenuButton = dotMenuButton;
        button = (

            // @ts-ignore
            <MenuButton
                title={title}
                $isActive={active}
                onClick={handleClick}
                onKeyDown={(e: KeyboardEvent) => {
                    // Handle Enter and Space as clicking on the button. The
                    // spacebar's KeyboardEvent.key is ' ' (a single space).
                    if (e.key === ' ' || e.key === 'Enter') {
                        if (e.key === ' ') {
                            // Space would otherwise also scroll the page; Enter
                            // needs no preventDefault on a div.
                            e.preventDefault();
                        }
                        e.stopPropagation();
                        toggleOpen();
                    }
                }}
                tabIndex={0}
                className={className}
                role={'button'}
                disabled={disabled ?? false}
                data-testid={testId}
            >
                {icon}
            </MenuButton>
        );
    } else {
        button = (
            <IconButton
                icon={<Icon glyph={icon}/>}
                aria-label={title}
                title={title}
                size={size}
                padding={padding}
                active={active}
                onClick={handleClick}
                className={className}
                disabled={disabled ?? false}
                data-testid={testId}
            />
        );
    }

    const menu = (

        // @ts-ignore
        <Menu
            data-testid='dropdownmenu'
            onClick={(e: React.MouseEvent) => {
                e.stopPropagation();
                if (closeOnClick) {
                    setOpen(false);
                }
            }}
        >
            {children}
        </Menu>
    );

    return (
        <Dropdown
            {...props}
            isOpen={isOpen}
            onOpenChange={setOpen}
            target={button}
        >
            {menu}
        </Dropdown>
    );
};

type DropdownMenuItemProps = {
    label: React.ReactNode;

    /** An icon glyph, sized by the menu item. */
    icon?: React.ReactNode;

    /** A pre-sized leading node such as an avatar; takes precedence over `icon`. */
    leading?: React.ReactNode;

    /** Shows the menu item's check mark. */
    selected?: boolean;
    onClick?: (e: React.MouseEvent) => void;
    className?: string;
    destructive?: boolean;
    disabled?: boolean;
};

export const DropdownMenuItem = (props: DropdownMenuItemProps) => {
    const leading = props.leading ?? (props.icon && <Icon glyph={props.icon}/>);
    return (
        <MenuItem
            label={props.label}
            leadingElement={Boolean(leading)}
            leadingVisual={leading}
            trailingElement={Boolean(props.selected)}
            onClick={props.onClick}
            className={props.className}
            destructive={props.destructive}
            disabled={props.disabled}
        />
    );
};

export default DotMenu;
