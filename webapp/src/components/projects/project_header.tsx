// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {useDispatch, useSelector} from 'react-redux';
import styled from 'styled-components';

import type {GlobalState} from '@mattermost/types/store';

import {ProgressBar} from '@mattermost/compass-ui/components/progress-bar';

import {getProfilesByIds} from '@/client';
import type {Project} from '@/types/projects';

import ProjectActions from './project_actions';
import ProjectLink from './project_link';
import {checklistRunPath} from './project_links';
import ProjectStateBadge from './project_state_badge';
import {useProjectChecklist} from './use_project';

const STATUS_NOTE_COLLAPSED_LENGTH = 180;

function useUsername(userId: string): string {
    const dispatch = useDispatch();
    const username = useSelector((state: GlobalState) => state.entities.users.profiles[userId]?.username ?? '');

    useEffect(() => {
        if (!userId || username) {
            return;
        }
        getProfilesByIds([userId]).then(
            (profiles) => dispatch({type: 'RECEIVED_PROFILES_LIST', data: profiles}),
            () => { /* the lead line is optional */ },
        );
    }, [userId, username, dispatch]);

    return username;
}

const ChecklistProgress = ({project}: {project: Project}) => {
    const intl = useIntl();
    const lookup = useProjectChecklist(project);

    if (lookup.status === 'none') {
        return (
            <Muted data-testid='project-checklist-none'>
                <FormattedMessage defaultMessage='No checklist yet.'/>
            </Muted>
        );
    }
    if (lookup.status === 'loading') {
        return (
            <Muted data-testid='project-checklist-loading'>
                <FormattedMessage defaultMessage='Loading checklist…'/>
            </Muted>
        );
    }

    const checklist = lookup.checklist;
    const link = (
        <ProjectLink
            path={checklistRunPath(project, checklist?.url)}
            testId='project-checklist-link'
        >
            <FormattedMessage defaultMessage='Open checklist'/>
        </ProjectLink>
    );

    if (!checklist) {
        return (
            <ChecklistRow data-testid='project-checklist-unavailable'>
                <Muted><FormattedMessage defaultMessage='Checklist progress is not available.'/></Muted>
                {link}
            </ChecklistRow>
        );
    }

    const percent = checklist.total_tasks > 0 ? Math.round((checklist.completed_tasks / checklist.total_tasks) * 100) : 0;
    return (
        <ChecklistBlock data-testid='project-checklist-progress'>
            <ChecklistRow>
                <Strong data-testid='project-checklist-count'>
                    <FormattedMessage
                        defaultMessage='{completed}/{total} done'
                        values={{completed: checklist.completed_tasks, total: checklist.total_tasks}}
                    />
                </Strong>
                {link}
            </ChecklistRow>
            <ProgressBar
                size='small'
                value={percent}
                aria-label={intl.formatMessage({defaultMessage: 'Checklist progress'})}
            />
        </ChecklistBlock>
    );
};

const StatusNote = ({note}: {note: string}) => {
    const [expanded, setExpanded] = useState(false);
    if (!note) {
        return (
            <Muted data-testid='project-status-note-empty'>
                <FormattedMessage defaultMessage='The agent has not posted a status update yet.'/>
            </Muted>
        );
    }
    const long = note.length > STATUS_NOTE_COLLAPSED_LENGTH;
    return (
        <div>
            <Note
                $collapsed={long && !expanded}
                data-testid='project-status-note'
            >
                {note}
            </Note>
            {long && (
                <ToggleButton
                    type='button'
                    onClick={() => setExpanded(!expanded)}
                    aria-expanded={expanded}
                    data-testid='project-status-note-toggle'
                >
                    {expanded ? <FormattedMessage defaultMessage='Show less'/> : <FormattedMessage defaultMessage='Show more'/>}
                </ToggleButton>
            )}
        </div>
    );
};

const ProjectHeader = ({project}: {project: Project}) => {
    const lead = useUsername(project.lead_user_id);

    return (
        <Container data-testid='project-header'>
            <TitleRow>
                <AgentName data-testid='project-agent-name'>{project.agent_display_name || project.agent_username}</AgentName>
                <ProjectStateBadge state={project.state}/>
            </TitleRow>
            {lead && (
                <Muted data-testid='project-lead'>
                    <FormattedMessage
                        defaultMessage='Lead: @{username}'
                        values={{username: lead}}
                    />
                </Muted>
            )}
            <StatusNote note={project.status_note}/>
            <ChecklistProgress project={project}/>
            <ProjectActions project={project}/>
        </Container>
    );
};

export default ProjectHeader;

const Container = styled.div`
    display: flex;
    flex: none;
    flex-direction: column;
    gap: var(--spacing-s, 8px);
    padding: var(--spacing-m, 12px) var(--spacing-l, 16px);
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
`;

const TitleRow = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-xs, 8px);
`;

const AgentName = styled.h3`
    min-width: 0;
    margin: 0;
    overflow: hidden;
    color: var(--center-channel-color);
    font-size: 16px;
    font-weight: 600;
    line-height: 24px;
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const Muted = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.64);
    font-size: 12px;
    line-height: 16px;
`;

const Strong = styled.span`
    color: var(--center-channel-color);
    font-size: 13px;
    font-weight: 600;
`;

const Note = styled.div<{$collapsed: boolean}>`
    color: var(--center-channel-color);
    font-size: 13px;
    line-height: 18px;
    white-space: pre-wrap;
    overflow-wrap: anywhere;

    ${(props) => props.$collapsed && `
        display: -webkit-box;
        -webkit-box-orient: vertical;
        -webkit-line-clamp: 3;
        overflow: hidden;
    `}
`;

const ToggleButton = styled.button`
    padding: 0;
    border: none;
    background: none;
    color: var(--button-bg);
    cursor: pointer;
    font-size: 12px;
    font-weight: 600;
`;

const ChecklistBlock = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs, 4px);
`;

const ChecklistRow = styled.div`
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--spacing-xs, 8px);
    font-size: 12px;
`;
