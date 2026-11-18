// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';
import {Provider} from 'react-redux';
import {combineReducers, createStore, type Store} from 'redux';

import {getChannelArtifact, listChannelArtifacts, type ChannelArtifactEntry} from '@/client';
import manifest from '@/manifest';
import {pluginReducer} from '@/redux';

import LibraryArtifactDeepLink from './library_artifact_deep_link';

jest.mock('@/client', () => ({
    getChannelArtifact: jest.fn(),
    listChannelArtifacts: jest.fn(),
}));

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => <>{defaultMessage}</>,
    };
});

jest.mock('../html_artifacts/html_artifact_fullscreen', () => {
    return function MockFullscreen(props: {fileId: string}) {
        return <div data-testid='html-artifact-fullscreen'>{props.fileId}</div>;
    };
});

const artifactId = 'abcdefghijklmnopqrstuvwxyz';
const channelId = 'channelid0123456789012345';

const entry = {
    id: artifactId,
    channel_id: channelId,
    title: 'Notes',
    source_post_id: 'postid01234567890123456789',
    source_file_id: 'fileid01234567890123456789',
    source_bot_user_id: 'botid012345678901234567890',
    created_by: 'userid0123456789012345678',
    create_at: 1,
    updated_by: 'userid0123456789012345678',
    update_at: 1,
    available: true,
    size: 12,
    sha256: 'abc',
    version: 1,
} satisfies ChannelArtifactEntry;

function renderLink(): Store {
    const entities = (state = {
        channels: {currentChannelId: '', channels: {} as Record<string, {name: string; team_id: string}>},
        general: {config: {}, license: {}},
        users: {currentUserId: 'user', profiles: {}},
        teams: {currentTeamId: '', teams: {} as Record<string, {name: string}>},
    }, action: {
        type: string;
        id?: string;
        channel?: {name: string; team_id: string};
        team?: {id: string; name: string};
    }) => {
        if (action.type === 'SET_CHANNEL') {
            const channels = action.channel && action.id ? {...state.channels.channels, [action.id]: action.channel} : state.channels.channels;
            const teams = action.team ? {...state.teams.teams, [action.team.id]: {name: action.team.name}} : state.teams.teams;
            return {
                ...state,
                channels: {currentChannelId: action.id || '', channels},
                teams: {...state.teams, teams},
            };
        }
        return state;
    };
    const store = createStore(combineReducers({
        [`plugins-${manifest.id}`]: pluginReducer,
        entities,
    } as never));
    render(
        <Provider store={store}>
            <IntlProvider locale='en'>
                <LibraryArtifactDeepLink/>
            </IntlProvider>
        </Provider>,
    );
    return store;
}

describe('LibraryArtifactDeepLink', () => {
    beforeEach(() => {
        jest.mocked(getChannelArtifact).mockReset();
        jest.mocked(listChannelArtifacts).mockReset();
        jest.mocked(listChannelArtifacts).mockResolvedValue({entries: [], can_add: true, can_edit: true, can_delete: true, max_per_channel: 50});
        window.history.replaceState({}, '', `/test/channels/off-topic?mm_ai_library_artifact=${artifactId}`);
    });

    test('opens a library link after the feature flag and channel arrive', async () => {
        jest.mocked(getChannelArtifact).mockResolvedValue(entry);
        const store = renderLink();
        expect(getChannelArtifact).not.toHaveBeenCalled();
        expect(window.location.search).toContain(artifactId);

        act(() => {
            store.dispatch({
                type: 'SET_CHANNEL_ARTIFACTS_ENABLED',
                channelArtifactsEnabled: true,
            });
        });
        expect(getChannelArtifact).not.toHaveBeenCalled();
        expect(window.location.search).toContain(artifactId);

        act(() => {
            store.dispatch({type: 'SET_CHANNEL', id: channelId});
        });
        const viewer = await screen.findByTestId('html-artifact-fullscreen');
        expect(viewer.textContent).toBe(artifactId);
        expect(getChannelArtifact).toHaveBeenCalledWith(artifactId);
        await waitFor(() => expect(window.location.search).not.toContain('mm_ai_library_artifact'));
    });

    test('reports the feature as disabled only after the flag is known', async () => {
        const store = renderLink();
        act(() => {
            store.dispatch({type: 'SET_CHANNEL', id: channelId});
        });
        expect(screen.queryByText('Channel artifacts are disabled on this server.')).toBeNull();
        expect(window.location.search).toContain(artifactId);

        act(() => {
            store.dispatch({
                type: 'SET_CHANNEL_ARTIFACTS_ENABLED',
                channelArtifactsEnabled: false,
            });
        });
        expect(await screen.findByText('Channel artifacts are disabled on this server.')).toBeTruthy();
        expect(getChannelArtifact).not.toHaveBeenCalled();
        expect(window.location.search).not.toContain('mm_ai_library_artifact');
    });

    test('retries a library link after the list request fails', async () => {
        jest.mocked(getChannelArtifact).mockRejectedValueOnce(new Error('down')).mockResolvedValue(entry);
        const store = renderLink();
        act(() => {
            store.dispatch({
                type: 'SET_CHANNEL_ARTIFACTS_ENABLED',
                channelArtifactsEnabled: true,
            });
            store.dispatch({type: 'SET_CHANNEL', id: channelId});
        });
        expect(await screen.findByText('This channel artifact is unavailable.')).toBeTruthy();

        window.history.replaceState({}, '', `/test/channels/off-topic?mm_ai_library_artifact=${artifactId}`);
        act(() => {
            window.dispatchEvent(new PopStateEvent('popstate'));
        });
        expect((await screen.findByTestId('html-artifact-fullscreen')).textContent).toBe(artifactId);
        expect(screen.queryByText('This channel artifact is unavailable.')).toBeNull();
        expect(getChannelArtifact).toHaveBeenCalledTimes(2);
    });

    test('shows the unavailable notice when the feature flag request fails', async () => {
        const store = renderLink();
        act(() => {
            store.dispatch({type: 'SET_CHANNEL', id: channelId});
            store.dispatch({type: 'SET_CHANNEL_ARTIFACTS_LOAD_FAILED'});
        });
        expect(await screen.findByText('This channel artifact is unavailable.')).toBeTruthy();
        expect(getChannelArtifact).not.toHaveBeenCalled();
        expect(window.location.search).not.toContain('mm_ai_library_artifact');
        expect(screen.queryByText('Channel artifacts are disabled on this server.')).toBeNull();
    });

    test('opens a library link when a refresh fails after the feature is known', async () => {
        jest.mocked(getChannelArtifact).mockResolvedValue(entry);
        const store = renderLink();
        act(() => {
            store.dispatch({
                type: 'SET_CHANNEL_ARTIFACTS_ENABLED',
                channelArtifactsEnabled: true,
            });
            store.dispatch({type: 'SET_CHANNEL_ARTIFACTS_LOAD_FAILED'});
            store.dispatch({type: 'SET_CHANNEL', id: channelId});
        });
        expect((await screen.findByTestId('html-artifact-fullscreen')).textContent).toBe(artifactId);
        expect(screen.queryByText('This channel artifact is unavailable.')).toBeNull();
        expect(getChannelArtifact).toHaveBeenCalledWith(artifactId);
    });

    test('opens the same library link again after the feature flag request recovers', async () => {
        const listeners: Array<(location: {search?: string}) => void> = [];
        const previous = window.WebappUtils;
        window.WebappUtils = {
            browserHistory: {
                listen: (cb: (location: {search?: string}) => void) => {
                    listeners.push(cb);
                    return () => {
                        const index = listeners.indexOf(cb);
                        if (index >= 0) {
                            listeners.splice(index, 1);
                        }
                    };
                },
                replace: (next: string) => window.history.replaceState({}, '', next),
            },
        };
        jest.mocked(getChannelArtifact).mockResolvedValue(entry);
        try {
            const store = renderLink();
            act(() => {
                store.dispatch({type: 'SET_CHANNEL', id: channelId});
                store.dispatch({type: 'SET_CHANNEL_ARTIFACTS_LOAD_FAILED'});
            });
            expect(await screen.findByText('This channel artifact is unavailable.')).toBeTruthy();

            act(() => {
                store.dispatch({
                    type: 'SET_CHANNEL_ARTIFACTS_ENABLED',
                    channelArtifactsEnabled: true,
                });
            });
            act(() => {
                listeners[listeners.length - 1]?.({search: `?mm_ai_library_artifact=${artifactId}`});
            });
            expect((await screen.findByTestId('html-artifact-fullscreen')).textContent).toBe(artifactId);
        } finally {
            window.WebappUtils = previous;
        }
    });

    test('ignores an artifact response after the channel changes', async () => {
        const otherChannelId = 'otherchannel012345678901234';
        let resolveFirst: ((value: ChannelArtifactEntry) => void) | null = null;
        jest.mocked(getChannelArtifact).mockImplementation(() => new Promise((resolve) => {
            resolveFirst = resolve;
        }));
        const store = renderLink();
        act(() => {
            store.dispatch({
                type: 'SET_CHANNEL_ARTIFACTS_ENABLED',
                channelArtifactsEnabled: true,
            });
            store.dispatch({type: 'SET_CHANNEL', id: channelId});
        });
        await waitFor(() => expect(getChannelArtifact).toHaveBeenCalledWith(artifactId));

        act(() => {
            store.dispatch({type: 'SET_CHANNEL', id: otherChannelId});
        });
        await act(async () => {
            resolveFirst?.(entry);
        });
        expect(screen.queryByTestId('html-artifact-fullscreen')).toBeNull();
    });

    test('waits to open when the url names a channel redux has not selected', async () => {
        const townSquareId = 'townsquare012345678901234';
        jest.mocked(getChannelArtifact).mockResolvedValue(entry);
        const store = renderLink();
        act(() => {
            store.dispatch({
                type: 'SET_CHANNEL_ARTIFACTS_ENABLED',
                channelArtifactsEnabled: true,
            });
            store.dispatch({
                type: 'SET_CHANNEL',
                id: townSquareId,
                channel: {name: 'town-square', team_id: 'team1'},
                team: {id: 'team1', name: 'test'},
            });
        });
        expect(getChannelArtifact).not.toHaveBeenCalled();
        expect(screen.queryByText('This channel artifact is unavailable.')).toBeNull();
        expect(window.location.search).toContain(artifactId);

        act(() => {
            store.dispatch({
                type: 'SET_CHANNEL',
                id: channelId,
                channel: {name: 'off-topic', team_id: 'team1'},
                team: {id: 'team1', name: 'test'},
            });
        });
        expect((await screen.findByTestId('html-artifact-fullscreen')).textContent).toBe(artifactId);
        expect(getChannelArtifact).toHaveBeenCalledTimes(1);
        expect(getChannelArtifact).toHaveBeenCalledWith(artifactId);
    });

    test('keeps the query when the url changes before a response for another channel comes back', async () => {
        let resolveFirst: ((value: ChannelArtifactEntry) => void) | null = null;
        jest.mocked(getChannelArtifact).mockImplementation(() => new Promise((resolve) => {
            resolveFirst = resolve;
        }));
        const store = renderLink();
        act(() => {
            store.dispatch({
                type: 'SET_CHANNEL_ARTIFACTS_ENABLED',
                channelArtifactsEnabled: true,
            });
            store.dispatch({
                type: 'SET_CHANNEL',
                id: channelId,
                channel: {name: 'off-topic', team_id: 'team1'},
                team: {id: 'team1', name: 'test'},
            });
        });
        await waitFor(() => expect(getChannelArtifact).toHaveBeenCalledWith(artifactId));

        window.history.replaceState({}, '', `/test/channels/town-square?mm_ai_library_artifact=${artifactId}`);
        await act(async () => {
            resolveFirst?.({...entry, channel_id: 'townsquare012345678901234'});
        });
        expect(screen.queryByText('This channel artifact is unavailable.')).toBeNull();
        expect(window.location.search).toContain(artifactId);

        jest.mocked(getChannelArtifact).mockResolvedValue({...entry, channel_id: 'townsquare012345678901234'});
        act(() => {
            store.dispatch({
                type: 'SET_CHANNEL',
                id: 'townsquare012345678901234',
                channel: {name: 'town-square', team_id: 'team1'},
                team: {id: 'team1', name: 'test'},
            });
        });
        expect((await screen.findByTestId('html-artifact-fullscreen')).textContent).toBe(artifactId);
    });

    test('reports unavailable when the open channel matches the url and the artifact is missing', async () => {
        jest.mocked(getChannelArtifact).mockRejectedValue({status_code: 404});
        const store = renderLink();
        act(() => {
            store.dispatch({
                type: 'SET_CHANNEL_ARTIFACTS_ENABLED',
                channelArtifactsEnabled: true,
            });
            store.dispatch({
                type: 'SET_CHANNEL',
                id: channelId,
                channel: {name: 'off-topic', team_id: 'team1'},
                team: {id: 'team1', name: 'test'},
            });
        });
        expect(await screen.findByText('This channel artifact is unavailable.')).toBeTruthy();
        expect(window.location.search).not.toContain('mm_ai_library_artifact');
    });
});
