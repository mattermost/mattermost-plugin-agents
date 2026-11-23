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

// toAppPath turns a link to this server into the in-app path the host router takes, and leaves
// any other link as it is.
export function toAppPath(link: string): string {
    if (link.startsWith('/') && !link.startsWith('//')) {
        return link;
    }
    try {
        const url = new URL(link);
        const base = window.basename || '';
        if (url.origin !== window.location.origin || (base && url.pathname !== base && !url.pathname.startsWith(`${base}/`))) {
            return link;
        }
        return (url.pathname.slice(base.length) || '/') + url.search + url.hash;
    } catch {
        return link;
    }
}

// navigateTo routes path through the host's router, and reports false when there is none so the
// caller can let the browser follow the link.
export function navigateTo(path: string): boolean {
    const history = window.WebappUtils?.browserHistory;
    if (!history?.push) {
        return false;
    }
    history.push(path);
    return true;
}
