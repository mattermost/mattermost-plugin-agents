// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';
import {Provider} from 'react-redux';
import {combineReducers, createStore, type Store} from 'redux';

import {deleteChannelArtifact, listChannelArtifacts, patchChannelArtifact, type ChannelArtifactEntry, type ChannelArtifactList} from '@/client';
import manifest from '@/manifest';
import {pluginReducer} from '@/redux';

import {syncLibraryArtifactBookmarkTitle, unpinLibraryArtifactBookmark} from './bookmark';
import ChannelArtifactsRHS from './channel_artifacts_rhs';

jest.mock('@/client', () => ({
    listChannelArtifacts: jest.fn(),
    patchChannelArtifact: jest.fn(),
    deleteChannelArtifact: jest.fn(),
}));

jest.mock('./bookmark', () => ({
    syncLibraryArtifactBookmarkTitle: jest.fn(),
    unpinLibraryArtifactBookmark: jest.fn(),
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
    return function MockFullscreen(props: {fileId: string; renderDocument?: unknown; startedByClick?: boolean; canEdit?: boolean}) {
        return (
            <div
                data-testid='html-artifact-fullscreen'
                data-file-id={props.fileId}
                data-document={String(Boolean(props.renderDocument))}
                data-started-by-click={String(Boolean(props.startedByClick))}
                data-can-edit={String(Boolean(props.canEdit))}
            />
        );
    };
});

const channelID = 'channelid0123456789012345';
const otherChannelID = 'channelid0123456789012346';
const artifactID = 'artifactid012345678901234';

function libraryEntry(overrides: Partial<ChannelArtifactEntry> = {}): ChannelArtifactEntry {
    return {
        id: artifactID,
        channel_id: channelID,
        title: 'Notes',
        version: 1,
        sha256: 'abc',
        size: 12,
        source_post_id: 'postid01234567890123456789',
        source_file_id: 'fileid01234567890123456789',
        source_bot_user_id: 'botid012345678901234567890',
        source_agent_display_name: 'Helper',
        created_by: 'userid0123456789012345678',
        create_at: 1,
        updated_by: 'userid0123456789012345678',
        update_at: 1,
        available: true,
        ...overrides,
    };
}

function libraryList(entries: ChannelArtifactEntry[], permissions: Partial<ChannelArtifactList> = {}): ChannelArtifactList {
    return {entries, can_add: true, can_edit: true, can_delete: true, max_per_channel: 50, ...permissions};
}

function renderRHS(): Store {
    const entities = (state = {
        channels: {currentChannelId: channelID, channels: {[channelID]: {type: 'O'}, [otherChannelID]: {type: 'O'}, dmchannel01234567890123456: {type: 'D'}} as Record<string, {type: string}>},
        general: {config: {}, license: {}},
        users: {currentUserId: 'user', profiles: {}},
        teams: {currentTeamId: '', teams: {}},
    }, action: {type: string; id?: string}) => {
        if (action.type === 'SET_CHANNEL') {
            return {...state, channels: {...state.channels, currentChannelId: action.id || ''}};
        }
        return state;
    };
    const store: Store = createStore(combineReducers({
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
        jest.mocked(deleteChannelArtifact).mockReset();
        jest.mocked(unpinLibraryArtifactBookmark).mockReset();
    });

    test('lists entries newest first as the server returns them, and marks an unavailable one', async () => {
        jest.mocked(listChannelArtifacts).mockResolvedValue(libraryList([
            libraryEntry({id: 'artifactid012345678901235', title: 'Second', available: false}),
            libraryEntry({title: 'First'}),
        ]));
        renderRHS();

        const cards = await screen.findAllByTestId(/^channel-artifact-artifactid/);
        expect(cards.map((card) => card.getAttribute('data-testid'))).toEqual([
            'channel-artifact-artifactid012345678901235',
            'channel-artifact-artifactid012345678901234',
        ]);
        expect(screen.getByTestId('channel-artifact-unavailable-artifactid012345678901235')).toBeTruthy();
        expect(screen.queryByTestId('channel-artifact-unavailable-artifactid012345678901234')).toBeNull();
        expect(screen.getAllByText(/Helper/).length).toBeGreaterThan(0);
    });

    test('badges each entry with its format, treating a missing one as HTML', async () => {
        jest.mocked(listChannelArtifacts).mockResolvedValue(libraryList([
            libraryEntry({id: 'artifactid012345678901235', title: 'Charter', format: 'markdown'}),
            libraryEntry({id: 'artifactid012345678901236', title: 'Dashboard', format: 'html'}),
            libraryEntry({title: 'Legacy'}),
        ]));
        renderRHS();

        const badge = (id: string) => screen.findByTestId(`channel-artifact-format-${id}`);
        expect((await badge('artifactid012345678901235')).getAttribute('data-format')).toBe('markdown');
        expect((await badge('artifactid012345678901235')).textContent).toBe('Markdown');
        expect((await badge('artifactid012345678901236')).textContent).toBe('HTML');
        expect((await badge(artifactID)).getAttribute('data-format')).toBe('html');
    });

    test.each([
        ['html', false],
        ['markdown', true],
    ] as const)('opens a %s artifact in its own viewer', async (format, document) => {
        jest.mocked(listChannelArtifacts).mockResolvedValue(libraryList([libraryEntry({format})]));
        renderRHS();
        fireEvent.click(await screen.findByTestId(`channel-artifact-open-${artifactID}`));

        const viewer = await screen.findByTestId('html-artifact-fullscreen');
        expect(viewer.getAttribute('data-file-id')).toBe(artifactID);
        expect(viewer.getAttribute('data-document')).toBe(String(document));
        expect(viewer.getAttribute('data-started-by-click')).toBe('true');
        expect(viewer.getAttribute('data-can-edit')).toBe('true');
    });

    test('does not label an entry with no agent name as an agent', async () => {
        jest.mocked(listChannelArtifacts).mockResolvedValue(libraryList([libraryEntry({source_agent_display_name: ''})]));
        renderRHS();
        await screen.findByTestId(`channel-artifact-${artifactID}`);
        expect(screen.queryByText(/Agent/)).toBeNull();
    });

    test('hides rename and remove from someone who cannot edit or delete', async () => {
        jest.mocked(listChannelArtifacts).mockResolvedValue(libraryList([libraryEntry()], {can_add: false, can_edit: false, can_delete: false}));
        renderRHS();
        await screen.findByTestId(`channel-artifact-${artifactID}`);
        expect(screen.queryByTestId(`channel-artifact-edit-${artifactID}`)).toBeNull();
        expect(screen.queryByTestId(`channel-artifact-delete-${artifactID}`)).toBeNull();
        expect(screen.getByTestId(`channel-artifact-open-${artifactID}`)).toBeTruthy();
    });

    test('clears the bookmark warning when the channel changes', async () => {
        jest.mocked(listChannelArtifacts).mockResolvedValue(libraryList([libraryEntry()]));
        jest.mocked(patchChannelArtifact).mockResolvedValue(libraryEntry({title: 'Renamed'}));
        jest.mocked(syncLibraryArtifactBookmarkTitle).mockRejectedValue(new Error('bookmarks unavailable'));

        const store = renderRHS();
        fireEvent.click(await screen.findByTestId(`channel-artifact-edit-${artifactID}`));
        fireEvent.keyDown(screen.getByTestId(`channel-artifact-rename-${artifactID}`), {key: 'Enter'});

        expect(await screen.findByText('Renamed the artifact, but could not update its bookmark.')).toBeTruthy();

        act(() => {
            store.dispatch({type: 'SET_CHANNEL', id: otherChannelID});
        });

        await waitFor(() => {
            expect(screen.queryByText('Renamed the artifact, but could not update its bookmark.')).toBeNull();
        });
    });

    test('escape leaves the title unchanged', async () => {
        jest.mocked(listChannelArtifacts).mockResolvedValue(libraryList([libraryEntry()]));
        renderRHS();
        fireEvent.click(await screen.findByTestId(`channel-artifact-edit-${artifactID}`));
        const input = screen.getByTestId(`channel-artifact-rename-${artifactID}`);
        fireEvent.change(input, {target: {value: 'Draft'}});
        fireEvent.keyDown(input, {key: 'Escape'});
        fireEvent.blur(input);

        expect(patchChannelArtifact).not.toHaveBeenCalled();
    });

    test('enter saves the title once when the input then blurs, and shows the new title', async () => {
        jest.mocked(listChannelArtifacts).mockResolvedValue(libraryList([libraryEntry()]));
        jest.mocked(patchChannelArtifact).mockResolvedValue(libraryEntry({title: 'Renamed'}));
        jest.mocked(syncLibraryArtifactBookmarkTitle).mockResolvedValue(true);

        renderRHS();
        fireEvent.click(await screen.findByTestId(`channel-artifact-edit-${artifactID}`));
        const input = screen.getByTestId(`channel-artifact-rename-${artifactID}`);
        fireEvent.change(input, {target: {value: 'Renamed'}});
        fireEvent.keyDown(input, {key: 'Enter'});
        fireEvent.blur(input);

        await waitFor(() => expect(syncLibraryArtifactBookmarkTitle).toHaveBeenCalled());
        expect(patchChannelArtifact).toHaveBeenCalledTimes(1);
        expect(await screen.findByText('Renamed')).toBeTruthy();
    });

    test('removing an artifact deletes the entry, unpins its bookmark and drops the card', async () => {
        jest.mocked(listChannelArtifacts).mockResolvedValue(libraryList([libraryEntry({version: 3})]));
        jest.mocked(deleteChannelArtifact).mockResolvedValue(undefined); // eslint-disable-line no-undefined
        jest.mocked(unpinLibraryArtifactBookmark).mockResolvedValue(true);
        const confirm = jest.spyOn(window, 'confirm').mockReturnValue(true);

        renderRHS();
        fireEvent.click(await screen.findByTestId(`channel-artifact-delete-${artifactID}`));

        await waitFor(() => expect(deleteChannelArtifact).toHaveBeenCalledWith(artifactID));
        await waitFor(() => expect(screen.queryByTestId(`channel-artifact-${artifactID}`)).toBeNull());
        expect(unpinLibraryArtifactBookmark).toHaveBeenCalledWith(expect.anything(), channelID, artifactID, expect.anything());
        expect(confirm).toHaveBeenCalledTimes(1);
    });

    test('declining the confirmation removes nothing', async () => {
        jest.mocked(listChannelArtifacts).mockResolvedValue(libraryList([libraryEntry()]));
        jest.spyOn(window, 'confirm').mockReturnValue(false);

        renderRHS();
        fireEvent.click(await screen.findByTestId(`channel-artifact-delete-${artifactID}`));

        expect(deleteChannelArtifact).not.toHaveBeenCalled();
        expect(screen.getByTestId(`channel-artifact-${artifactID}`)).toBeTruthy();
    });

    test('shows an error and keeps the pin when removing fails', async () => {
        jest.mocked(listChannelArtifacts).mockResolvedValue(libraryList([libraryEntry()]));
        jest.mocked(deleteChannelArtifact).mockRejectedValue(new Error('boom'));
        jest.spyOn(window, 'confirm').mockReturnValue(true);

        renderRHS();
        fireEvent.click(await screen.findByTestId(`channel-artifact-delete-${artifactID}`));

        expect(await screen.findByText('Could not remove the artifact.')).toBeTruthy();
        expect(unpinLibraryArtifactBookmark).not.toHaveBeenCalled();
        expect(screen.getByTestId(`channel-artifact-${artifactID}`)).toBeTruthy();
    });

    test('shows no library controls in a direct message, and never asks the server for one', async () => {
        jest.mocked(listChannelArtifacts).mockResolvedValue(libraryList([libraryEntry()]));
        const store = renderRHS();
        await screen.findByTestId(`channel-artifact-${artifactID}`);
        jest.mocked(listChannelArtifacts).mockClear();

        act(() => {
            store.dispatch({type: 'SET_CHANNEL', id: 'dmchannel01234567890123456'});
        });

        expect(await screen.findByTestId('channel-artifacts-unsupported')).toBeTruthy();
        expect(screen.queryByTestId(`channel-artifact-${artifactID}`)).toBeNull();
        expect(listChannelArtifacts).not.toHaveBeenCalled();
    });

    test('has no explanatory text above the list', async () => {
        jest.mocked(listChannelArtifacts).mockResolvedValue(libraryList([libraryEntry()]));
        renderRHS();
        await screen.findByTestId(`channel-artifact-${artifactID}`);
        expect(screen.queryByText(/keeps a link/)).toBeNull();
        expect(screen.queryByText(/Tip:/)).toBeNull();
    });

    test('offers a retry after the list fails to load', async () => {
        jest.mocked(listChannelArtifacts).mockRejectedValueOnce(new Error('down')).mockResolvedValue(libraryList([libraryEntry()]));
        renderRHS();
        expect(await screen.findByText('Could not load channel artifacts.')).toBeTruthy();

        fireEvent.click(screen.getByTestId('channel-artifacts-refresh'));
        expect(await screen.findByTestId(`channel-artifact-${artifactID}`)).toBeTruthy();
    });
});
