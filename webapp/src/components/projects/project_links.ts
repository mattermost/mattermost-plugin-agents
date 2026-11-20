// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {GlobalState} from '@mattermost/types/store';

import type {Project} from '@/types/projects';

function teamName(state: GlobalState, teamId: string): string {
    return state.entities.teams.teams[teamId]?.name ?? '';
}

// Mattermost resolves a channel id in the channel URL, so a board channel the viewer has not
// loaded yet is still reachable.
export function boardChannelPath(state: GlobalState, project: Project): string {
    const team = teamName(state, project.team_id);
    return team && project.board_channel_id ? `/${team}/channels/${project.board_channel_id}` : '';
}

export function checklistRunPath(project: Project, checklistUrl?: string): string {
    if (checklistUrl) {
        return checklistUrl;
    }
    return project.playbook_run_id ? `/playbooks/runs/${project.playbook_run_id}` : '';
}

export function navigateTo(path: string) {
    window.WebappUtils?.browserHistory?.push(path);
}
