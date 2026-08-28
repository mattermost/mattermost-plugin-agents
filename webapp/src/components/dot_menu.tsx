// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {ComponentProps, useState} from 'react';
import styled, {css} from 'styled-components';

import {useUpdateEffect} from 'react-use';

import Dropdown from './dropdown';

export const DotMenuButton = styled.div<{isActive: boolean}>`
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

    color: ${(props) => (props.isActive ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.56)')};
    background-color: ${(props) => (props.isActive ? 'rgba(var(--button-bg-rgb), 0.08)' : 'transparent')};

    &:hover {
        color: ${(props) => (props.isActive ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.56)')};
        background-color: ${(props) => (props.isActive ? 'rgba(var(--button-bg-rgb), 0.08)' : 'rgba(var(--center-channel-color-rgb), 0.08)')};
    }
`;

export const DropdownMenu = styled.div`
    display: flex;
    flex-direction: column;

    width: max-content;
    min-width: 16rem;
    text-align: left;
    list-style: none;

    padding: 10px 0;
    font-family: Open Sans;
    font-style: normal;
    font-weight: normal;
    font-size: 14px;
    line-height: 20px;
    color: var(--center-channel-color);

    background: var(--center-channel-bg);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    box-shadow: 0px 8px 24px rgba(0, 0, 0, 0.12);
    border-radius: 4px;
`;

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
    const MenuButton = dotMenuButton || DotMenuButton;

    const button = (

        // @ts-ignore
        <MenuButton
            title={title}
            isActive={(isActive ?? false) || isOpen}
            onClick={(e: MouseEvent) => {
                e.preventDefault();
                e.stopPropagation();
                toggleOpen();
            }}
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

export const DropdownMenuItemStyled = styled.a`
 && {
    font-family: 'Open Sans';
    font-style: normal;
    font-weight: normal;
    font-size: 14px;
    color: var(--center-channel-color);
    padding: 10px 20px;
    text-decoration: unset;
    display: inline-flex;
    align-items: center;

    >.icon {
        margin-right: 8px;
    }

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.08);
        color: var(--center-channel-color);
    }
    &&:focus {
        text-decoration: none;
        color: inherit;
  }
}
`;

export const DisabledDropdownMenuItemStyled = styled.div`
 && {
    cursor: default;
    font-family: 'Open Sans';
    font-style: normal;
    font-weight: normal;
    font-size: 14px;
    color: var(--center-channel-color-40);
    padding: 8px 20px;
    text-decoration: unset;
}
`;

export const iconSplitStyling = css`
    display: flex;
    align-items: center;
    gap: 8px;
`;

export const DropdownMenuItem = (props: { children: React.ReactNode, onClick?: (e: React.MouseEvent) => void, className?: string}) => {
    return (
        <DropdownMenuItemStyled
            href='#'
            onClick={props.onClick}
            className={props.className}
            role={'button'}

            // Prevent trigger icon (parent) from propagating title prop to options
            // Menu items use to be full text (not just icons) so don't need title
            title=''
        >
            {props.children}
        </DropdownMenuItemStyled>
    );
};

// Alternate dot menu button. Use `dotMenuButton={TitleButton}` for this style.
export const TitleButton = styled.div<{isActive: boolean}>`
    padding: 2px 2px 2px 6px;
    display: inline-flex;
    border-radius: 4px;
    color: ${({isActive}) => (isActive ? 'var(--button-bg)' : 'var(--center-channel-color)')};
    background: ${({isActive}) => (isActive ? 'rgba(var(--button-bg-rgb), 0.08)' : 'auto')};

    min-width: 0;

    &:hover {
        background: ${({isActive}) => (isActive ? 'rgba(var(--button-bg-rgb), 0.08)' : 'rgba(var(--center-channel-color-rgb), 0.08)')};
    }
`;

export default DotMenu;
