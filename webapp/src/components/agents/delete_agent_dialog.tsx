// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

import {PrimaryButton} from '@/components/assets/buttons';

type Props = {
    agentName: string;
    onConfirm: () => void;
    onCancel: () => void;
}

const DeleteAgentDialog = (props: Props) => {
    const dialogRef = useRef<HTMLDivElement>(null);

    // Close on Escape key
    useEffect(() => {
        const handler = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                props.onCancel();
            }
        };
        document.addEventListener('keydown', handler);
        return () => document.removeEventListener('keydown', handler);
    }, [props.onCancel]);

    // Close on click outside
    useEffect(() => {
        const handler = (e: MouseEvent) => {
            if (dialogRef.current && !dialogRef.current.contains(e.target as Node)) {
                props.onCancel();
            }
        };
        document.addEventListener('mousedown', handler);
        return () => document.removeEventListener('mousedown', handler);
    }, [props.onCancel]);

    return (
        <Backdrop>
            <Dialog ref={dialogRef}>
                <DialogTitle>
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
                    <CancelButton onClick={props.onCancel}>
                        <FormattedMessage defaultMessage='Cancel'/>
                    </CancelButton>
                    <DeleteButton onClick={props.onConfirm}>
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

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.12);
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
