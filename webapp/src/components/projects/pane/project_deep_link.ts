// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect} from 'react';
import {useDispatch, useSelector} from 'react-redux';

import type {GlobalState} from '@mattermost/types/store';

import {getProjectChannelsEnabled} from '@/selectors';
import type {ProjectTab} from '@/types/projects';

import {historyPathWithoutBasename} from '../../channel_artifacts/bookmark';
import {useProject} from '../use_project';

import {isChannelRoute, useBrowserLocation} from './browser_location';
import {isProjectTab, openProjectPane} from './pane_state';

export const PROJECT_PANE_QUERY = 'mm_ai_project';

export function parseProjectPaneTab(search: string): ProjectTab | null {
    const value = new URLSearchParams(search.startsWith('?') ? search.slice(1) : search).get(PROJECT_PANE_QUERY);
    return isProjectTab(value) ? value : null;
}

export function hasProjectPaneQuery(search: string): boolean {
    return new URLSearchParams(search.startsWith('?') ? search.slice(1) : search).has(PROJECT_PANE_QUERY);
}

export function stripProjectPaneQuery(pathname: string, search: string, hash = ''): string {
    const params = new URLSearchParams(search.startsWith('?') ? search.slice(1) : search);
    params.delete(PROJECT_PANE_QUERY);
    const next = params.toString();
    return pathname + (next ? `?${next}` : '') + hash;
}

function stripQuery() {
    const {pathname, search, hash} = window.location;
    const next = stripProjectPaneQuery(pathname, search, hash);
    if (next === pathname + search + hash) {
        return;
    }
    const history = window.WebappUtils?.browserHistory;
    if (history?.replace) {
        history.replace(historyPathWithoutBasename(next));
    } else {
        window.history.replaceState(window.history.state, '', next);
    }
}

// Root listener: a project channel link with ?mm_ai_project=<tab> (the board and checklist
// bookmarks) opens the project pane on that tab, then drops the query so a reload doesn't reopen
// it. The link only picks a tab of the project of the channel it names: nothing else is read
// from the URL. It waits until the URL and the store agree on the channel, and until the channel
// is known to be a project or not.
const ProjectDeepLink = () => {
    const dispatch = useDispatch();
    const location = useBrowserLocation();
    const enabled = useSelector((state: GlobalState) => getProjectChannelsEnabled(state as never));
    const channelId = useSelector((state: GlobalState) => state.entities.channels.currentChannelId);
    const channelName = useSelector((state: GlobalState) => state.entities.channels.channels[channelId]?.name ?? '');
    const teamName = useSelector((state: GlobalState) => {
        const teamId = state.entities.channels.channels[channelId]?.team_id || state.entities.teams.currentTeamId;
        return state.entities.teams.teams[teamId]?.name ?? '';
    });
    const linked = hasProjectPaneQuery(location.search);
    const onChannel = isChannelRoute(location.pathname, teamName, channelName);
    const lookup = useProject(enabled && linked && onChannel ? channelId : '');

    useEffect(() => {
        if (!linked || !enabled || !onChannel) {
            return;
        }
        if (lookup.status === 'loading') {
            return;
        }
        const tab = parseProjectPaneTab(location.search);
        if (lookup.status === 'ready' && tab) {
            dispatch(openProjectPane(channelId, tab));
        }
        stripQuery();
    }, [linked, enabled, onChannel, lookup.status, location.search, channelId, dispatch]);

    return null;
};

export default ProjectDeepLink;
