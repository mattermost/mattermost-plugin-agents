// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef} from 'react';
import {CSSTransition} from 'react-transition-group';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {Button} from '@mattermost/compass-ui/components/button';
import {Modal} from '@mattermost/compass-ui/components/modal';

import {MODAL_SHEET_CLASS, MODAL_TRANSITION_MS, modalTransitionPhases} from '@/components/animated_modal_shell';

interface ConfirmationDialogProps {
    title: React.ReactNode;
    titleId?: string;
    message: React.ReactNode;
    confirmButtonText: React.ReactNode;
    cancelButtonText?: React.ReactNode;
    onConfirm: () => void;
    onCancel: () => void;
    isDestructive?: boolean;

    /** Disables buttons (e.g. while a request is in flight). */
    confirmPending?: boolean;

    /** Higher z-index for stacking over other modals (e.g. 1100 over agent config). */
    zIndex?: number;

    /**
     * When true, focuses the primary action on open, restores focus on unmount,
     * traps Tab within the dialog, closes on Escape, and on backdrop mousedown outside content.
     */
    managedAccessibility?: boolean;

    /**
     * When set, dialog mount/visibility is driven by CSSTransition (fade + sheet motion).
     * Omit to keep legacy behavior (parent mounts/unmounts the component).
     */
    show?: boolean;
}

const ConfirmationDialog: React.FC<ConfirmationDialogProps> = ({
    title,
    titleId = 'confirmation-dialog-title',
    message,
    confirmButtonText,
    cancelButtonText = <FormattedMessage defaultMessage='Cancel'/>,
    onConfirm,
    onCancel,
    isDestructive = false,
    confirmPending = false,
    zIndex = 1000,
    managedAccessibility = false,
    show,
}) => {
    const intl = useIntl();
    const transitionRef = useRef<HTMLDivElement>(null);
    const dialogRef = useRef<HTMLDivElement>(null);
    const confirmButtonRef = useRef<HTMLButtonElement>(null);
    const pendingRef = useRef(confirmPending);
    const onCancelRef = useRef(onCancel);
    pendingRef.current = confirmPending;
    onCancelRef.current = onCancel;

    const dialogMounted = typeof show === 'undefined' || show;

    useEffect(() => {
        if (!managedAccessibility || !dialogMounted) {
            return () => {
                // No focus management when using simple mode or while transition keeps dialog unmounted
            };
        }
        const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
        const focusId = window.requestAnimationFrame(() => {
            confirmButtonRef.current?.focus();
        });
        return () => {
            window.cancelAnimationFrame(focusId);
            previousFocus?.focus?.({preventScroll: true});
        };
    }, [managedAccessibility, dialogMounted]);

    useEffect(() => {
        if (!managedAccessibility || !dialogMounted) {
            return () => {
                // No keyboard trap
            };
        }
        const dialog = dialogRef.current;
        const focusableSelector = 'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])';

        const onKeyDown = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                e.preventDefault();
                if (!pendingRef.current) {
                    onCancelRef.current();
                }
                return;
            }
            if (e.key !== 'Tab' || !dialog) {
                return;
            }
            const focusables = Array.from(dialog.querySelectorAll<HTMLElement>(focusableSelector)).
                filter((el) => !el.hasAttribute('disabled') && el.offsetParent !== null);
            if (focusables.length === 0) {
                return;
            }
            const first = focusables[0];
            const last = focusables[focusables.length - 1];
            if (e.shiftKey) {
                if (document.activeElement === first) {
                    e.preventDefault();
                    last.focus();
                }
            } else if (document.activeElement !== first) {
                e.preventDefault();
                first.focus();
            }
        };

        // Capture phase so the dialog claims Escape before page-level document listeners registered earlier.
        document.addEventListener('keydown', onKeyDown, true);
        return () => document.removeEventListener('keydown', onKeyDown, true);
    }, [managedAccessibility, dialogMounted]);

    useEffect(() => {
        if (!managedAccessibility || confirmPending || !dialogMounted) {
            return () => {
                // No outside click listener while pending or in simple mode
            };
        }
        const handler = (e: MouseEvent) => {
            if (dialogRef.current && !dialogRef.current.contains(e.target as Node)) {
                onCancel();
            }
        };
        document.addEventListener('mousedown', handler);
        return () => document.removeEventListener('mousedown', handler);
    }, [managedAccessibility, confirmPending, onCancel, dialogMounted]);

    const confirmDisabled = confirmPending;
    const cancelDisabled = confirmPending;
    const backdropProps = managedAccessibility ? {} : {onClick: onCancel};

    const transitionRefProps = typeof show === 'undefined' ? {} : {ref: transitionRef};

    const dialogTree = (
        <DialogWrapper
            {...transitionRefProps}
            $zIndex={zIndex}
            {...backdropProps}
        >
            <DialogSheet
                ref={dialogRef}
                className={MODAL_SHEET_CLASS}
                onClick={(e) => e.stopPropagation()}
            >
                <DialogModal
                    title={<span id={titleId}>{title}</span>}
                    onClose={() => !confirmPending && onCancel()}
                    closeLabel={intl.formatMessage({defaultMessage: 'Close'})}
                    headerDivider={false}
                    footerDivider={false}
                    scrollable={false}
                    footer={(
                        <>
                            <Button
                                emphasis='tertiary'
                                disabled={cancelDisabled}
                                onClick={onCancel}
                            >
                                {cancelButtonText}
                            </Button>
                            <Button
                                ref={confirmButtonRef}
                                emphasis='primary'
                                destructive={isDestructive}
                                disabled={confirmDisabled}
                                onClick={onConfirm}
                            >
                                {confirmButtonText}
                            </Button>
                        </>
                    )}
                >
                    {message}
                </DialogModal>
            </DialogSheet>
        </DialogWrapper>
    );

    if (typeof show === 'undefined') {
        return dialogTree;
    }

    return (
        <CSSTransition
            nodeRef={transitionRef}
            in={show}
            timeout={MODAL_TRANSITION_MS}
            classNames='mm-ai-modal'
            unmountOnExit={true}
            mountOnEnter={true}
            appear={true}
        >
            {dialogTree}
        </CSSTransition>
    );
};

const DialogWrapper = styled.div<{$zIndex: number}>`
    ${modalTransitionPhases}
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    background-color: rgba(0, 0, 0, 0.5);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: ${(p) => p.$zIndex};
`;

const DialogSheet = styled.div`
    width: 100%;
    max-width: 512px;
`;

const DialogModal = styled(Modal)`
    && {
        width: 100%;
    }
`;

export default ConfirmationDialog;
