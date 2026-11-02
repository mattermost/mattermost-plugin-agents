// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useRef} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import {Button} from '@mattermost/compass-ui/components/button';
import {Modal} from '@mattermost/compass-ui/components/modal';

import {AnimatedModalShell} from './animated_modal_shell';

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
     * When true, focuses the primary action on open. (Escape, Tab trap, focus restore and
     * backdrop dismissal are always on, via AnimatedModalShell and compass Modal.)
     */
    managedAccessibility?: boolean;

    /**
     * When set, dialog mount/visibility is driven by the shell (fade + sheet motion).
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

    // The shell stays mounted through the exit and unmounts when `show` is false (or when
    // the parent unmounts this component), so Modal's focus handling covers initial focus,
    // Tab trap and focus restore in both modes.
    return (
        <AnimatedModalShell
            show={show}
            zIndex={zIndex}
            onBackdropClick={cancelUnlessPending}
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
        </AnimatedModalShell>
    );
};

export default ConfirmationDialog;
