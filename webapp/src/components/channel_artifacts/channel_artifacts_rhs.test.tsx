// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';
import {Provider} from 'react-redux';
import {combineReducers, createStore, type Store} from 'redux';

import {listChannelArtifacts, patchChannelArtifact} from '@/client';
import manifest from '@/manifest';
import {pluginReducer} from '@/redux';

import {syncLibraryArtifactBookmarkTitle} from './bookmark';
import ChannelArtifactsRHS from './channel_artifacts_rhs';

jest.mock('@/client', () => ({
    listChannelArtifacts: jest.fn(),
    patchChannelArtifact: jest.fn(),
    deleteChannelArtifact: jest.fn(),
}));

jest.mock('./bookmark', () => ({
    syncLibraryArtifactBookmarkTitle: jest.fn(),
}));

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
        }),
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => <>{defaultMessage}</>,
    };
});

jest.mock('../html_artifacts/html_artifact_fullscreen', () => {
    return function MockFullscreen() {
        return null;
    };
});

const channelID = 'channelid0123456789012345';
const otherChannelID = 'channelid0123456789012346';

function renderRHS(): Store {
    const entities = (state = {
        channels: {currentChannelId: channelID},
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
    store.dispatch({
        type: 'SET_CHANNEL_ARTIFACTS_ENABLED',
        channelArtifactsEnabled: true,
    });
    render(
        <Provider store={store}>
            <IntlProvider locale='en'>
                <ChannelArtifactsRHS/>
            </IntlProvider>
        </Provider>,
    );
    return store;
}

describe('ChannelArtifactsRHS', () => {
    beforeEach(() => {
        jest.mocked(listChannelArtifacts).mockReset();
        jest.mocked(patchChannelArtifact).mockReset();
        jest.mocked(syncLibraryArtifactBookmarkTitle).mockReset();
    });

    test('clears the bookmark warning when the channel changes', async () => {
        jest.mocked(listChannelArtifacts).mockResolvedValue({
            entries: [{
                id: 'artifactid012345678901234',
                channel_id: channelID,
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
            }],
            can_manage: true,
            download_enabled: true,
            render_enabled: true,
        });
        jest.mocked(patchChannelArtifact).mockResolvedValue({
            id: 'artifactid012345678901234',
            channel_id: channelID,
            team_id: 'teamid01234567890123456789',
            title: 'Renamed',
            source_post_id: 'postid01234567890123456789',
            source_file_id: 'fileid01234567890123456789',
            source_file_name: 'notes.html',
            source_bot_user_id: 'botid012345678901234567890',
            created_by: 'userid0123456789012345678',
            create_at: 1,
            update_at: 2,
            size: 12,
            sha256: 'abc',
            version: 1,
        });
        jest.mocked(syncLibraryArtifactBookmarkTitle).mockRejectedValue(new Error('bookmarks unavailable'));

        const store = renderRHS();
        fireEvent.click(await screen.findByTestId('channel-artifact-edit-artifactid012345678901234'));
        fireEvent.keyDown(screen.getByTestId('channel-artifact-rename-artifactid012345678901234'), {key: 'Enter'});

        expect(await screen.findByText('Renamed the artifact, but could not update its bookmark.')).toBeTruthy();

        act(() => {
            store.dispatch({type: 'SET_CHANNEL', id: otherChannelID});
        });

        await waitFor(() => {
            expect(screen.queryByText('Renamed the artifact, but could not update its bookmark.')).toBeNull();
        });
    });
});
