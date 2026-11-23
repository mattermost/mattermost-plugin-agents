// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render} from '@testing-library/react';
import {Provider} from 'react-redux';

import {listProjects} from '@/client';
import manifest from '@/manifest';

import ProjectWatcher from './project_watcher';

jest.mock('@/client', () => ({
    listProjects: jest.fn(),
}));

function renderWatcher({enabled = true} = {}) {
    const state = {
        entities: {
            teams: {currentTeamId: 'team1'},
            channels: {currentChannelId: 'chan1'},
        },
        ['plugins-' + manifest.id]: {
            projectChannelsEnabled: enabled,
            projects: {byChannelId: {}, notProject: {}, loadedTeams: {}},
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
});

describe('ProjectWatcher', () => {
    test('loads the team\'s projects once project channels are on, and not while they are off', () => {
        renderWatcher();
        expect(listProjects).toHaveBeenCalledWith('team1');

        (listProjects as jest.Mock).mockClear();
        renderWatcher({enabled: false});
        expect(listProjects).not.toHaveBeenCalled();
    });
});
