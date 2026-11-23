// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage} from 'react-intl';
import styled from 'styled-components';

import type {Project} from '@/types/projects';

import ProjectErrorBoundary from '../project_error_boundary';
import ProjectLink from '../project_link';
import {checklistRunPath, navigateTo} from '../project_links';
import {useProjectChecklist} from '../use_project';

const MIN_PLAYBOOKS_EMBED_VERSION = 1;

// Shown when the checklist can't be embedded: its progress, and a link to it in Playbooks.
const ChecklistFallback = ({project, crashed}: {project: Project; crashed: boolean}) => {
    const lookup = useProjectChecklist(project);
    const checklist = lookup.status === 'ready' ? lookup.checklist : undefined; // eslint-disable-line no-undefined
    return (
        <Message data-testid='project-checklist-fallback'>
            {checklist && (
                <Strong data-testid='project-checklist-fallback-progress'>
                    <FormattedMessage
                        defaultMessage='{name}: {completed}/{total} done'
                        values={{name: checklist.name, completed: checklist.completed_tasks, total: checklist.total_tasks}}
                    />
                </Strong>
            )}
            {crashed ? (
                <FormattedMessage defaultMessage='The checklist could not be shown here.'/>
            ) : (
                <FormattedMessage defaultMessage='Update Playbooks to work with the checklist here.'/>
            )}
            <ProjectLink
                path={checklistRunPath(project, checklist?.url)}
                testId='project-checklist-open-in-playbooks'
            >
                <FormattedMessage defaultMessage='Open in Playbooks'/>
            </ProjectLink>
        </Message>
    );
};

// The project's checklist run, embedded from Playbooks. The run is always the project's own;
// nothing else picks it.
const ProjectChecklistTab = ({project}: {project: Project}) => {
    const runId = project.playbook_run_id;
    if (!runId) {
        return null;
    }

    const playbooks = window.MattermostPlaybooks;
    if (!playbooks || !(playbooks.version >= MIN_PLAYBOOKS_EMBED_VERSION) || !playbooks.EmbeddedChecklist) {
        return (
            <ChecklistFallback
                project={project}
                crashed={false}
            />
        );
    }

    const EmbeddedChecklist = playbooks.EmbeddedChecklist;
    return (
        <ChecklistHost data-testid='project-checklist-embed'>
            <ProjectErrorBoundary
                fallback={(
                    <ChecklistFallback
                        project={project}
                        crashed={true}
                    />
                )}
            >
                <EmbeddedChecklist
                    runId={runId}
                    channelId={project.channel_id}
                    onOpenInPlaybooks={() => navigateTo(checklistRunPath(project))}
                />
            </ProjectErrorBoundary>
        </ChecklistHost>
    );
};

export default ProjectChecklistTab;

const ChecklistHost = styled.div`
    display: flex;
    flex: 1;
    flex-direction: column;
    min-height: 0;
`;

const Message = styled.div`
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--spacing-s, 8px);
    padding: var(--spacing-xl, 24px) var(--spacing-m, 16px);
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 14px;
    line-height: 20px;
    text-align: center;
`;

const Strong = styled.span`
    color: var(--center-channel-color);
    font-weight: 600;
`;
