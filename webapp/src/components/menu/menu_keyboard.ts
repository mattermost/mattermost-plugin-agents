// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type React from 'react';

const ITEM_SELECTOR = '[role="menuitem"], [role="menuitemcheckbox"], [role="menuitemradio"]';

// The enabled items of `menu` itself, not those of a submenu nested inside it.
export function menuItems(menu: HTMLElement): HTMLElement[] {
    return Array.from(menu.querySelectorAll<HTMLElement>(ITEM_SELECTOR)).filter((item) => (
        item.closest('[role="menu"]') === menu && !(item as HTMLButtonElement).disabled
    ));
}

// Focuses the menu's first enabled item; without one (loading, or every
// action disabled) its text field, else the menu itself.
export function focusFirstItem(menu: HTMLElement | null) {
    if (!menu) {
        return;
    }
    const [first] = menuItems(menu);
    (first ?? menu.querySelector<HTMLElement>('input:not(:disabled)') ?? menu).focus();
}

export const isTextField = (el: EventTarget | null) => el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement;

// Arrow Up/Down (wrapping), Home and End move focus between the items of the
// menu that owns the handler (`currentTarget`, the element with role="menu").
// Keys from a nested submenu are left to it. Returns whether it handled the key.
export function handleMenuNavigation(e: React.KeyboardEvent<HTMLElement>): boolean {
    const menu = e.currentTarget;
    const target = e.target as HTMLElement;
    if (target.closest('[role="menu"]') !== menu) {
        return false;
    }
    if ((e.key === 'Home' || e.key === 'End') && isTextField(target)) {
        return false;
    }
    const items = menuItems(menu);
    if (items.length === 0) {
        return false;
    }
    const index = items.indexOf(target);
    let next: HTMLElement | undefined;
    switch (e.key) {
    case 'ArrowDown':
        next = items[index < 0 || index === items.length - 1 ? 0 : index + 1];
        break;
    case 'ArrowUp':
        next = items[index <= 0 ? items.length - 1 : index - 1];
        break;
    case 'Home':
        next = items[0];
        break;
    case 'End':
        next = items[items.length - 1];
        break;
    default:
        return false;
    }
    e.preventDefault();
    next.focus();
    return true;
}
