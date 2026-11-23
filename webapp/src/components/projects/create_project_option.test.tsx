// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';
import {combineReducers, createStore} from 'redux';

import {createChannelAsUser, createProject} from '@/client';
import manifest from '@/manifest';
import {pluginReducer} from '@/redux';
import {getProject} from '@/selectors';
import type {Project} from '@/types/projects';

import {ProjectCreateExtra, makeProjectChannelTypeOption, resetProjectVisibility} from './create_project_option';
import {getProjectPaneTab, isProjectPaneOpen} from './pane/pane_state';

jest.mock('@/client', () => ({
    createChannelAsUser: jest.fn(),
    createProject: jest.fn(),
}));

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => <>{defaultMessage}</>,
    };
});

const intl = {formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage} as never;

const form = {teamId: 'team1', displayName: 'Q4 login revamp', url: 'q4-login-revamp', purpose: 'Ship it'};

const project = {channel_id: 'newchannel', team_id: 'team1', state: 'active', update_at: 1} as Project;

function makeStore() {
    const store = createStore(combineReducers({
        entities: (state = {}) => state,
        ['plugins-' + manifest.id]: pluginReducer,
    }));
    store.dispatch({type: 'SET_PROJECT_CHANNELS_ENABLED', projectChannelsEnabled: true});
    return store;
}

beforeEach(() => {
    jest.resetAllMocks();
    resetProjectVisibility();
    (createChannelAsUser as jest.Mock).mockResolvedValue({id: 'newchannel', team_id: 'team1', name: form.url});
    (createProject as jest.Mock).mockResolvedValue(project);
});

describe('Project channel type option', () => {
    test.each([
        {name: 'is available when project channels are on', enabled: true},
        {name: 'is not available when project channels are off', enabled: false},
    ])('$name', ({enabled}) => {
        const option = makeProjectChannelTypeOption(makeStore() as never, intl);
        const state = {'plugins-mattermost-ai': {projectChannelsEnabled: enabled}};

        expect(option.isAvailable(state as never)).toBe(enabled);
    });

    test('creates the channel as the person, then attaches the project to it', async () => {
        const store = makeStore();
        const option = makeProjectChannelTypeOption(store as never, intl);

        const result = await option.onCreate(form);

        expect(createChannelAsUser).toHaveBeenCalledWith({team_id: 'team1', name: 'q4-login-revamp', display_name: 'Q4 login revamp', purpose: 'Ship it', type: 'O'});
        expect(createProject).toHaveBeenCalledWith(expect.objectContaining({team_id: 'team1', channel_id: 'newchannel', name: 'q4-login-revamp', display_name: 'Q4 login revamp', private: false}));
        expect(result).toEqual({status: 'created', channel: expect.objectContaining({id: 'newchannel'})});
        expect(getProject(store.getState() as never, 'newchannel')).toEqual(project);
        expect(isProjectPaneOpen(store.getState() as never, 'newchannel')).toBe(true);
        expect(getProjectPaneTab(store.getState() as never, 'newchannel')).toBe('setup');
    });

    test('a private choice in the extra content makes a private channel and project', async () => {
        const option = makeProjectChannelTypeOption(makeStore() as never, intl);
        render(<ProjectCreateExtra setCanCreate={jest.fn()}/>);

        fireEvent.click(screen.getByTestId('project-visibility-private'));
        await option.onCreate(form);

        expect(createChannelAsUser).toHaveBeenCalledWith(expect.objectContaining({type: 'P'}));
        expect(createProject).toHaveBeenCalledWith(expect.objectContaining({private: true}));
    });

    test.each([
        {name: 'holds Create back until the project has a purpose', purpose: '', want: false},
        {name: 'holds Create back for a purpose of only spaces', purpose: '   ', want: false},
        {name: 'lets Create through once the purpose is written', purpose: 'Ship the new login', want: true},
    ])('$name', ({purpose, want}) => {
        const setCanCreate = jest.fn();
        render(
            <ProjectCreateExtra
                formState={{...form, purpose}}
                setCanCreate={setCanCreate}
            />,
        );

        expect(setCanCreate).toHaveBeenLastCalledWith(want);
        expect(screen.getByTestId('project-create-purpose-hint').textContent).toContain('Purpose');
    });

    test('a channel the server refuses to create is reported, and no project is started', async () => {
        (createChannelAsUser as jest.Mock).mockRejectedValue(new Error('A channel with that name already exists.'));
        const option = makeProjectChannelTypeOption(makeStore() as never, intl);

        await expect(option.onCreate(form)).resolves.toEqual({status: 'error', message: 'A channel with that name already exists.'});
        expect(createProject).not.toHaveBeenCalled();
    });

    test('a project that cannot start is reported and says the channel exists', async () => {
        (createProject as jest.Mock).mockRejectedValue(new Error('Project channels are not available.'));
        const store = makeStore();
        const option = makeProjectChannelTypeOption(store as never, intl);

        const result = await option.onCreate(form);

        expect(result).toEqual({status: 'error', message: expect.stringContaining('Project channels are not available.')});
        expect((result as {message: string}).message).toContain('The channel was created');
        expect(getProject(store.getState() as never, 'newchannel')).toBeUndefined();
    });
});
