// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import styled from 'styled-components';

import {Button} from '@mattermost/compass-ui/components/button';
import {ErrorMessage} from '@mattermost/compass-ui/components/error-message';
import {Modal} from '@mattermost/compass-ui/components/modal';
import {Select} from '@mattermost/compass-ui/components/select';
import {TextInput} from '@mattermost/compass-ui/components/text-input';

import {updateProjectSchedule} from '@/client';
import {AnimatedModalShell} from '@/components/animated_modal_shell';
import type {Project} from '@/types/projects';

import {browserTimezone, buildCheckInCron, parseCheckInCron, type CheckInFrequency} from './check_in_schedule';

type Props = {
    project: Project;
    onClose: () => void;
    onUpdated: (project: Project) => void;
};

const CheckInScheduleModal = ({project, onClose, onUpdated}: Props) => {
    const intl = useIntl();
    const initial = parseCheckInCron(project.checkin_cron);
    const [frequency, setFrequency] = useState<CheckInFrequency>(initial.frequency);
    const [time, setTime] = useState(initial.time);
    const [timezone, setTimezone] = useState(project.checkin_timezone || browserTimezone());
    const [pending, setPending] = useState(false);
    const [error, setError] = useState('');

    const options = [
        {value: 'off', label: intl.formatMessage({defaultMessage: 'Off'})},
        {value: 'daily', label: intl.formatMessage({defaultMessage: 'Every day'})},
        {value: 'weekdays', label: intl.formatMessage({defaultMessage: 'Weekdays'})},
        ...(initial.frequency === 'custom' ? [{value: 'custom', label: intl.formatMessage({defaultMessage: 'Custom ({cron})'}, {cron: project.checkin_cron})}] : []),
    ];
    const hasTime = frequency === 'daily' || frequency === 'weekdays';
    const valid = frequency === 'off' || frequency === 'custom' || (Boolean(time) && Boolean(timezone.trim()));

    const close = () => {
        if (!pending) {
            onClose();
        }
    };

    const submit = async () => {
        setPending(true);
        setError('');
        try {
            const cron = buildCheckInCron({frequency, time}, project.checkin_cron);
            onUpdated(await updateProjectSchedule(project.channel_id, cron, cron ? timezone.trim() : ''));
            onClose();
        } catch (e) {
            setError(e instanceof Error && e.message ? e.message : intl.formatMessage({defaultMessage: 'Could not update the check-in schedule. Try again.'}));
            setPending(false);
        }
    };

    return (
        <AnimatedModalShell
            show={true}
            onBackdropClick={close}
        >
            <Modal
                open={true}
                size='small'
                title={<FormattedMessage defaultMessage='Check-in schedule'/>}
                onClose={close}
                closeOnEscape={false}
                closeLabel={intl.formatMessage({defaultMessage: 'Close'})}
                headerDivider={false}
                footerDivider={false}
                scrollable={false}
                footer={(
                    <>
                        <Button
                            emphasis='tertiary'
                            disabled={pending}
                            onClick={close}
                        >
                            <FormattedMessage defaultMessage='Cancel'/>
                        </Button>
                        <Button
                            emphasis='primary'
                            disabled={pending || !valid}
                            onClick={submit}
                            data-testid='project-schedule-save'
                        >
                            <FormattedMessage defaultMessage='Save'/>
                        </Button>
                    </>
                )}
            >
                <Body>
                    <FormattedMessage defaultMessage='On this schedule the project agent posts a short standup: what moved, what is next and what is stuck.'/>
                    <Select
                        label={intl.formatMessage({defaultMessage: 'Check in'})}
                        options={options}
                        value={frequency}
                        onChange={(value) => setFrequency(value as CheckInFrequency)}
                        disabled={pending}
                        zIndex={2100}
                    />
                    {hasTime && (
                        <>
                            <TextInput
                                type='time'
                                label={intl.formatMessage({defaultMessage: 'Time'})}
                                value={time}
                                onChange={(e) => setTime(e.target.value)}
                                disabled={pending}
                            />
                            <TextInput
                                label={intl.formatMessage({defaultMessage: 'Time zone'})}
                                value={timezone}
                                onChange={(e) => setTimezone(e.target.value)}
                                disabled={pending}
                            />
                        </>
                    )}
                    {error && <ErrorMessage message={error}/>}
                </Body>
            </Modal>
        </AnimatedModalShell>
    );
};

export default CheckInScheduleModal;

const Body = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-m, 12px);
    font-size: 14px;
    line-height: 20px;
`;
