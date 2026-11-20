// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useState} from 'react';
import {FormattedMessage, IntlShape} from 'react-intl';
import styled from 'styled-components';
import type {Store, UnknownAction} from 'redux';

import type {GlobalState} from '@mattermost/types/store';

import {Radio} from '@mattermost/compass-ui/components/radio';
import {FlagOutlineIcon} from '@mattermost/compass-icons/components';

import {createChannelAsUser, createProject} from '@/client';
import {getProjectChannelsEnabled} from '@/selectors';

import {receiveProject} from './project_state';

// The slice of the host's NewChannelFormState this option reads.
export type ProjectFormState = {
    teamId: string;
    displayName: string;
    url: string;
    purpose: string;
};

type CreateResult =
    {status: 'created'; channel: unknown} |
    {status: 'error'; message: string};

type Visibility = 'O' | 'P';

// The host draws the public/private choice only for its own channel types, and passes the form
// state to the option read-only, so the choice made in extraContent is kept here for onCreate.
let visibility: Visibility = 'O';

export const ProjectCreateExtra = ({setCanCreate}: {setCanCreate: (value: boolean) => void}) => {
    const [value, setValue] = useState<Visibility>(visibility);

    useEffect(() => {
        setCanCreate(true);
    }, [setCanCreate]);

    const choose = (next: Visibility) => {
        visibility = next;
        setValue(next);
    };

    return (
        <Extra data-testid='project-create-extra'>
            <Hint>
                <FormattedMessage defaultMessage='A project channel is led by its own agent, which sets up a charter, a board and a checklist with your team.'/>
            </Hint>
            <Group>
                <Legend><FormattedMessage defaultMessage='Who can find this channel'/></Legend>
                <Radio
                    name='project-visibility'
                    checked={value === 'O'}
                    onChange={() => choose('O')}
                    data-testid='project-visibility-public'
                >
                    <FormattedMessage defaultMessage='Public: anyone on the team can join'/>
                </Radio>
                <Radio
                    name='project-visibility'
                    checked={value === 'P'}
                    onChange={() => choose('P')}
                    data-testid='project-visibility-private'
                >
                    <FormattedMessage defaultMessage='Private: only people you add can join'/>
                </Radio>
            </Group>
        </Extra>
    );
};

export const resetProjectVisibility = () => {
    visibility = 'O';
};

const errorMessage = (error: unknown, fallback: string) => (error instanceof Error && error.message ? error.message : fallback);

// The channel is created as the person, so the server applies their channel permissions, and the
// project is then attached to it.
export function makeProjectChannelTypeOption(store: Store<GlobalState, UnknownAction>, intl: IntlShape) {
    const onCreate = async (formState: ProjectFormState): Promise<CreateResult> => {
        const type = visibility;
        let channel;
        try {
            channel = await createChannelAsUser({
                team_id: formState.teamId,
                name: formState.url,
                display_name: formState.displayName,
                purpose: formState.purpose,
                type,
            });
        } catch (error) {
            return {status: 'error', message: errorMessage(error, intl.formatMessage({defaultMessage: 'Could not create the channel.'}))};
        }

        try {
            const project = await createProject({
                team_id: formState.teamId,
                channel_id: channel.id,
                name: formState.url,
                display_name: formState.displayName,
                purpose: formState.purpose,
                private: type === 'P',
            });
            store.dispatch(receiveProject(project));
        } catch (error) {
            return {
                status: 'error',
                message: errorMessage(error, intl.formatMessage({defaultMessage: 'Could not start the project.'})) + ' ' +
                    intl.formatMessage({defaultMessage: 'The channel was created, so search for it by name to continue.'}),
            };
        }
        resetProjectVisibility();
        return {status: 'created', channel};
    };

    return {
        label: <FormattedMessage defaultMessage='Project'/>,
        description: <FormattedMessage defaultMessage='A channel led by its own AI agent, with a board, a checklist and artifacts'/>,
        icon: <FlagOutlineIcon size={20}/>,
        createButtonText: <FormattedMessage defaultMessage='Create project'/>,
        isAvailable: (state: GlobalState) => getProjectChannelsEnabled(state as never),
        extraContent: ProjectCreateExtra,
        onCreate,
    };
}

const Extra = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-m, 12px);
    margin-top: var(--spacing-m, 12px);
`;

const Hint = styled.p`
    margin: 0;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 12px;
    line-height: 16px;
`;

const Group = styled.fieldset`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs, 8px);
    margin: 0;
    padding: 0;
    border: none;
`;

const Legend = styled.legend`
    padding: 0;
    margin-bottom: var(--spacing-xs, 8px);
    font-size: 14px;
    font-weight: 600;
`;
