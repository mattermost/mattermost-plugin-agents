// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';
import {Provider} from 'react-redux';
import {combineReducers, createStore, type Store} from 'redux';

import {createChannelArtifact, listChannelArtifacts, type ChannelArtifactEntry, type ChannelArtifactList} from '@/client';
import manifest from '@/manifest';
import {pluginReducer} from '@/redux';

import {canAddChannelBookmarks, pinLibraryArtifactBookmark, syncLibraryArtifactBookmarkTitle} from './bookmark';
import SaveToChannelButton from './save_to_channel_button';

jest.mock('@/client', () => ({
    listChannelArtifacts: jest.fn(),
    createChannelArtifact: jest.fn(),
}));

jest.mock('./bookmark', () => ({
    canAddChannelBookmarks: jest.fn(() => false),
    pinLibraryArtifactBookmark: jest.fn(),
    syncLibraryArtifactBookmarkTitle: jest.fn(),
}));

jest.mock('../animated_modal_shell', () => ({
    AnimatedModalShell: ({show, children}: {show: boolean; children: React.ReactNode}) => (show ? <>{children}</> : null),
}));

jest.mock('@mattermost/compass-ui/components/modal', () => ({
    Modal: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
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

const channelId = 'channelid0123456789012345';
const postId = 'postid01234567890123456789';
const fileId = 'fileid01234567890123456789';

function entry(overrides: Partial<ChannelArtifactEntry> = {}): ChannelArtifactEntry {
    return {
        id: 'artifactid012345678901234',
        channel_id: channelId,
        title: 'Notes',
        version: 1,
        sha256: 'abc',
        size: 1,
        source_post_id: postId,
        source_file_id: fileId,
        source_bot_user_id: 'botid012345678901234567890',
        created_by: 'user',
        create_at: 1,
        updated_by: 'user',
        update_at: 1,
        available: true,
        ...overrides,
    };
}

const entities = (state = {channels: {channels: {[channelId]: {type: 'O'}}}}) => state;

function permissions(overrides: Partial<ChannelArtifactList> = {}): ChannelArtifactList {
    return {entries: [], can_add: true, can_edit: true, can_delete: true, max_per_channel: 50, ...overrides};
}

function renderButton(channelType = 'O') {
    const store: Store = createStore(combineReducers({[`plugins-${manifest.id}`]: pluginReducer, entities: () => ({channels: {channels: {[channelId]: {type: channelType}}}})} as never));
    store.dispatch({type: 'SET_CHANNEL_ARTIFACTS_ENABLED', channelArtifactsEnabled: true});
    render(
        <Provider store={store}>
            <IntlProvider locale='en'>
                <SaveToChannelButton
                    fileId={fileId}
                    fileName='dashboard.html'
                    postId={postId}
                    channelId={channelId}
                />
            </IntlProvider>
        </Provider>,
    );
    return store;
}

// jsdom marks every event dispatched from script as untrusted. Dispatching
// through its internal implementation stands in for a real user's click.
type JsdomImpl = {isTrusted: boolean; _dispatch: (event: JsdomImpl) => boolean};
function jsdomImpl(wrapper: object): JsdomImpl {
    const symbol = Object.getOwnPropertySymbols(wrapper).find((s) => s.description === 'impl');
    return (wrapper as Record<symbol, JsdomImpl>)[symbol!];
}
function trustedClick(el: Element) {
    const event = jsdomImpl(new MouseEvent('click', {bubbles: true, cancelable: true}));
    event.isTrusted = true;
    act(() => {
        jsdomImpl(el)._dispatch(event); // eslint-disable-line no-underscore-dangle
    });
}

describe('SaveToChannelButton', () => {
    beforeEach(() => {
        jest.mocked(listChannelArtifacts).mockReset();
        jest.mocked(createChannelArtifact).mockReset();
    });

    afterEach(() => {
        jest.restoreAllMocks();
    });

    test('is hidden from someone who may neither add nor edit', async () => {
        jest.mocked(listChannelArtifacts).mockResolvedValue(permissions({can_add: false, can_edit: false, can_delete: false}));
        renderButton();
        await waitFor(() => expect(listChannelArtifacts).toHaveBeenCalled());
        expect(screen.queryByTestId('html-artifact-save-to-channel')).toBeNull();
    });

    test.each(['D', 'G'])('is hidden in a %s channel and never asks the server for a library', async (channelType) => {
        jest.mocked(listChannelArtifacts).mockResolvedValue(permissions());
        renderButton(channelType);
        await act(async () => {
            await new Promise((resolve) => setTimeout(resolve, 20));
        });
        expect(screen.queryByTestId('html-artifact-save-to-channel')).toBeNull();
        expect(listChannelArtifacts).not.toHaveBeenCalled();
    });

    test('still offers the control after the list fails, and the click that retries the load opens the dialog', async () => {
        jest.mocked(listChannelArtifacts).mockRejectedValueOnce(new Error('down')).mockResolvedValue(permissions());
        renderButton();

        const button = await screen.findByTestId('html-artifact-save-to-channel');
        trustedClick(button);
        expect(await screen.findByTestId('channel-artifact-save-modal')).toBeTruthy();
        expect(listChannelArtifacts).toHaveBeenCalledTimes(2);
    });

    test('lists the library once however many cards are on screen', async () => {
        jest.mocked(listChannelArtifacts).mockResolvedValue(permissions());
        const store: Store = createStore(combineReducers({[`plugins-${manifest.id}`]: pluginReducer, entities} as never));
        store.dispatch({type: 'SET_CHANNEL_ARTIFACTS_ENABLED', channelArtifactsEnabled: true});
        render(
            <Provider store={store}>
                <IntlProvider locale='en'>
                    {Array.from({length: 8}, (_, i) => (
                        <SaveToChannelButton
                            key={i}
                            fileId={`${fileId}${i}`}
                            fileName='dashboard.html'
                            postId={postId}
                            channelId={channelId}
                        />
                    ))}
                </IntlProvider>
            </Provider>,
        );
        await waitFor(() => expect(screen.getAllByTestId('html-artifact-save-to-channel')).toHaveLength(8));
        expect(listChannelArtifacts).toHaveBeenCalledTimes(1);
    });

    test.each([
        {status: 403, message: "You don't have permission to save artifacts in this channel."},
        {status: 404, message: 'The message or file is no longer available.'},
        {status: 409, message: "This channel's artifact library is full. Remove one first."},
        {status: 413, message: 'This artifact is too large to save.'},
        {status: 422, message: 'Only HTML files posted by an agent can be saved.'},
        {status: 500, message: 'Could not save artifact.'},
    ])('explains a $status response', async ({status, message}) => {
        jest.mocked(listChannelArtifacts).mockResolvedValue(permissions());
        jest.mocked(createChannelArtifact).mockRejectedValue({status_code: status, message: ''});
        renderButton();

        trustedClick(await screen.findByTestId('html-artifact-save-to-channel'));
        fireEvent.click(await screen.findByTestId('channel-artifact-save-confirm'));
        expect(await screen.findByText(message)).toBeTruthy();
    });

    test('saves, adds the entry to the shared library, and closes', async () => {
        jest.mocked(listChannelArtifacts).mockResolvedValue(permissions());
        jest.mocked(createChannelArtifact).mockResolvedValue(entry());
        const store = renderButton();

        trustedClick(await screen.findByTestId('html-artifact-save-to-channel'));
        fireEvent.click(await screen.findByTestId('channel-artifact-save-confirm'));

        await waitFor(() => expect(screen.queryByTestId('channel-artifact-save-modal')).toBeNull());
        expect(createChannelArtifact).toHaveBeenCalledWith({channel_id: channelId, post_id: postId, file_id: fileId, title: 'dashboard'});
        const state = store.getState() as unknown as Record<string, {channelArtifactLibrary: Record<string, {entries: ChannelArtifactEntry[]}>}>;
        expect(state[`plugins-${manifest.id}`].channelArtifactLibrary[channelId].entries).toHaveLength(1);
    });

    test('retries only the pin, not the save, when pinning fails after a save', async () => {
        jest.mocked(canAddChannelBookmarks).mockReturnValue(true);
        jest.mocked(pinLibraryArtifactBookmark).mockReset();
        jest.mocked(pinLibraryArtifactBookmark).mockRejectedValueOnce(new Error('bookmarks down')).mockResolvedValue({id: 'bm1'} as never);
        jest.mocked(listChannelArtifacts).mockResolvedValue(permissions({entries: [entry({source_file_id: fileId})]}));
        jest.mocked(createChannelArtifact).mockResolvedValue(entry({version: 2}));
        renderButton();

        trustedClick(await screen.findByTestId('html-artifact-save-to-channel'));
        fireEvent.click(await screen.findByTestId('channel-artifact-update-ack'));
        fireEvent.click(screen.getByTestId('channel-artifact-pin-bookmark'));
        fireEvent.click(screen.getByTestId('channel-artifact-save-confirm'));
        expect(await screen.findByText('Saved the artifact, but could not pin it to bookmarks.')).toBeTruthy();

        fireEvent.click(screen.getByTestId('channel-artifact-save-confirm'));
        await waitFor(() => expect(screen.queryByTestId('channel-artifact-save-modal')).toBeNull());
        expect(createChannelArtifact).toHaveBeenCalledTimes(1);
        expect(pinLibraryArtifactBookmark).toHaveBeenCalledTimes(2);
    });

    test('a new version that renames the entry renames its channel bookmark too', async () => {
        jest.mocked(syncLibraryArtifactBookmarkTitle).mockReset();
        jest.mocked(syncLibraryArtifactBookmarkTitle).mockResolvedValue(true);
        jest.mocked(listChannelArtifacts).mockResolvedValue(permissions({entries: [entry({source_file_id: fileId})]}));
        jest.mocked(createChannelArtifact).mockResolvedValue(entry({version: 2, title: 'Renamed'}));
        renderButton();

        trustedClick(await screen.findByTestId('html-artifact-save-to-channel'));
        fireEvent.click(await screen.findByTestId('channel-artifact-update-ack'));
        fireEvent.click(screen.getByTestId('channel-artifact-save-confirm'));

        await waitFor(() => expect(screen.queryByTestId('channel-artifact-save-modal')).toBeNull());
        expect(syncLibraryArtifactBookmarkTitle).toHaveBeenCalledWith(expect.anything(), channelId, entry().id, 'Renamed', expect.anything());
    });

    test('retries only the bookmark rename, not the save, when it fails after a save', async () => {
        jest.mocked(syncLibraryArtifactBookmarkTitle).mockReset();
        jest.mocked(syncLibraryArtifactBookmarkTitle).mockRejectedValueOnce(new Error('bookmarks down')).mockResolvedValue(true);
        jest.mocked(listChannelArtifacts).mockResolvedValue(permissions({entries: [entry({source_file_id: fileId})]}));
        jest.mocked(createChannelArtifact).mockResolvedValue(entry({version: 2, title: 'Renamed'}));
        renderButton();

        trustedClick(await screen.findByTestId('html-artifact-save-to-channel'));
        fireEvent.click(await screen.findByTestId('channel-artifact-update-ack'));
        fireEvent.click(screen.getByTestId('channel-artifact-save-confirm'));
        expect(await screen.findByText('Saved the artifact, but could not update its bookmark title.')).toBeTruthy();

        fireEvent.click(screen.getByTestId('channel-artifact-save-confirm'));
        await waitFor(() => expect(screen.queryByTestId('channel-artifact-save-modal')).toBeNull());
        expect(createChannelArtifact).toHaveBeenCalledTimes(1);
        expect(syncLibraryArtifactBookmarkTitle).toHaveBeenCalledTimes(2);
    });
});
