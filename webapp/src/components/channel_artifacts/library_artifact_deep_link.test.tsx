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
        channels: {currentChannelId: ''},
        general: {config: {}, license: {}},
        users: {currentUserId: 'user', profiles: {}},
        teams: {currentTeamId: '', teams: {}},
    }, action: {type: string; id?: string}) => {
        if (action.type === 'SET_CHANNEL') {
            return {...state, channels: {currentChannelId: action.id || ''}};
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
        expect(listChannelArtifacts).toHaveBeenCalledTimes(2);
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
});
