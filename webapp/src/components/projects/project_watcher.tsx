// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect} from 'react';
import {useDispatch, useSelector, useStore} from 'react-redux';
import type {Store, UnknownAction} from 'redux';

import type {GlobalState} from '@mattermost/types/store';

import {listProjects} from '@/client';
import manifest from '@/manifest';
import {getProject, getProjectChannelsEnabled, isKnownNotProject} from '@/selectors';

import {receiveProjects} from './project_state';
import {getProjectRHSHandle} from './project_rhs_handle';

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

type RHSView = {isSidebarOpen?: boolean; pluggableId?: string};

// Core keeps a plugin sidebar open across channel switches, so this closes the Project sidebar
// when the channel it would show is not a project. In a project channel it stays and re-targets.
const ProjectWatcher = () => {
    const dispatch = useDispatch();
    const store = useStore<GlobalState>();
    const enabled = useSelector((state: GlobalState) => getProjectChannelsEnabled(state as never));
    const teamId = useSelector((state: GlobalState) => state.entities.teams.currentTeamId);
    const channelId = useSelector((state: GlobalState) => state.entities.channels.currentChannelId);
    const isProject = useSelector((state: GlobalState) => Boolean(getProject(state as never, channelId)));
    const teamLoaded = useSelector((state: GlobalState & Record<string, any>) => Boolean(state[`plugins-${manifest.id}`]?.projects?.loadedTeams?.[teamId]));
    const knownNotProject = useSelector((state: GlobalState) => isKnownNotProject(state as never, channelId));

    useEffect(() => {
        loadTeamProjects(store as WebappStore, teamId);
    }, [store, enabled, teamId]);

    useEffect(() => {
        const handle = getProjectRHSHandle();
        const rhs = (store.getState() as {views?: {rhs?: RHSView}}).views?.rhs;
        if (!handle || !rhs?.isSidebarOpen || rhs.pluggableId !== handle.id) {
            return;
        }
        if (!enabled || (!isProject && (teamLoaded || knownNotProject))) {
            dispatch(handle.hideRHSPlugin as never);
        }
    }, [store, dispatch, enabled, channelId, isProject, teamLoaded, knownNotProject]);

    return null;
};

export default ProjectWatcher;
