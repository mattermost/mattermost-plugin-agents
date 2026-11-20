// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';
import {Provider} from 'react-redux';
import {combineReducers, createStore} from 'redux';

import {listChannelArtifactVersions, rollbackChannelArtifact, type ChannelArtifactEntry, type ChannelArtifactVersionRecord} from '@/client';
import manifest from '@/manifest';
import {pluginReducer} from '@/redux';

import HTMLArtifactFullscreen from './html_artifact_fullscreen';

jest.mock('@/client', () => ({
    deleteChannelArtifactVersion: jest.fn(),
    listChannelArtifactVersions: jest.fn(),
    rollbackChannelArtifact: jest.fn(),
    channelArtifactDownloadURL: jest.fn(),
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

jest.mock('./artifact_broker', () => ({cancelPendingConsent: jest.fn()}));

jest.mock('./artifact_frame', () => ({
    __esModule: true,
    default: () => <div data-testid='artifact-frame'/>,
}));

const fileId = 'artifactid012345678901234';

function versionRecord(version: number): ChannelArtifactVersionRecord {
    return {
        version,
        sha256: `sha${version}`,
        size: version,
        source_post_id: 'p',
        source_file_id: 'f',
        source_bot_user_id: 'b',
        created_by: 'u',
        create_at: version * 1000,
        head: version === 2,
        available: true,
    };
}

const restored = {version: 1} as ChannelArtifactEntry;

describe('HTMLArtifactFullscreen showing a document', () => {
    beforeEach(() => {
        jest.mocked(listChannelArtifactVersions).mockReset();
        jest.mocked(rollbackChannelArtifact).mockReset();
        jest.mocked(listChannelArtifactVersions).mockResolvedValue({versions: [versionRecord(1), versionRecord(2)]});
    });

    function renderDocumentViewer(renderDocument: jest.Mock) {
        const store = createStore(combineReducers({
            [`plugins-${manifest.id}`]: pluginReducer,
        } as never));
        render(
            <Provider store={store}>
                <IntlProvider locale='en'>
                    <HTMLArtifactFullscreen
                        fileId={fileId}
                        fileName='Charter'
                        artifactKind='library'
                        canEdit={true}
                        headVersion={2}
                        renderDocument={renderDocument}
                        onClose={jest.fn()}
                    />
                </IntlProvider>
            </Provider>,
        );
    }

    test('renders the document instead of a frame, for the version being viewed', async () => {
        const renderDocument = jest.fn((version: number) => <div data-testid='document'>{`v${version}`}</div>);
        renderDocumentViewer(renderDocument);

        expect(screen.queryByTestId('artifact-frame')).toBeNull();
        expect(screen.getByTestId('document').textContent).toBe('v2');
        expect(screen.getByText('Document')).toBeTruthy();
        expect(screen.queryByText('Interactive')).toBeNull();

        fireEvent.change(await screen.findByTestId('html-artifact-version-select'), {target: {value: '1'}});
        expect(screen.getByTestId('document').textContent).toBe('v1');
    });

    test('restoring an older version loads the restored document', async () => {
        jest.mocked(rollbackChannelArtifact).mockResolvedValue(restored);
        const renderDocument = jest.fn((version: number, revision: number) => <div data-testid='document'>{`v${version} r${revision}`}</div>);
        renderDocumentViewer(renderDocument);

        fireEvent.change(await screen.findByTestId('html-artifact-version-select'), {target: {value: '1'}});
        fireEvent.click(screen.getByTestId('html-artifact-restore-version'));

        await waitFor(() => expect(screen.getByTestId('document').textContent).toBe('v1 r1'));
        expect(rollbackChannelArtifact).toHaveBeenCalledWith(fileId, 1);
    });

    test('the reload button loads the document again', async () => {
        const renderDocument = jest.fn((version: number, revision: number) => <div data-testid='document'>{`v${version} r${revision}`}</div>);
        renderDocumentViewer(renderDocument);

        fireEvent.click(screen.getByTestId('html-artifact-fullscreen-reload'));
        expect(screen.getByTestId('document').textContent).toBe('v2 r1');
    });
});
