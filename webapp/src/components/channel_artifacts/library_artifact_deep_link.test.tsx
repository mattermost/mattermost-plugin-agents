// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';
import {Provider} from 'react-redux';
import {combineReducers, createStore, type Store} from 'redux';

import {listChannelArtifacts, type ChannelArtifactEntry, type ChannelArtifactList} from '@/client';
import manifest from '@/manifest';
import {pluginReducer} from '@/redux';

import LibraryArtifactDeepLink from './library_artifact_deep_link';

jest.mock('@/client', () => ({
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
    team_id: 'teamid01234567890123456789',
    title: 'Notes',
    source_post_id: 'postid01234567890123456789',
    source_file_id: 'fileid01234567890123456789',
    source_file_name: 'notes.html',
    source_bot_user_id: 'botid012345678901234567890',
    created_by: 'userid0123456789012345678',
    create_at: 1,
    update_at: 1,
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
        jest.mocked(listChannelArtifacts).mockReset();
        window.history.replaceState({}, '', `/test/channels/off-topic?mm_ai_library_artifact=${artifactId}`);
    });

    test('opens a library link after the feature flag and channel arrive', async () => {
        const list: ChannelArtifactList = {
            entries: [entry],
            can_manage: true,
            download_enabled: true,
            render_enabled: true,
        };
        jest.mocked(listChannelArtifacts).mockResolvedValue(list);
        const store = renderLink();
        expect(listChannelArtifacts).not.toHaveBeenCalled();
        expect(window.location.search).toContain(artifactId);

        act(() => {
            store.dispatch({
                type: 'SET_CHANNEL_ARTIFACTS_ENABLED',
                channelArtifactsEnabled: true,
                channelArtifactDownloadEnabled: true,
                channelArtifactRenderingEnabled: true,
            });
        });
        expect(listChannelArtifacts).not.toHaveBeenCalled();
        expect(window.location.search).toContain(artifactId);

        act(() => {
            store.dispatch({type: 'SET_CHANNEL', id: channelId});
        });
        const viewer = await screen.findByTestId('html-artifact-fullscreen');
        expect(viewer.textContent).toBe(artifactId);
        expect(listChannelArtifacts).toHaveBeenCalledWith(channelId);
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
        expect(listChannelArtifacts).not.toHaveBeenCalled();
        expect(window.location.search).not.toContain('mm_ai_library_artifact');
    });

    test('retries a library link after the list request fails', async () => {
        const list: ChannelArtifactList = {
            entries: [entry],
            can_manage: true,
            download_enabled: true,
            render_enabled: true,
        };
        jest.mocked(listChannelArtifacts).mockRejectedValueOnce(new Error('down')).mockResolvedValue(list);
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
        expect(listChannelArtifacts).toHaveBeenCalledTimes(2);
    });

    test('shows the unavailable notice when the feature flag request fails', async () => {
        const store = renderLink();
        act(() => {
            store.dispatch({type: 'SET_CHANNEL', id: channelId});
            store.dispatch({type: 'SET_CHANNEL_ARTIFACTS_LOAD_FAILED'});
        });
        expect(await screen.findByText('This channel artifact is unavailable.')).toBeTruthy();
        expect(listChannelArtifacts).not.toHaveBeenCalled();
        expect(window.location.search).not.toContain('mm_ai_library_artifact');
        expect(screen.queryByText('Channel artifacts are disabled on this server.')).toBeNull();
    });

    test('opens a library link when a refresh fails after the feature is known', async () => {
        const list: ChannelArtifactList = {
            entries: [entry],
            can_manage: true,
            download_enabled: true,
            render_enabled: true,
        };
        jest.mocked(listChannelArtifacts).mockResolvedValue(list);
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
        expect(listChannelArtifacts).toHaveBeenCalledWith(channelId);
    });

    test('opens the same library link again after the feature flag request recovers', async () => {
        const list: ChannelArtifactList = {
            entries: [entry],
            can_manage: true,
            download_enabled: true,
            render_enabled: true,
        };
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
        jest.mocked(listChannelArtifacts).mockResolvedValue(list);
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

    test('ignores a list response after the channel changes', async () => {
        const otherChannelId = 'otherchannel012345678901234';
        let resolveFirst: ((value: ChannelArtifactList) => void) | null = null;
        jest.mocked(listChannelArtifacts).mockImplementation((id: string) => {
            if (id === channelId) {
                return new Promise((resolve) => {
                    resolveFirst = resolve;
                });
            }
            return Promise.resolve({entries: [], can_manage: false, download_enabled: true, render_enabled: true});
        });
        const store = renderLink();
        act(() => {
            store.dispatch({
                type: 'SET_CHANNEL_ARTIFACTS_ENABLED',
                channelArtifactsEnabled: true,
            });
            store.dispatch({type: 'SET_CHANNEL', id: channelId});
        });
        await waitFor(() => expect(listChannelArtifacts).toHaveBeenCalledWith(channelId));

        act(() => {
            store.dispatch({type: 'SET_CHANNEL', id: otherChannelId});
        });
        await act(async () => {
            resolveFirst?.({
                entries: [entry],
                can_manage: true,
                download_enabled: true,
                render_enabled: true,
            });
        });
        expect(screen.queryByTestId('html-artifact-fullscreen')).toBeNull();
        expect(listChannelArtifacts).toHaveBeenCalledWith(otherChannelId);
    });

    test('waits to open when the url names a channel redux has not selected', async () => {
        const townSquareId = 'townsquare012345678901234';
        jest.mocked(listChannelArtifacts).mockResolvedValue({
            entries: [entry],
            can_manage: true,
            download_enabled: true,
            render_enabled: true,
        });
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
        expect(listChannelArtifacts).not.toHaveBeenCalled();
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
        expect(listChannelArtifacts).toHaveBeenCalledTimes(1);
        expect(listChannelArtifacts).toHaveBeenCalledWith(channelId);
    });

    test('keeps the query when the url changes before a miss comes back', async () => {
        let resolveFirst: ((value: ChannelArtifactList) => void) | null = null;
        jest.mocked(listChannelArtifacts).mockImplementation(() => new Promise((resolve) => {
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
        await waitFor(() => expect(listChannelArtifacts).toHaveBeenCalledWith(channelId));

        window.history.replaceState({}, '', `/test/channels/town-square?mm_ai_library_artifact=${artifactId}`);
        await act(async () => {
            resolveFirst?.({
                entries: [],
                can_manage: false,
                download_enabled: true,
                render_enabled: true,
            });
        });
        expect(screen.queryByText('This channel artifact is unavailable.')).toBeNull();
        expect(window.location.search).toContain(artifactId);

        jest.mocked(listChannelArtifacts).mockResolvedValue({
            entries: [{...entry, channel_id: 'townsquare012345678901234'}],
            can_manage: true,
            download_enabled: true,
            render_enabled: true,
        });
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
        jest.mocked(listChannelArtifacts).mockResolvedValue({
            entries: [],
            can_manage: false,
            download_enabled: true,
            render_enabled: true,
        });
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
