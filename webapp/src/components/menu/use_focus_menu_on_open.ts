// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect} from 'react';

import {focusFirstItem} from './menu_keyboard';

// Moves focus to the first item of the menu with id `menuId` whenever it opens. The Dropdown's
// focus manager moves focus in on mount, but a reopen during the exit transition keeps the menu
// mounted, so this covers that case.
export function useFocusMenuOnOpen(open: boolean, menuId: string) {
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
}
