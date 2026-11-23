// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen} from '@testing-library/react';
import {Provider} from 'react-redux';
import {combineReducers, createStore} from 'redux';

import manifest from '@/manifest';
import {pluginReducer} from '@/redux';
import type {Project} from '@/types/projects';

import {getProjectPaneMode, isProjectPaneOpen, openProjectPane} from './pane_state';
import ProjectPaneHost from './project_pane_host';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        useIntl: () => ({formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage}),
    };
});

jest.mock('./project_pane', () => ({
    __esModule: true,
    default: ({channelId, controls}: {channelId: string; controls: React.ReactNode}) => (
        <div
            data-testid='mock-project-pane'
            data-channel-id={channelId}
        >
            <button data-testid='pane-button'>{'card'}</button>
            <input data-testid='pane-input'/>
            {controls}
        </div>
    ),
}));

const CHANNEL_ID = 'projectchannel01234567890';
const OTHER_PROJECT_ID = 'otherproject0123456789012';

const project = (channelId: string) => ({channel_id: channelId, team_id: 'team1', state: 'active', update_at: 1} as Project);

function renderHost({path = '/eng/channels/launch', isProject = true, open = true, enabled = true} = {}) {
    window.history.pushState({}, '', path);
    const entities = {
        users: {currentUserId: 'me'},
        channels: {currentChannelId: CHANNEL_ID, channels: {[CHANNEL_ID]: {id: CHANNEL_ID, name: 'launch', team_id: 'team1'}}},
        teams: {currentTeamId: 'team1', teams: {team1: {id: 'team1', name: 'eng'}}},
    };
    const store = createStore(combineReducers({
        entities: (state: typeof entities = entities) => state,
        ['plugins-' + manifest.id]: pluginReducer,
    }));
    store.dispatch({type: 'SET_PROJECT_CHANNELS_ENABLED', projectChannelsEnabled: enabled});
    if (isProject) {
        store.dispatch({type: 'PROJECTS_RECEIVED', teamId: 'team1', projects: [project(CHANNEL_ID), project(OTHER_PROJECT_ID)]});
    }
    if (open) {
        store.dispatch(openProjectPane(CHANNEL_ID));
    }
    const view = render(
        <Provider store={store}>
            <ProjectPaneHost/>
        </Provider>,
    );
    return {store, ...view};
}

beforeEach(() => {
    window.localStorage.clear();
    delete window.MattermostPlaybooks;
});

describe('ProjectPaneHost', () => {
    test('mounts the pane beside the channel, marked for the scoped layout', () => {
        renderHost();

        const pane = screen.getByTestId('project-pane-host');
        expect(pane.tagName).toBe('ASIDE');
        expect(pane.getAttribute('data-mm-ai-project-pane')).toBe('split');
        expect(screen.getByTestId('mock-project-pane').getAttribute('data-channel-id')).toBe(CHANNEL_ID);
        expect(screen.getByTestId('project-pane-resize-handle')).not.toBeNull();
    });

    test.each([
        {name: 'in Threads, which keeps the channel selected', options: {path: '/eng/threads'}},
        {name: 'in a channel that is not a project', options: {isProject: false}},
        {name: 'while the pane is closed', options: {open: false}},
        {name: 'while project channels are off', options: {enabled: false}},
    ])('shows nothing $name', ({options}) => {
        renderHost(options);

        expect(screen.queryByTestId('project-pane-host')).toBeNull();
        expect(document.querySelector('[data-mm-ai-project-pane]')).toBeNull();
    });

    test('expands over the channel, to the full window, and back', () => {
        const {store} = renderHost();

        fireEvent.click(screen.getByTestId('project-pane-expand'));
        expect(getProjectPaneMode(store.getState() as never)).toBe('pane');
        expect(screen.getByTestId('project-pane-host').getAttribute('data-mm-ai-project-pane')).toBe('pane');
        expect(screen.queryByTestId('project-pane-resize-handle')).toBeNull();

        fireEvent.click(screen.getByTestId('project-pane-fullscreen'));
        expect(screen.getByTestId('project-pane-host').getAttribute('data-mm-ai-project-pane')).toBe('fullscreen');

        fireEvent.click(screen.getByTestId('project-pane-split'));
        expect(screen.getByTestId('project-pane-host').getAttribute('data-mm-ai-project-pane')).toBe('split');
    });

    test('closing the pane unmounts it, which restores the stock layout', () => {
        const {store} = renderHost();

        fireEvent.click(screen.getByTestId('project-pane-close'));

        expect(isProjectPaneOpen(store.getState() as never, CHANNEL_ID)).toBe(false);
        expect(document.querySelector('[data-mm-ai-project-pane]')).toBeNull();
    });

    test('typed characters stay in the pane, while shortcuts and text fields work as usual', () => {
        renderHost();
        const seen: string[] = [];
        const listener = (e: KeyboardEvent) => seen.push((e.ctrlKey ? 'ctrl+' : '') + e.key);
        document.addEventListener('keydown', listener);

        fireEvent.keyDown(screen.getByTestId('pane-button'), {key: 'a'});
        fireEvent.keyDown(screen.getByTestId('pane-button'), {key: 'z', ctrlKey: true});
        fireEvent.keyDown(screen.getByTestId('pane-button'), {key: 'Escape'});
        fireEvent.keyDown(screen.getByTestId('pane-input'), {key: 'b'});

        document.removeEventListener('keydown', listener);
        expect(seen).toEqual(['ctrl+z', 'Escape', 'b']);
    });

    test('the resize handle widens the pane with the keyboard and remembers the width', () => {
        renderHost();
        const handle = screen.getByTestId('project-pane-resize-handle');
        const before = Number(handle.getAttribute('aria-valuenow'));

        fireEvent.keyDown(handle, {key: 'ArrowLeft'});

        expect(Number(screen.getByTestId('project-pane-resize-handle').getAttribute('aria-valuenow'))).toBe(before + 16);
        expect(window.localStorage.getItem('mm-ai.projectPane.me.width')).toBe(String(before + 16));
    });

    test('keeps Playbooks from opening its sidebar in every known project channel', () => {
        const releases: Record<string, jest.Mock> = {};
        const suppressAutoOpen = jest.fn((channelId: string) => {
            releases[channelId] = jest.fn();
            return releases[channelId];
        });
        window.MattermostPlaybooks = {version: 1, EmbeddedChecklist: () => null, suppressAutoOpen};
        const {unmount} = renderHost({open: false});

        expect(suppressAutoOpen.mock.calls.map(([id]) => id).sort()).toEqual([OTHER_PROJECT_ID, CHANNEL_ID].sort());
        unmount();
        expect(releases[CHANNEL_ID]).toHaveBeenCalled();
        expect(releases[OTHER_PROJECT_ID]).toHaveBeenCalled();
    });

    test('suppresses Playbooks\'s sidebar once Playbooks loads after this plugin', () => {
        jest.useFakeTimers();
        try {
            renderHost({open: false});
            const suppressAutoOpen = jest.fn(() => jest.fn());
            window.MattermostPlaybooks = {version: 1, EmbeddedChecklist: () => null, suppressAutoOpen};

            act(() => {
                jest.advanceTimersByTime(300);
            });

            expect(suppressAutoOpen).toHaveBeenCalledTimes(2);
        } finally {
            jest.useRealTimers();
        }
    });

    test('reopens the panes and layout the user left open', () => {
        window.localStorage.setItem('mm-ai.projectPane.me', JSON.stringify({open: {[CHANNEL_ID]: true}, tab: {[CHANNEL_ID]: 'artifacts'}, mode: 'pane'}));
        renderHost({open: false});

        expect(screen.getByTestId('project-pane-host').getAttribute('data-mm-ai-project-pane')).toBe('pane');
    });
});
