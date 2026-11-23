// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect} from 'react';
import {useSelector, useStore} from 'react-redux';
import type {Store, UnknownAction} from 'redux';

import type {GlobalState} from '@mattermost/types/store';

import {listProjects} from '@/client';
import {getProjectChannelsEnabled} from '@/selectors';

import {receiveProjects} from './project_state';

type WebappStore = Store<GlobalState, UnknownAction>;

// Loads the projects of a team, so channel indicators render without a request per channel.
export async function loadTeamProjects(store: WebappStore, teamId: string): Promise<void> {
    if (!teamId || !getProjectChannelsEnabled(store.getState() as never)) {
        return;
    }
    try {
        store.dispatch(receiveProjects(teamId, await listProjects(teamId)));
    } catch {
        // Indicators stay as they were; a channel's own lookup still works from the sidebar.
    }
}

export const refreshCurrentTeamProjects = (store: WebappStore) => loadTeamProjects(store, store.getState().entities.teams.currentTeamId);

// Loads the team's projects, so channel indicators and the project pane know which channels are
// projects.
const ProjectWatcher = () => {
    const store = useStore<GlobalState>();
    const enabled = useSelector((state: GlobalState) => getProjectChannelsEnabled(state as never));
    const teamId = useSelector((state: GlobalState) => state.entities.teams.currentTeamId);

    useEffect(() => {
        loadTeamProjects(store as WebappStore, teamId);
    }, [store, enabled, teamId]);

    return null;
};

export default ProjectWatcher;
