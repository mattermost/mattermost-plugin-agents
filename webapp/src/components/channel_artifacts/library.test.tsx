// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, render, screen, waitFor} from '@testing-library/react';
import {Provider} from 'react-redux';
import {combineReducers, createStore, type Store, type UnknownAction} from 'redux';

import {listChannelArtifacts, type ChannelArtifactEntry, type ChannelArtifactList} from '@/client';
import manifest from '@/manifest';
import {pluginReducer} from '@/redux';

import {applyLibraryEvent, useChannelLibrary} from './library';

jest.mock('@/client', () => ({
    listChannelArtifacts: jest.fn(),
}));

const channelId = 'channelid0123456789012345';

function entry(id: string, title = id): ChannelArtifactEntry {
    return {
        id,
        channel_id: channelId,
        title,
        version: 1,
        sha256: 'abc',
        size: 1,
        source_post_id: 'postid01234567890123456789',
        source_file_id: 'fileid01234567890123456789',
        source_bot_user_id: 'botid012345678901234567890',
        created_by: 'user',
        create_at: 1,
        updated_by: 'user',
        update_at: 1,
        available: true,
    };
}

const list = (entries: ChannelArtifactEntry[]): ChannelArtifactList => ({entries, can_add: true, can_edit: false, can_delete: false, max_per_channel: 50});

function Consumer({testId}: {testId: string}) {
    const library = useChannelLibrary(channelId);
    return <div data-testid={testId}>{`${library.status}:${library.entries.map((e) => e.title).join(',')}:${library.canAdd}`}</div>;
}

function setup(consumers = 1) {
    const store: Store<unknown, UnknownAction> = createStore(combineReducers({[`plugins-${manifest.id}`]: pluginReducer} as never)) as never;
    store.dispatch({type: 'SET_CHANNEL_ARTIFACTS_ENABLED', channelArtifactsEnabled: true});
    render(
        <Provider store={store}>
            {Array.from({length: consumers}, (_, i) => (
                <Consumer
                    key={i}
                    testId={`consumer-${i}`}
                />
            ))}
        </Provider>,
    );
    return store;
}

describe('channel artifact library cache', () => {
    beforeEach(() => {
        jest.mocked(listChannelArtifacts).mockReset();
    });

    test('many consumers of one channel share a single list request', async () => {
        jest.mocked(listChannelArtifacts).mockResolvedValue(list([entry('a1')]));
        setup(10);

        await waitFor(() => expect(screen.getByTestId('consumer-9').textContent).toBe('ready:a1:true'));
        expect(listChannelArtifacts).toHaveBeenCalledTimes(1);
    });

    test('a failed load is retried by the next consumer that mounts', async () => {
        jest.mocked(listChannelArtifacts).mockRejectedValueOnce(new Error('down')).mockResolvedValue(list([entry('a1')]));
        const store = setup();
        await waitFor(() => expect(screen.getByTestId('consumer-0').textContent).toMatch(/^error/));

        render(
            <Provider store={store}>
                <Consumer testId='late'/>
            </Provider>,
        );
        await waitFor(() => expect(screen.getByTestId('late').textContent).toBe('ready:a1:true'));
        expect(listChannelArtifacts).toHaveBeenCalledTimes(2);
    });

    test('a server that keeps failing is asked once per consumer, not in a loop', async () => {
        jest.mocked(listChannelArtifacts).mockRejectedValue(new Error('down'));
        const store = setup();
        await waitFor(() => expect(screen.getByTestId('consumer-0').textContent).toMatch(/^error/));
        await act(async () => {
            await new Promise((resolve) => setTimeout(resolve, 50));
        });
        expect(listChannelArtifacts).toHaveBeenCalledTimes(1);

        render(
            <Provider store={store}>
                <Consumer testId='late'/>
            </Provider>,
        );
        await waitFor(() => expect(listChannelArtifacts).toHaveBeenCalledTimes(2));
        await act(async () => {
            await new Promise((resolve) => setTimeout(resolve, 50));
        });
        expect(listChannelArtifacts).toHaveBeenCalledTimes(2);
    });

    test('websocket events update every consumer without another request', async () => {
        jest.mocked(listChannelArtifacts).mockResolvedValue(list([entry('a1', 'One')]));
        const store = setup(2);
        await waitFor(() => expect(screen.getByTestId('consumer-1').textContent).toBe('ready:One:true'));

        act(() => {
            applyLibraryEvent(store.dispatch as never, 'created', {channel_id: channelId, artifact: entry('a2', 'Two')});
        });
        expect(screen.getByTestId('consumer-0').textContent).toBe('ready:Two,One:true');

        act(() => {
            applyLibraryEvent(store.dispatch as never, 'updated', {channel_id: channelId, artifact: entry('a1', 'Renamed')});
        });
        expect(screen.getByTestId('consumer-1').textContent).toBe('ready:Two,Renamed:true');

        act(() => {
            applyLibraryEvent(store.dispatch as never, 'deleted', {channel_id: channelId, id: 'a2'});
        });
        expect(screen.getByTestId('consumer-0').textContent).toBe('ready:Renamed:true');
        expect(listChannelArtifacts).toHaveBeenCalledTimes(1);
    });

    test('turning the feature off clears the cache', async () => {
        jest.mocked(listChannelArtifacts).mockResolvedValue(list([entry('a1')]));
        const store = setup();
        await waitFor(() => expect(screen.getByTestId('consumer-0').textContent).toBe('ready:a1:true'));

        act(() => {
            store.dispatch({type: 'SET_CHANNEL_ARTIFACTS_ENABLED', channelArtifactsEnabled: false});
        });
        expect(screen.getByTestId('consumer-0').textContent).toBe('loading::false');
    });
});
