// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage} from 'react-intl';

import ConfirmationDialog from '@/components/confirmation_dialog';

type Props = {
    agentName: string;
    confirmPending?: boolean;
    onConfirm: () => void;
    onCancel: () => void;
}

const DeleteAgentDialog = (props: Props) => {
    return (
        <ConfirmationDialog
            titleId='delete-agent-dialog-title'
            title={<FormattedMessage defaultMessage='Delete agent'/>}
            message={(
                <FormattedMessage
                    defaultMessage='Are you sure you want to delete <b>{name}</b>? This action cannot be undone. The agent will be deactivated and removed from the workspace.'
                    values={{
                        name: props.agentName,
                        b: (chunks: React.ReactNode) => <strong>{chunks}</strong>,
                    }}
                />
            )}
            confirmButtonText={<FormattedMessage defaultMessage='Delete'/>}
            onConfirm={props.onConfirm}
            onCancel={props.onCancel}
            isDestructive={true}
            confirmPending={props.confirmPending}
            managedAccessibility={true}
            zIndex={1100}
        />
    );
};

export default DeleteAgentDialog;
