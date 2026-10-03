// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef} from 'react';
import {CSSTransition} from 'react-transition-group';
import styled, {css} from 'styled-components';

/** Apply to the centered panel (card) inside the overlay so enter/exit slide + fade run on the sheet. */
export const MODAL_SHEET_CLASS = 'mmAiModal__sheet';

/**
 * Match the longest transition for CSSTransition `timeout`.
 * Mattermost GenericModal uses Bootstrap: .modal-dialog transform 0.3s ease-out; .fade opacity 0.15s linear.
 */
export const MODAL_TRANSITION_MS = 300;

/**
 * Enter/exit phases for `classNames="mm-ai-modal"` (react-transition-group).
 * Timings/motion aligned with host webapp: react-bootstrap Fade (0.15s linear) + .modal-dialog slide (0.3s ease-out, -25%).
 */
export const modalTransitionPhases = css`
    &.mm-ai-modal-enter,
    &.mm-ai-modal-appear {
        opacity: 0;
    }

    &.mm-ai-modal-enter-active,
    &.mm-ai-modal-appear-active {
        opacity: 1;
        transition: opacity 0.15s linear;
    }

    &.mm-ai-modal-exit {
        opacity: 1;
    }

    &.mm-ai-modal-exit-active {
        opacity: 0;
        transition: opacity 0.15s linear;
    }

    &.mm-ai-modal-enter .${MODAL_SHEET_CLASS},
    &.mm-ai-modal-appear .${MODAL_SHEET_CLASS} {
        opacity: 0;
        transform: translateY(-25%);
    }

    &.mm-ai-modal-enter-active .${MODAL_SHEET_CLASS},
    &.mm-ai-modal-appear-active .${MODAL_SHEET_CLASS} {
        opacity: 1;
        transform: translateY(0);
        transition: opacity 0.15s linear, transform 0.3s ease-out;
    }

    &.mm-ai-modal-exit .${MODAL_SHEET_CLASS} {
        opacity: 1;
        transform: translateY(0);
    }

    &.mm-ai-modal-exit-active .${MODAL_SHEET_CLASS} {
        opacity: 0;
        transform: translateY(-25%);
        transition: opacity 0.15s linear, transform 0.3s ease-out;
    }
`;

const ShellRoot = styled.div<{$zIndex: number}>`
    ${modalTransitionPhases}
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    background-color: rgba(0, 0, 0, 0.64);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: ${(p) => p.$zIndex};

    [role='dialog']:focus {
        outline: none;
    }
`;

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

const FOCUSABLE = 'a[href], button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])';

type ShellProps = {
    show: boolean;
    children: React.ReactNode;
    onBackdropClick?: () => void;
    zIndex?: number;
};

/**
 * Full-screen dimmed overlay with enter/exit animation (fade + sheet from top),
 * aligned with Mattermost GenericModal (Bootstrap fade + modal-dialog motion).
 */
export const AnimatedModalShell = ({show, children, onBackdropClick, zIndex = 2000}: ShellProps) => {
    const nodeRef = useRef<HTMLDivElement>(null);
    const returnFocusRef = useRef<HTMLElement | null>(null);

    const onCloseRef = useRef(onBackdropClick);
    onCloseRef.current = onBackdropClick;

    const handleEnter = () => {
        returnFocusRef.current = document.activeElement as HTMLElement | null;
        const dialog = nodeRef.current?.querySelector<HTMLElement>('[role="dialog"]');
        dialog?.setAttribute('tabindex', '-1');
        dialog?.focus();
    };

    // A host menu closing as the modal opens can restore focus to its trigger; reclaim it.
    const handleEntered = () => {
        const dialog = nodeRef.current?.querySelector<HTMLElement>('[role="dialog"]');
        if (dialog && !dialog.contains(document.activeElement)) {
            dialog.focus();
        }
    };

    // Window capture so host handlers (which may swallow Escape) can't pre-empt us;
    // a dialog stacked outside this shell owns Escape instead.
    useEffect(() => {
        const onKeyDown = (e: KeyboardEvent) => {
            if (e.key !== 'Escape' || e.defaultPrevented) {
                return;
            }
            const root = nodeRef.current;
            const hasOtherDialog = Array.from(document.querySelectorAll<HTMLElement>('[role="dialog"]')).
                some((el) => !root?.contains(el) && isDialogOpen(el));
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
    }, [show]);

    const handleExited = () => {
        returnFocusRef.current?.focus();
        returnFocusRef.current = null;
    };

    const handleKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
        if (e.key !== 'Tab') {
            return;
        }
        const focusable = Array.from(e.currentTarget.querySelectorAll<HTMLElement>(FOCUSABLE));
        if (focusable.length === 0) {
            return;
        }
        const first = focusable[0];
        const last = focusable[focusable.length - 1];
        const active = document.activeElement;
        const dialog = e.currentTarget.querySelector('[role="dialog"]');
        if (e.shiftKey && (active === first || active === dialog)) {
            e.preventDefault();
            last.focus();
        } else if (!e.shiftKey && active === last) {
            e.preventDefault();
            first.focus();
        }
    };

    return (
        <CSSTransition
            nodeRef={nodeRef}
            in={show}
            timeout={MODAL_TRANSITION_MS}
            classNames='mm-ai-modal'
            unmountOnExit={true}
            mountOnEnter={true}
            appear={true}
            onEnter={handleEnter}
            onEntered={handleEntered}
            onExited={handleExited}
        >
            <ShellRoot
                ref={nodeRef}
                $zIndex={zIndex}
                onKeyDown={handleKeyDown}
                onClick={(e) => {
                    if (e.target === e.currentTarget) {
                        onBackdropClick?.();
                    }
                }}
            >
                {children}
            </ShellRoot>
        </CSSTransition>
    );
};
