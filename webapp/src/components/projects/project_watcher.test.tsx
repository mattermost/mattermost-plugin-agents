// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render} from '@testing-library/react';
import {Provider} from 'react-redux';

import {listProjects} from '@/client';
import manifest from '@/manifest';

import {setProjectRHSHandle} from './project_rhs_handle';
import ProjectWatcher from './project_watcher';

jest.mock('@/client', () => ({
    listProjects: jest.fn(),
}));

const HIDE = {type: 'HIDE_PROJECT_RHS'};

function renderWatcher({channelId = 'chan1', enabled = true, projects = {} as Record<string, unknown>, loaded = true, notProject = {} as Record<string, true>, rhsOpen = true, rhsId = 'project_rhs'} = {}) {
    const state = {
        entities: {
            teams: {currentTeamId: 'team1'},
            channels: {currentChannelId: channelId},
        },
        views: {rhs: {isSidebarOpen: rhsOpen, pluggableId: rhsId}},
        ['plugins-' + manifest.id]: {
            projectChannelsEnabled: enabled,
            projects: {byChannelId: projects, notProject, loadedTeams: loaded ? {team1: true} : {}},
        },
    };
    const store = {getState: () => state, dispatch: jest.fn(), subscribe: () => jest.fn()};
    render(
        <Provider store={store as never}>
            <ProjectWatcher/>
        </Provider>,
    );
    return store;
}

beforeEach(() => {
    jest.resetAllMocks();
    (listProjects as jest.Mock).mockResolvedValue([]);
    setProjectRHSHandle({id: 'project_rhs', showRHSPlugin: {}, hideRHSPlugin: HIDE, toggleRHSPlugin: {}});
});

afterEach(() => {
    setProjectRHSHandle(null);
});

describe('ProjectWatcher', () => {
    const project = {channel_id: 'chan1', team_id: 'team1', state: 'active', update_at: 1};

    test.each([
        {name: 'closes the sidebar in a channel that is not a project', options: {}, closes: true},
        {name: 'closes the sidebar when project channels are turned off', options: {enabled: false, projects: {chan1: project}}, closes: true},
        {name: 'closes the sidebar in a channel the server said is not a project', options: {loaded: false, notProject: {chan1: true as const}}, closes: true},
        {name: 'keeps the sidebar in a project channel', options: {projects: {chan1: project}}, closes: false},
        {name: 'keeps the sidebar while the team\'s projects are still loading', options: {loaded: false}, closes: false},
        {name: 'leaves another sidebar alone', options: {rhsId: 'something_else'}, closes: false},
        {name: 'does nothing when the sidebar is closed', options: {rhsOpen: false}, closes: false},
    ])('$name', ({options, closes}) => {
        const store = renderWatcher(options);

        if (closes) {
            expect(store.dispatch).toHaveBeenCalledWith(HIDE);
        } else {
            expect(store.dispatch).not.toHaveBeenCalledWith(HIDE);
        }
    });

    test('loads the team\'s projects once project channels are on, and not while they are off', () => {
        renderWatcher();
        expect(listProjects).toHaveBeenCalledWith('team1');

        (listProjects as jest.Mock).mockClear();
        renderWatcher({enabled: false});
        expect(listProjects).not.toHaveBeenCalled();
    });
});
