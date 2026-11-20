// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {useDispatch} from 'react-redux';
import styled from 'styled-components';

import {Button} from '@mattermost/compass-ui/components/button';
import {ErrorMessage} from '@mattermost/compass-ui/components/error-message';

import {endProject} from '@/client';
import ConfirmationDialog from '@/components/confirmation_dialog';
import type {Project} from '@/types/projects';

import CheckInScheduleModal from './check_in_schedule_modal';
import {receiveProject} from './project_state';
import TransferLeadModal from './transfer_lead_modal';

type Props = {
    project: Project;
};

type Dialog = 'end' | 'transfer' | 'schedule' | null;

const ProjectActions = ({project}: Props) => {
    const intl = useIntl();
    const dispatch = useDispatch();
    const [dialog, setDialog] = useState<Dialog>(null);
    const [ending, setEnding] = useState(false);
    const [endError, setEndError] = useState('');

    const ended = project.state === 'ended';
    const active = project.state === 'active';

    const closeDialog = () => {
        setDialog(null);
        setEndError('');
    };

    const confirmEnd = async () => {
        setEnding(true);
        setEndError('');
        try {
            dispatch(receiveProject(await endProject(project.channel_id)));
            setDialog(null);
        } catch (e) {
            setEndError(e instanceof Error && e.message ? e.message : intl.formatMessage({defaultMessage: 'Could not end the project. Try again.'}));
        } finally {
            setEnding(false);
        }
    };

    return (
        <Row data-testid='project-actions'>
            <Button
                size='small'
                emphasis='tertiary'
                disabled={!active}
                onClick={() => setDialog('schedule')}
                data-testid='project-action-schedule'
            >
                <FormattedMessage defaultMessage='Check-in schedule'/>
            </Button>
            <Button
                size='small'
                emphasis='tertiary'
                disabled={ended}
                onClick={() => setDialog('transfer')}
                data-testid='project-action-transfer'
            >
                <FormattedMessage defaultMessage='Transfer lead'/>
            </Button>
            <Button
                size='small'
                emphasis='tertiary'
                destructive={true}
                disabled={!active}
                onClick={() => setDialog('end')}
                data-testid='project-action-end'
            >
                <FormattedMessage defaultMessage='End project'/>
            </Button>
            {dialog === 'end' && (
                <ConfirmationDialog
                    title={<FormattedMessage defaultMessage='End this project?'/>}
                    message={(
                        <>
                            <FormattedMessage defaultMessage='The project agent writes a wrap-up, then the checklist run is finished and the project channel and its board are archived. The agent stops working on this project.'/>
                            {endError && <ErrorMessage message={endError}/>}
                        </>
                    )}
                    confirmButtonText={<FormattedMessage defaultMessage='End project'/>}
                    onConfirm={confirmEnd}
                    onCancel={closeDialog}
                    isDestructive={true}
                    confirmPending={ending}
                    zIndex={2000}
                />
            )}
            {dialog === 'transfer' && (
                <TransferLeadModal
                    project={project}
                    onClose={closeDialog}
                    onTransferred={(updated) => dispatch(receiveProject(updated))}
                />
            )}
            {dialog === 'schedule' && (
                <CheckInScheduleModal
                    project={project}
                    onClose={closeDialog}
                    onUpdated={(updated) => dispatch(receiveProject(updated))}
                />
            )}
        </Row>
    );
};

export default ProjectActions;

const Row = styled.div`
    display: flex;
    flex-wrap: wrap;
    gap: var(--spacing-xs, 8px);
`;
