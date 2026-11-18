// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';
import {Provider} from 'react-redux';
import {combineReducers, createStore} from 'redux';

import {
    deleteChannelArtifactVersion,
    listChannelArtifactVersions,
    rollbackChannelArtifact,
    type ChannelArtifactEntry,
    type ChannelArtifactVersionRecord,
} from '@/client';
import manifest from '@/manifest';
import {pluginReducer} from '@/redux';

import HTMLArtifactFullscreen from './html_artifact_fullscreen';

jest.mock('@/client', () => ({
    deleteChannelArtifactVersion: jest.fn(),
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

function versionRecord(version: number, overrides: Partial<ChannelArtifactVersionRecord> = {}): ChannelArtifactVersionRecord {
    return {
        version,
        sha256: `sha${version}`,
        size: version,
        source_post_id: 'postid01234567890123456789',
        source_file_id: 'fileid01234567890123456789',
        source_bot_user_id: 'botid012345678901234567890',
        created_by: 'userid0123456789012345678',
        create_at: version * 1000,
        head: version === 2,
        available: true,
        ...overrides,
    };
}

const restored = {
    id: fileId,
    channel_id: 'channelid0123456789012345',
    title: 'Notes',
    source_post_id: 'postid01234567890123456789',
    source_file_id: 'fileid01234567890123456789',
    source_bot_user_id: 'botid012345678901234567890',
    created_by: 'userid0123456789012345678',
    create_at: 1,
    updated_by: 'userid0123456789012345678',
    update_at: 3,
    available: true,
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
                    canEdit={true}
                    canDelete={true}
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
        jest.mocked(deleteChannelArtifactVersion).mockReset();
        jest.mocked(listChannelArtifactVersions).mockResolvedValue({
            versions: [
                versionRecord(2),
                versionRecord(1),
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
                    versionRecord(2),
                    versionRecord(1),
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

    test('refuses to restore a version whose source is gone', async () => {
        jest.mocked(listChannelArtifactVersions).mockResolvedValue({
            versions: [versionRecord(2), versionRecord(1, {available: false})],
        });
        renderViewer();
        const select = await screen.findByTestId('html-artifact-version-select');
        expect(screen.getByText(/v1 .* unavailable/)).toBeTruthy();
        fireEvent.change(select, {target: {value: '1'}});
        expect((screen.getByTestId('html-artifact-restore-version') as HTMLButtonElement).disabled).toBe(true);
    });

    test('removes a version and follows the new current version', async () => {
        jest.spyOn(window, 'confirm').mockReturnValue(true);
        jest.mocked(deleteChannelArtifactVersion).mockResolvedValue({...restored, version: 1});
        renderViewer();
        const select = await screen.findByTestId('html-artifact-version-select');
        fireEvent.change(select, {target: {value: '2'}});
        jest.mocked(listChannelArtifactVersions).mockResolvedValue({versions: [versionRecord(1, {head: true})]});
        fireEvent.click(screen.getByTestId('html-artifact-remove-version'));
        await waitFor(() => expect(deleteChannelArtifactVersion).toHaveBeenCalledWith(fileId, 2));
        await waitFor(() => expect(screen.queryByTestId('html-artifact-remove-version')).toBeNull());
    });

    test('says so when removing a version fails', async () => {
        jest.spyOn(window, 'confirm').mockReturnValue(true);
        jest.mocked(deleteChannelArtifactVersion).mockRejectedValue(new Error('denied'));
        renderViewer();
        await screen.findByTestId('html-artifact-version-select');
        fireEvent.click(screen.getByTestId('html-artifact-remove-version'));
        expect((await screen.findByTestId('html-artifact-remove-version-error')).textContent).toBe('Could not remove this version.');
    });
});
