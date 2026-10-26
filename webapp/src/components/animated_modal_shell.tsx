// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useId, useRef} from 'react';

import {ModalOverlay} from '@mattermost/compass-ui/components/modal';

import {OPEN_POPUP_ATTRIBUTE} from './dropdown';

/**
 * Marker class callers put on their centered panel. Compass `ModalOverlay` now owns
 * the enter/exit motion, so this no longer carries any styles; it is only kept so
 * existing callers keep compiling and can drop it at their leisure.
 */
export const MODAL_SHEET_CLASS = 'mmAiModal__sheet';

const SHELL_ATTRIBUTE = 'data-mm-ai-modal-shell';

// Ignores hidden dialogs and ones mid exit animation (bootstrap drops `in`/`show` first).
const isDialogOpen = (el: HTMLElement) => {
    if (el.closest('[aria-hidden="true"]') || el.getClientRects().length === 0) {
        return false;
    }
    if (getComputedStyle(el).visibility === 'hidden') {
        return false;
    }
    const modal = el.closest('.modal.fade');
    return !modal || modal.classList.contains('in') || modal.classList.contains('show');
};

type ShellProps = {
    show: boolean;
    children: React.ReactNode;
    onBackdropClick?: () => void;

    /** Escape handler when it should differ from a backdrop click (defaults to `onBackdropClick`). */
    onEscape?: () => void;
    zIndex?: number;
};

/**
 * Compass `ModalOverlay` (scrim, portal to document.body, scroll lock, backdrop press,
 * exit fade) plus the Escape policy callers rely on. Children are expected to render a
 * compass `Modal`, which owns initial focus, Tab trap and focus restore.
 */
export const AnimatedModalShell = ({show, children, onBackdropClick, onEscape, zIndex = 2000}: ShellProps) => {
    const shellId = useId();

    const onCloseRef = useRef(onEscape ?? onBackdropClick);
    onCloseRef.current = onEscape ?? onBackdropClick;

    // Callers render `<Modal closeOnEscape={false}>` because Modal's own Escape is a
    // bubble-phase document listener, so it can't defer to a host popup menu or a host
    // (Bootstrap) dialog. Window capture so host handlers (which may swallow Escape)
    // can't pre-empt us; a dialog stacked outside this shell owns Escape instead.
    useEffect(() => {
        const onKeyDown = (e: KeyboardEvent) => {
            if (e.key !== 'Escape' || e.defaultPrevented) {
                return;
            }

            // An open popup menu (see Dropdown) takes Escape first: its own dismiss
            // handler closes the menu, and the modal stays.
            if (document.querySelector(`[${OPEN_POPUP_ATTRIBUTE}]`)) {
                return;
            }
            const hasOtherDialog = Array.from(document.querySelectorAll<HTMLElement>('[role="dialog"]')).
                some((el) => el.closest(`[${SHELL_ATTRIBUTE}="${shellId}"]`) === null && isDialogOpen(el));
            if (hasOtherDialog) {
                return;
            }
            e.stopPropagation();
            onCloseRef.current?.();
        };
        if (show) {
            window.addEventListener('keydown', onKeyDown, true);
        }
        return () => window.removeEventListener('keydown', onKeyDown, true);
    }, [show, shellId]);

    return (
        <ModalOverlay
            {...{[SHELL_ATTRIBUTE]: shellId}}
            open={show}
            zIndex={zIndex}
            onClose={onBackdropClick}
        >
            {children}
        </ModalOverlay>
    );
};
