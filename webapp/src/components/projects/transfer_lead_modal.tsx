// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useMemo, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import styled from 'styled-components';

import type {UserProfile} from '@mattermost/types/users';

import {Button} from '@mattermost/compass-ui/components/button';
import {ErrorMessage} from '@mattermost/compass-ui/components/error-message';
import {Modal} from '@mattermost/compass-ui/components/modal';
import {Select} from '@mattermost/compass-ui/components/select';

import {getChannelMemberProfiles, transferProjectLead} from '@/client';
import {AnimatedModalShell} from '@/components/animated_modal_shell';
import type {Project} from '@/types/projects';

type Props = {
    project: Project;
    onClose: () => void;
    onTransferred: (project: Project) => void;
};

const isEligible = (user: UserProfile, currentLeadId: string) => (
    user.id !== currentLeadId &&
    !user.is_bot &&
    user.delete_at === 0 &&
    !user.roles.split(' ').includes('system_guest')
);

const TransferLeadModal = ({project, onClose, onTransferred}: Props) => {
    const intl = useIntl();
    const [members, setMembers] = useState<UserProfile[] | null>(null);
    const [loadFailed, setLoadFailed] = useState(false);
    const [selected, setSelected] = useState('');
    const [pending, setPending] = useState(false);
    const [error, setError] = useState('');

    useEffect(() => {
        let current = true;
        getChannelMemberProfiles(project.channel_id).then(
            (profiles) => {
                if (current) {
                    setMembers(profiles);
                }
            },
            () => {
                if (current) {
                    setLoadFailed(true);
                }
            },
        );
        return () => {
            current = false;
        };
    }, [project.channel_id]);

    const options = useMemo(() => (members ?? []).
        filter((user) => isEligible(user, project.lead_user_id)).
        map((user) => ({value: user.id, label: `@${user.username}`})), [members, project.lead_user_id]);

    const close = () => {
        if (!pending) {
            onClose();
        }
    };

    const submit = async () => {
        setPending(true);
        setError('');
        try {
            onTransferred(await transferProjectLead(project.channel_id, selected));
            onClose();
        } catch (e) {
            setError(e instanceof Error && e.message ? e.message : intl.formatMessage({defaultMessage: 'Could not transfer the lead. Try again.'}));
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
                title={<FormattedMessage defaultMessage='Transfer project lead'/>}
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
                            disabled={pending || !selected}
                            onClick={submit}
                            data-testid='project-transfer-lead-confirm'
                        >
                            <FormattedMessage defaultMessage='Transfer lead'/>
                        </Button>
                    </>
                )}
            >
                <Body>
                    <FormattedMessage defaultMessage='The lead is who the agent acts for in check-ins and other turns nobody starts. Choose a channel member who is not a guest.'/>
                    <Select
                        label={intl.formatMessage({defaultMessage: 'New lead'})}
                        options={options}
                        value={selected}
                        onChange={setSelected}
                        disabled={pending || members === null}
                        zIndex={2100}
                    />
                    {loadFailed && (
                        <ErrorMessage message={intl.formatMessage({defaultMessage: 'Could not load the channel members.'})}/>
                    )}
                    {members !== null && options.length === 0 && (
                        <FormattedMessage defaultMessage='No other eligible members in this channel.'/>
                    )}
                    {error && <ErrorMessage message={error}/>}
                </Body>
            </Modal>
        </AnimatedModalShell>
    );
};

export default TransferLeadModal;

const Body = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-m, 12px);
    font-size: 14px;
    line-height: 20px;
`;
