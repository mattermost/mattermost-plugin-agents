// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {useDispatch, useSelector} from 'react-redux';
import styled from 'styled-components';

import type {GlobalState} from '@mattermost/types/store';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {FlagOutlineIcon} from '@mattermost/compass-icons/components';

import {getCurrentChannelProject, getProject} from '@/selectors';

import ProjectStateBadge from './project_state_badge';
import {getProjectRHSHandle} from './project_rhs_handle';

export const isProjectChannel = (state: GlobalState, channelId: string): boolean => Boolean(getProject(state as never, channelId));

// Channel header icon: opens the Project sidebar, and shows nothing outside projects.
export const ProjectHeaderIcon = ({channel}: {channel?: {id: string}}) => {
    const intl = useIntl();
    const dispatch = useDispatch();
    const isProject = useSelector((state: GlobalState) => Boolean(channel && isProjectChannel(state, channel.id)));
    if (!isProject) {
        return null;
    }
    const label = intl.formatMessage({defaultMessage: 'Project'});
    return (
        <IconButton
            size='small'
            icon={<Icon glyph={<FlagOutlineIcon/>}/>}
            aria-label={label}
            title={label}
            onClick={() => {
                const handle = getProjectRHSHandle();
                if (handle) {
                    dispatch(handle.toggleRHSPlugin as never);
                }
            }}
            data-testid='project-header-button'
        />
    );
};

// Channel Info row: the label, with the agent's status note beneath it.
export const ProjectInfoText = () => {
    const project = useSelector((state: GlobalState) => getCurrentChannelProject(state as never));
    return (
        <InfoText data-testid='project-info-text'>
            <FormattedMessage defaultMessage='Project'/>
            {project?.status_note && <InfoNote>{project.status_note}</InfoNote>}
        </InfoText>
    );
};

export const ProjectInfoBadge = () => {
    const project = useSelector((state: GlobalState) => getCurrentChannelProject(state as never));
    return project ? <ProjectStateBadge state={project.state}/> : null;
};

const InfoText = styled.span`
    display: flex;
    flex-direction: column;
    min-width: 0;
`;

const InfoNote = styled.span`
    overflow: hidden;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    font-size: 12px;
    line-height: 16px;
    text-overflow: ellipsis;
    white-space: nowrap;
`;
