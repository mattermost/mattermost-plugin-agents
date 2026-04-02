// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

import {PrimaryButton} from '@/components/assets/buttons';

type Props = {
    agentName: string;
    confirmPending?: boolean;
    onConfirm: () => void;
    onCancel: () => void;
}

const DeleteAgentDialog = (props: Props) => {
    const dialogRef = useRef<HTMLDivElement>(null);
    const deleteButtonRef = useRef<HTMLButtonElement>(null);
    const confirmPendingRef = useRef(props.confirmPending);
    const onCancelRef = useRef(props.onCancel);
    confirmPendingRef.current = props.confirmPending;
    onCancelRef.current = props.onCancel;

    // Focus primary control on open; restore focus on unmount
    useEffect(() => {
        const previousFocus = document.activeElement instanceof HTMLElement ?
            document.activeElement :
            null;

        const focusId = window.requestAnimationFrame(() => {
            deleteButtonRef.current?.focus();
        });

        return () => {
            window.cancelAnimationFrame(focusId);
            previousFocus?.focus?.({preventScroll: true});
        };
    }, []);

    // Escape + Tab trap (use refs so handlers stay current without re-running focus effect)
    useEffect(() => {
        const dialog = dialogRef.current;
        const focusableSelector = 'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])';

        const onKeyDown = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                if (!confirmPendingRef.current) {
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
    }, []);

    // Close on click outside (disabled while confirm is in flight)
    useEffect(() => {
        if (props.confirmPending) {
            return () => {
                // No mousedown listener while pending
            };
        }
        const handler = (e: MouseEvent) => {
            if (dialogRef.current && !dialogRef.current.contains(e.target as Node)) {
                props.onCancel();
            }
        };
        document.addEventListener('mousedown', handler);
        return () => document.removeEventListener('mousedown', handler);
    }, [props.onCancel, props.confirmPending]);

    return (
        <Backdrop>
            <Dialog
                ref={dialogRef}
                role='dialog'
                aria-modal='true'
                aria-labelledby='delete-agent-dialog-title'
            >
                <DialogTitle id='delete-agent-dialog-title'>
                    <FormattedMessage defaultMessage='Delete agent'/>
                </DialogTitle>
                <DialogBody>
                    <FormattedMessage
                        defaultMessage='Are you sure you want to delete <b>{name}</b>? This action cannot be undone. The agent will be deactivated and removed from the workspace.'
                        values={{
                            name: props.agentName,
                            b: (chunks: React.ReactNode) => <strong>{chunks}</strong>,
                        }}
                    />
                </DialogBody>
                <DialogFooter>
                    <CancelButton
                        type='button'
                        disabled={props.confirmPending}
                        onClick={props.onCancel}
                    >
                        <FormattedMessage defaultMessage='Cancel'/>
                    </CancelButton>
                    <DeleteButton
                        ref={deleteButtonRef}
                        type='button'
                        disabled={props.confirmPending}
                        onClick={props.onConfirm}
                    >
                        <FormattedMessage defaultMessage='Delete'/>
                    </DeleteButton>
                </DialogFooter>
            </Dialog>
        </Backdrop>
    );
};

// --- Styled Components ---

const Backdrop = styled.div`
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    z-index: 1100;
    background: rgba(0, 0, 0, 0.5);
    display: flex;
    align-items: center;
    justify-content: center;
`;

const Dialog = styled.div`
    background: var(--center-channel-bg, #fff);
    border-radius: 8px;
    box-shadow: 0px 20px 32px rgba(0, 0, 0, 0.12);
    max-width: 480px;
    width: 100%;
    padding: 24px;
`;

const DialogTitle = styled.h2`
    font-size: 20px;
    font-weight: 600;
    color: var(--center-channel-color);
    margin: 0 0 12px 0;
`;

const DialogBody = styled.div`
    font-size: 14px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    line-height: 20px;
    margin-bottom: 24px;
`;

const DialogFooter = styled.div`
    display: flex;
    justify-content: flex-end;
    gap: 8px;
`;

const CancelButton = styled.button`
    height: 40px;
    padding: 0 20px;
    border-radius: 4px;
    border: none;
    background: rgba(var(--center-channel-color-rgb), 0.08);
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-weight: 600;
    font-size: 14px;
    cursor: pointer;

    &:hover:not(:disabled) {
        background: rgba(var(--center-channel-color-rgb), 0.12);
    }

    &:disabled {
        opacity: 0.5;
        cursor: not-allowed;
    }
`;

const DeleteButton = styled(PrimaryButton)`
    background: var(--dnd-indicator, #D24B4E);

    &&, &&:focus {
        background: var(--dnd-indicator, #D24B4E);
    }

    &&:hover:not([disabled]) {
        background: var(--dnd-indicator, #D24B4E);
    }
`;

export default DeleteAgentDialog;
