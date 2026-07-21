// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen, waitFor} from '@testing-library/react';
import {Provider} from 'react-redux';

import {getChannelById, getProfilesByUsernames} from '@/client';

import {ChannelChip, UserChip} from './entity_chips';

jest.mock('@/client', () => ({
    getChannelById: jest.fn(),
    getProfilesByUsernames: jest.fn(),
    getProfilePictureUrl: jest.fn((userId: string) => `/avatar/${userId}`),
}));

const mockGetChannelById = getChannelById as jest.Mock;
const mockGetProfilesByUsernames = getProfilesByUsernames as jest.Mock;

const state = {
    entities: {
        channels: {channels: {'redux-chan': {id: 'redux-chan', display_name: 'Redux Channel', team_id: 'team1', type: 'P'}}},
        teams: {teams: {team1: {id: 'team1', display_name: 'Eng'}}},
    },
};

const store = {
    getState: () => state,
    subscribe: () => jest.fn(),
    dispatch: jest.fn(),
} as any;

function renderChip(ui: React.ReactElement) {
    return render(<Provider store={store}>{ui}</Provider>);
}

beforeEach(() => {
    mockGetChannelById.mockReset();
    mockGetProfilesByUsernames.mockReset();
});

describe('ChannelChip', () => {
    test('resolves from the redux store without fetching (icon + name + team)', () => {
        const {container} = renderChip(<ChannelChip channelId='redux-chan'/>);

        expect(screen.getByText('Redux Channel')).not.toBeNull();
        expect(screen.getByText(/Eng/)).not.toBeNull();
        expect(container.querySelector('svg')).not.toBeNull(); // resolved => icon
        expect(mockGetChannelById).not.toHaveBeenCalled();
    });

    test('fetches via the API when not in the store', async () => {
        mockGetChannelById.mockResolvedValue({display_name: 'Fetched Channel', team_display_name: 'Ops', type: 'O'});

        const {container} = renderChip(<ChannelChip channelId='fetch-chan-1'/>);

        await waitFor(() => expect(screen.getByText('Fetched Channel')).not.toBeNull());
        expect(container.querySelector('svg')).not.toBeNull();
        expect(mockGetChannelById).toHaveBeenCalledWith('fetch-chan-1');
    });

    test('degrades to the fallback display name (plain text, no icon) on fetch failure', async () => {
        mockGetChannelById.mockRejectedValue(new Error('nope'));

        const {container} = renderChip(
            <ChannelChip
                channelId='fetch-chan-2'
                fallbackName='Town Square'
                fallbackTeam='Eng'
            />,
        );

        await waitFor(() => expect(screen.getByText(/Town Square/)).not.toBeNull());

        // Fallback is plain text: no icon/svg (the resolved-entity styling).
        expect(container.querySelector('svg')).toBeNull();
    });

    test('degrades to the raw channel id when there is no fallback name', async () => {
        mockGetChannelById.mockRejectedValue(new Error('nope'));

        const {container} = renderChip(<ChannelChip channelId='fetch-chan-3'/>);

        await waitFor(() => expect(screen.getByText('fetch-chan-3')).not.toBeNull());
        expect(container.querySelector('svg')).toBeNull();
    });

    test('retries a failed lookup after the failure TTL expires', async () => {
        jest.spyOn(Date, 'now').mockReturnValue(1000000);
        try {
            mockGetChannelById.mockRejectedValueOnce(new Error('transient'));

            const first = renderChip(<ChannelChip channelId='fetch-chan-ttl'/>);
            await waitFor(() => expect(screen.getByText('fetch-chan-ttl')).not.toBeNull());
            expect(mockGetChannelById).toHaveBeenCalledTimes(1);
            first.unmount();

            // Within the TTL the failure is cached: no refetch.
            renderChip(<ChannelChip channelId='fetch-chan-ttl'/>).unmount();
            expect(mockGetChannelById).toHaveBeenCalledTimes(1);

            // After the TTL the entry expires and the fetch is retried.
            (Date.now as jest.Mock).mockReturnValue(1000000 + 60000);
            mockGetChannelById.mockResolvedValue({display_name: 'Recovered', team_display_name: 'Ops', type: 'O'});

            renderChip(<ChannelChip channelId='fetch-chan-ttl'/>);
            await waitFor(() => expect(screen.getByText('Recovered')).not.toBeNull());
            expect(mockGetChannelById).toHaveBeenCalledTimes(2);
        } finally {
            (Date.now as jest.Mock).mockRestore();
        }
    });
});

describe('UserChip', () => {
    test('resolves a username to an avatar chip', async () => {
        mockGetProfilesByUsernames.mockResolvedValue([{id: 'u1', username: 'alice', last_picture_update: 5}]);

        renderChip(<UserChip username='alice'/>);

        await waitFor(() => expect(screen.getByText('@alice')).not.toBeNull());
        const img = screen.getByRole('img');
        expect(img.getAttribute('src')).toBe('/avatar/u1');
        expect(mockGetProfilesByUsernames).toHaveBeenCalledWith(['alice']);
    });

    test('strips a leading @ before resolving', async () => {
        mockGetProfilesByUsernames.mockResolvedValue([{id: 'u2', username: 'bob', last_picture_update: 0}]);

        renderChip(<UserChip username='@bob'/>);

        await waitFor(() => expect(screen.getByText('@bob')).not.toBeNull());
        expect(mockGetProfilesByUsernames).toHaveBeenCalledWith(['bob']);
    });

    test('degrades to plain text @username (no avatar) when unresolved', async () => {
        mockGetProfilesByUsernames.mockResolvedValue([]);

        renderChip(<UserChip username='ghost'/>);

        await waitFor(() => expect(screen.getByText('@ghost')).not.toBeNull());
        expect(screen.queryByRole('img')).toBeNull();
    });

    test('degrades to plain text on fetch error', async () => {
        mockGetProfilesByUsernames.mockRejectedValue(new Error('nope'));

        renderChip(<UserChip username='err-user'/>);

        await waitFor(() => expect(screen.getByText('@err-user')).not.toBeNull());
        expect(screen.queryByRole('img')).toBeNull();
    });
});
