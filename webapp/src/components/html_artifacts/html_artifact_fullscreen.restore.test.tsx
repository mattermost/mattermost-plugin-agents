// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';
import {Provider} from 'react-redux';
import {combineReducers, createStore} from 'redux';

import {listChannelArtifactVersions, rollbackChannelArtifact, type ChannelArtifactEntry} from '@/client';
import manifest from '@/manifest';
import {pluginReducer} from '@/redux';

import HTMLArtifactFullscreen from './html_artifact_fullscreen';

jest.mock('@/client', () => ({
    listChannelArtifactVersions: jest.fn(),
    rollbackChannelArtifact: jest.fn(),
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

jest.mock('./artifact_frame', () => {
    const {forwardRef} = jest.requireActual('react') as typeof import('react');
    const MockFrame = forwardRef(() => {
        return <div data-testid='artifact-frame'/>;
    });
    return {
        __esModule: true,
        default: MockFrame,
    };
});

const fileId = 'artifactid012345678901234';

const restored = {
    id: fileId,
    channel_id: 'channelid0123456789012345',
    team_id: 'teamid01234567890123456789',
    title: 'Notes',
    source_post_id: 'postid01234567890123456789',
    source_file_id: 'fileid01234567890123456789',
    source_file_name: 'notes.html',
    source_bot_user_id: 'botid012345678901234567890',
    created_by: 'userid0123456789012345678',
    create_at: 1,
    update_at: 3,
    size: 12,
    sha256: 'abc',
    version: 3,
} satisfies ChannelArtifactEntry;

function renderViewer() {
    const store = createStore(combineReducers({
        [`plugins-${manifest.id}`]: pluginReducer,
    } as never));
    render(
        <Provider store={store}>
            <IntlProvider locale='en'>
                <HTMLArtifactFullscreen
                    fileId={fileId}
                    fileName='Notes'
                    artifactKind='library'
                    canManage={true}
                    headVersion={2}
                    onClose={jest.fn()}
                />
            </IntlProvider>
        </Provider>,
    );
}

describe('HTMLArtifactFullscreen restore', () => {
    beforeEach(() => {
        jest.mocked(listChannelArtifactVersions).mockReset();
        jest.mocked(rollbackChannelArtifact).mockReset();
        jest.mocked(listChannelArtifactVersions).mockResolvedValue({
            versions: [
                {version: 2, sha256: 'b', size: 2, update_at: 2000},
                {version: 1, sha256: 'a', size: 1, update_at: 1000},
            ],
        });
    });

    test('tells the manager when restore fails and clears that message on a new version', async () => {
        jest.mocked(rollbackChannelArtifact).mockRejectedValue(new Error('denied'));
        renderViewer();
        const select = await screen.findByTestId('html-artifact-version-select');
        fireEvent.change(select, {target: {value: '1'}});
        fireEvent.click(screen.getByTestId('html-artifact-restore-version'));
        expect((await screen.findByTestId('html-artifact-restore-error')).textContent).toBe('Could not restore this version.');

        fireEvent.change(select, {target: {value: '2'}});
        fireEvent.change(select, {target: {value: '1'}});
        expect(screen.queryByTestId('html-artifact-restore-error')).toBeNull();
    });

    test('does not report a restore failure when the version list refresh fails', async () => {
        jest.mocked(rollbackChannelArtifact).mockResolvedValue(restored);
        jest.mocked(listChannelArtifactVersions).
            mockResolvedValueOnce({
                versions: [
                    {version: 2, sha256: 'b', size: 2, update_at: 2000},
                    {version: 1, sha256: 'a', size: 1, update_at: 1000},
                ],
            }).
            mockRejectedValueOnce(new Error('list down'));
        renderViewer();
        const select = await screen.findByTestId('html-artifact-version-select');
        fireEvent.change(select, {target: {value: '1'}});
        fireEvent.click(screen.getByTestId('html-artifact-restore-version'));
        await waitFor(() => expect(screen.queryByTestId('html-artifact-restore-version')).toBeNull());
        expect(screen.queryByTestId('html-artifact-restore-error')).toBeNull();
        expect(rollbackChannelArtifact).toHaveBeenCalledWith(fileId, 1);
    });
});
