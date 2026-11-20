// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Dispatch} from 'redux';

import {ProjectMissing, ProjectReceived, ProjectRHSTabSet, ProjectsReceived} from '@/redux';
import type {Project, ProjectTab} from '@/types/projects';

export const PROJECT_UPDATED_EVENT = 'custom_mattermost-ai_project_updated';

export function isProject(value: unknown): value is Project {
    const candidate = value as Partial<Project> | null | undefined;
    return Boolean(candidate && typeof candidate.channel_id === 'string' && candidate.channel_id && typeof candidate.state === 'string');
}

// The event carries the project itself or, as the library events do, wraps it under `project`.
export function applyProjectEvent(dispatch: Dispatch, data: unknown) {
    const project = isProject(data) ? data : (data as {project?: unknown} | null | undefined)?.project;
    if (isProject(project)) {
        dispatch({type: ProjectReceived, project});
    }
}

export const receiveProject = (project: Project) => ({type: ProjectReceived, project});

export const receiveProjects = (teamId: string, projects: Project[]) => ({type: ProjectsReceived, teamId, projects});

export const markNotProject = (channelId: string) => ({type: ProjectMissing, channelId});

export const setProjectRHSTab = (tab: ProjectTab) => ({type: ProjectRHSTabSet, tab});
