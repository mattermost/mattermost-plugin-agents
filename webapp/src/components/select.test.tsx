// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';

import {SelectChannel, SelectUser} from './select';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
    return {
        ...actual,
        useIntl: () => intl,
    };
});

jest.mock('../client', () => ({
    getAutocompleteAllUsers: jest.fn(),
    searchTeams: jest.fn(),
    getProfilesByIds: jest.fn(),
    getTeamsByIds: jest.fn(),
    searchAllChannels: jest.fn(),
    getChannelById: jest.fn(),
    getProfilePictureUrl: (id: string) => `/users/${id}/image`,
    getTeamIconUrl: (id: string) => `/teams/${id}/image`,
}));

const client = jest.requireMock('../client') as Record<string, jest.Mock>;

const user = (id: string, username: string) => ({id, username, last_picture_update: 0, is_bot: false});
const team = (id: string, displayName: string) => ({id, display_name: displayName, name: displayName.toLowerCase(), last_team_icon_update: 0});
const channel = (id: string, displayName: string) => ({id, display_name: displayName, type: 'O', team_display_name: 'Team A'});

describe('SelectUser', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        client.getProfilesByIds.mockResolvedValue([user('u1', 'alice')]);
        client.getTeamsByIds.mockResolvedValue([]);
        client.getAutocompleteAllUsers.mockResolvedValue({users: [user('u2', 'bob')]});
        client.searchTeams.mockResolvedValue([team('t1', 'Engineering')]);
    });

    it.each([
        {name: 'a team', pick: 'Engineering', users: ['u1'], teams: ['t1']},
        {name: 'a user', pick: 'bob', users: ['u1', 'u2'], teams: []},
    ])('keeps users and teams apart when $name is added', async ({pick, users, teams}) => {
        const onChangeIDs = jest.fn();
        render(
            <SelectUser
                userIDs={['u1']}
                teamIDs={[]}
                onChangeIDs={onChangeIDs}
            />,
        );

        await screen.findByText('alice');
        fireEvent.focus(screen.getByRole('combobox'));
        fireEvent.click(await screen.findByRole('option', {name: new RegExp(pick)}));

        expect(onChangeIDs).toHaveBeenLastCalledWith(users, teams);
    });

    it('keeps an existing selection visible when a search does not return it', async () => {
        client.getAutocompleteAllUsers.mockResolvedValue({users: []});
        client.searchTeams.mockResolvedValue([]);
        render(
            <SelectUser
                userIDs={['u1']}
                teamIDs={[]}
                onChangeIDs={jest.fn()}
            />,
        );

        await screen.findByText('alice');
        fireEvent.change(screen.getByRole('combobox'), {target: {value: 'zzz'}});
        await waitFor(() => expect(client.getAutocompleteAllUsers).toHaveBeenCalledWith('zzz'));

        expect(screen.getByText('alice')).toBeTruthy();
    });
});

describe('SelectChannel', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        client.getChannelById.mockResolvedValue(channel('c0', 'Existing'));
    });

    it('shows only the results of the latest search when responses arrive out of order', async () => {
        const pending: Record<string, (value: unknown) => void> = {};
        client.searchAllChannels.mockImplementation((term: string) => new Promise((resolve) => {
            pending[term] = resolve;
        }));

        render(
            <SelectChannel
                channelIDs={[]}
                onChangeChannelIDs={jest.fn()}
            />,
        );
        const input = screen.getByRole('combobox');
        fireEvent.focus(input);

        fireEvent.change(input, {target: {value: 'ra'}});
        await waitFor(() => expect(pending.ra).toBeDefined());
        fireEvent.change(input, {target: {value: 'ran'}});
        await waitFor(() => expect(pending.ran).toBeDefined());

        await act(async () => pending.ran([channel('c2', 'Random')]));
        await act(async () => pending.ra([channel('c1', 'Rabbits')]));

        expect(await screen.findByRole('option', {name: /Random/})).toBeTruthy();
        expect(screen.queryByRole('option', {name: /Rabbits/})).toBeNull();
    });

    it('drops the previous results when a search fails', async () => {
        client.searchAllChannels.mockImplementation((term: string) => (
            term === 'ra' ? Promise.resolve([channel('c1', 'Rabbits')]) : Promise.reject(new Error('network'))
        ));

        render(
            <SelectChannel
                channelIDs={[]}
                onChangeChannelIDs={jest.fn()}
            />,
        );
        const input = screen.getByRole('combobox');
        fireEvent.focus(input);

        fireEvent.change(input, {target: {value: 'ra'}});
        expect(await screen.findByRole('option', {name: /Rabbits/})).toBeTruthy();
        fireEvent.change(input, {target: {value: 'ran'}});
        await waitFor(() => expect(client.searchAllChannels).toHaveBeenCalledWith('ran'));

        await waitFor(() => expect(screen.queryByRole('option', {name: /Rabbits/})).toBeNull());
    });

    it('keeps earlier picks when another is added before the selection reloads', async () => {
        client.getChannelById.mockImplementation((id: string) => (
            id === 'c0' ? Promise.resolve(channel('c0', 'Existing')) : new Promise(jest.fn())
        ));
        client.searchAllChannels.mockResolvedValue([channel('c1', 'Rabbits'), channel('c2', 'Random')]);
        const onChangeChannelIDs = jest.fn();
        const Harness = () => {
            const [ids, setIDs] = useState(['c0']);
            return (
                <SelectChannel
                    channelIDs={ids}
                    onChangeChannelIDs={(next) => {
                        onChangeChannelIDs(next);
                        setIDs(next);
                    }}
                />
            );
        };
        render(<Harness/>);

        await screen.findByText('Existing');
        fireEvent.focus(screen.getByRole('combobox'));
        fireEvent.click(await screen.findByRole('option', {name: /Random/}));
        fireEvent.focus(screen.getByRole('combobox'));
        fireEvent.click(await screen.findByRole('option', {name: /Rabbits/}));

        expect(onChangeChannelIDs).toHaveBeenLastCalledWith(['c0', 'c2', 'c1']);
    });

    it('reports the chosen channel ids', async () => {
        client.searchAllChannels.mockResolvedValue([channel('c2', 'Random')]);
        const onChangeChannelIDs = jest.fn();
        render(
            <SelectChannel
                channelIDs={['c0']}
                onChangeChannelIDs={onChangeChannelIDs}
            />,
        );

        await screen.findByText('Existing');
        fireEvent.focus(screen.getByRole('combobox'));
        fireEvent.click(await screen.findByRole('option', {name: /Random/}));

        expect(onChangeChannelIDs).toHaveBeenLastCalledWith(['c0', 'c2']);
    });
});
