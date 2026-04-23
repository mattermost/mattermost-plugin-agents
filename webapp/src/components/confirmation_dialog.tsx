// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

import {PrimaryButton, TertiaryButton, DestructiveButton} from './assets/buttons';

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
}) => {
    const dialogRef = useRef<HTMLDivElement>(null);
    const confirmButtonRef = useRef<HTMLButtonElement>(null);
    const pendingRef = useRef(confirmPending);
    const onCancelRef = useRef(onCancel);
    pendingRef.current = confirmPending;
    onCancelRef.current = onCancel;

    useEffect(() => {
        if (!managedAccessibility) {
            return () => {
                // No focus management when using simple mode
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
    }, [managedAccessibility]);

    useEffect(() => {
        if (!managedAccessibility) {
            return () => {
                // No keyboard trap
            };
        }
        const dialog = dialogRef.current;
        const focusableSelector = 'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])';

        const onKeyDown = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
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
            } else if (document.activeElement === last) {
                e.preventDefault();
                first.focus();
            }
        };

        document.addEventListener('keydown', onKeyDown);
        return () => document.removeEventListener('keydown', onKeyDown);
    }, [managedAccessibility]);

    useEffect(() => {
        if (!managedAccessibility || confirmPending) {
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
    }, [managedAccessibility, confirmPending, onCancel]);

    const confirmDisabled = confirmPending;
    const cancelDisabled = confirmPending;
    const backdropProps = managedAccessibility ? {} : {onClick: onCancel};

    return (
        <DialogWrapper
            $zIndex={zIndex}
            {...backdropProps}
        >
            <DialogContent
                ref={dialogRef}
                onClick={(e) => e.stopPropagation()}
                role='dialog'
                aria-modal='true'
                aria-labelledby={titleId}
            >
                <DialogHeader>
                    <DialogTitle id={titleId}>{title}</DialogTitle>
                </DialogHeader>
                <DialogBody>
                    {message}
                </DialogBody>
                <DialogFooter>
                    <TertiaryButton
                        disabled={cancelDisabled}
                        onClick={onCancel}
                    >
                        {cancelButtonText}
                    </TertiaryButton>
                    {isDestructive ? (
                        <DestructiveButton
                            ref={managedAccessibility ? confirmButtonRef : null}
                            disabled={confirmDisabled}
                            onClick={onConfirm}
                        >
                            {confirmButtonText}
                        </DestructiveButton>
                    ) : (
                        <PrimaryButton
                            ref={managedAccessibility ? confirmButtonRef : null}
                            disabled={confirmDisabled}
                            onClick={onConfirm}
                        >
                            {confirmButtonText}
                        </PrimaryButton>
                    )}
                </DialogFooter>
            </DialogContent>
        </DialogWrapper>
    );
};

const DialogWrapper = styled.div<{$zIndex: number}>`
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

const DialogContent = styled.div`
    background-color: var(--center-channel-bg);
    border-radius: 8px;
    width: 100%;
    max-width: 512px;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.12);
`;

const DialogHeader = styled.div`
    padding: 24px 24px 0;
`;

const DialogTitle = styled.h2`
    font-size: 22px;
    font-weight: 600;
    margin: 0;
    color: var(--center-channel-color);
`;

const DialogBody = styled.div`
    padding: 24px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 14px;
    line-height: 20px;
`;

const DialogFooter = styled.div`
    padding: 0 24px 24px;
    display: flex;
    justify-content: flex-end;
    gap: 12px;
`;

export default ConfirmationDialog;
