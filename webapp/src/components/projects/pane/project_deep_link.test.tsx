// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, waitFor} from '@testing-library/react';
import {Provider} from 'react-redux';
import {combineReducers, createStore} from 'redux';

import {getProject} from '@/client';
import manifest from '@/manifest';
import {pluginReducer} from '@/redux';
import type {Project} from '@/types/projects';

import {getProjectPaneTab, isProjectPaneOpen} from './pane_state';
import ProjectDeepLink, {parseProjectPaneTab, stripProjectPaneQuery} from './project_deep_link';

jest.mock('@/client', () => ({
    getProject: jest.fn(),
}));

const CHANNEL_ID = 'projectchannel01234567890';

function renderLink({path, isProject = true}: {path: string; isProject?: boolean}) {
    window.history.pushState({}, '', path);
    const replace = jest.fn((next: string) => window.history.replaceState({}, '', next));
    window.WebappUtils = {browserHistory: {push: jest.fn(), replace}} as never;
    const entities = {
        channels: {currentChannelId: CHANNEL_ID, channels: {[CHANNEL_ID]: {id: CHANNEL_ID, name: 'launch', team_id: 'team1'}}},
        teams: {currentTeamId: 'team1', teams: {team1: {id: 'team1', name: 'eng'}}},
    };
    const store = createStore(combineReducers({
        entities: (state: typeof entities = entities) => state,
        ['plugins-' + manifest.id]: pluginReducer,
    }));
    store.dispatch({type: 'SET_PROJECT_CHANNELS_ENABLED', projectChannelsEnabled: true});
    if (isProject) {
        store.dispatch({type: 'PROJECT_RECEIVED', project: {channel_id: CHANNEL_ID, team_id: 'team1', state: 'active', update_at: 1} as Project});
    } else {
        store.dispatch({type: 'PROJECT_MISSING', channelId: CHANNEL_ID});
    }
    render(
        <Provider store={store}>
            <ProjectDeepLink/>
        </Provider>,
    );
    return {store, replace};
}

beforeEach(() => {
    jest.resetAllMocks();
});

describe('ProjectDeepLink', () => {
    test('a board link opens the project pane on its Board tab and drops the query', async () => {
        const {store, replace} = renderLink({path: '/eng/channels/launch?mm_ai_project=board&keep=1'});

        await waitFor(() => expect(isProjectPaneOpen(store.getState() as never, CHANNEL_ID)).toBe(true));
        expect(getProjectPaneTab(store.getState() as never, CHANNEL_ID)).toBe('board');
        expect(replace).toHaveBeenCalledWith('/eng/channels/launch?keep=1');
    });

    test('waits while the URL still names another channel', () => {
        const {store, replace} = renderLink({path: '/eng/channels/other?mm_ai_project=checklist'});

        expect(isProjectPaneOpen(store.getState() as never, CHANNEL_ID)).toBe(false);
        expect(replace).not.toHaveBeenCalled();
    });

    test('a link to a channel that is not a project only drops the query', async () => {
        const {store, replace} = renderLink({path: '/eng/channels/launch?mm_ai_project=board', isProject: false});

        await waitFor(() => expect(replace).toHaveBeenCalledWith('/eng/channels/launch'));
        expect(isProjectPaneOpen(store.getState() as never, CHANNEL_ID)).toBe(false);
        expect(getProject).not.toHaveBeenCalled();
    });

    test('an unknown tab only drops the query', async () => {
        const {store, replace} = renderLink({path: '/eng/channels/launch?mm_ai_project=settings'});

        await waitFor(() => expect(replace).toHaveBeenCalledWith('/eng/channels/launch'));
        expect(isProjectPaneOpen(store.getState() as never, CHANNEL_ID)).toBe(false);
    });
});

describe('project pane query', () => {
    test.each([
        {search: '?mm_ai_project=board', want: 'board'},
        {search: '?mm_ai_project=checklist', want: 'checklist'},
        {search: 'mm_ai_project=setup', want: 'setup'},
        {search: '?mm_ai_project=artifacts', want: 'artifacts'},
        {search: '?mm_ai_project=../../x', want: null},
        {search: '', want: null},
    ])('parses $search', ({search, want}) => {
        expect(parseProjectPaneTab(search)).toBe(want);
    });

    test('strips only its own parameter', () => {
        expect(stripProjectPaneQuery('/eng/channels/launch', '?a=1&mm_ai_project=board', '#x')).toBe('/eng/channels/launch?a=1#x');
    });
});
