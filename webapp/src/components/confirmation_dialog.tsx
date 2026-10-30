// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import {Button} from '@mattermost/compass-ui/components/button';
import {Modal, ModalOverlay} from '@mattermost/compass-ui/components/modal';

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
     * When true, focuses the primary action on open and closes on Escape.
     * (Tab trap, focus restore and backdrop dismissal are always on, via compass Modal/ModalOverlay.)
     */
    managedAccessibility?: boolean;

    /**
     * When set, dialog mount/visibility is driven by ModalOverlay (fade + sheet motion).
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
    show = true,
}) => {
    const intl = useIntl();
    const confirmButtonRef = useRef<HTMLButtonElement>(null);

    const cancelUnlessPending = () => {
        if (!confirmPending) {
            onCancel();
        }
    };

    const cancelRef = useRef(cancelUnlessPending);
    cancelRef.current = cancelUnlessPending;

    // Kept local instead of Modal's `closeOnEscape`: Modal listens in the bubble phase,
    // so page-level document listeners registered earlier (e.g. the agent editor's
    // "Escape goes back") would see Escape first. Capture phase claims it before them,
    // and preventDefault tells them the Escape was handled.
    useEffect(() => {
        if (!managedAccessibility || !show) {
            return () => {
                // No Escape handling in simple mode or while closing
            };
        }
        const onKeyDown = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                e.preventDefault();
                cancelRef.current();
            }
        };
        document.addEventListener('keydown', onKeyDown, true);
        return () => document.removeEventListener('keydown', onKeyDown, true);
    }, [managedAccessibility, show]);

    // ModalOverlay stays mounted through the exit and unmounts when `show` is false
    // (or when the parent unmounts this component), so Modal's focus handling covers
    // initial focus, Tab trap and focus restore in both modes.
    return (
        <ModalOverlay
            open={show}
            zIndex={zIndex}
            onClose={cancelUnlessPending}
        >
            <Modal
                open={show}
                size='small'
                title={<span id={titleId}>{title}</span>}
                onClose={cancelUnlessPending}
                closeOnEscape={false}
                {...(managedAccessibility && {initialFocusRef: confirmButtonRef})}
                closeLabel={intl.formatMessage({defaultMessage: 'Close'})}
                headerDivider={false}
                footerDivider={false}
                scrollable={false}
                footer={(
                    <>
                        <Button
                            emphasis='tertiary'
                            disabled={confirmPending}
                            onClick={onCancel}
                        >
                            {cancelButtonText}
                        </Button>
                        <Button
                            ref={confirmButtonRef}
                            emphasis='primary'
                            destructive={isDestructive}
                            disabled={confirmPending}
                            onClick={onConfirm}
                        >
                            {confirmButtonText}
                        </Button>
                    </>
                )}
            >
                {message}
            </Modal>
        </ModalOverlay>
    );
};

export default ConfirmationDialog;
