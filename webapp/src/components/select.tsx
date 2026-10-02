// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import {AccountMultipleOutlineIcon, GlobeIcon, LockIcon} from '@mattermost/compass-icons/components';

import {UserProfile} from '@mattermost/types/users';
import {Team} from '@mattermost/types/teams';
import {ChannelWithTeamData} from '@mattermost/types/channels';

import {Combobox, type ComboboxOption} from '@mattermost/compass-ui/components/combobox';
import {Icon} from '@mattermost/compass-ui/components/icon';

import {getAutocompleteAllUsers, getChannelById, getProfilePictureUrl, getProfilesByIds, getTeamIconUrl, getTeamsByIds, searchAllChannels, searchTeams} from '../client';

import {getPortalTarget} from '../utils/dom';

import {PORTALED_MENU_Z_INDEX} from './system_console/item';

const SEARCH_DEBOUNCE_MS = 200;

type AsyncMultiPickerProps = {
    selected: ComboboxOption[];
    search: (term: string) => Promise<ComboboxOption[]>;
    onChange: (selected: ComboboxOption[]) => void;
    placeholder: string;
    disabled?: boolean;
};

const AsyncMultiPicker = (props: AsyncMultiPickerProps) => {
    const intl = useIntl();
    const [inputValue, setInputValue] = useState('');
    const [results, setResults] = useState<ComboboxOption[]>([]);
    const [loading, setLoading] = useState(false);
    const latestRequest = useRef(0);
    const searchRef = useRef(props.search);
    searchRef.current = props.search;

    useEffect(() => {
        const request = ++latestRequest.current;
        setLoading(true);
        const timer = setTimeout(async () => {
            try {
                const options = await searchRef.current(inputValue);
                if (request === latestRequest.current) {
                    setResults(options);
                }
            } finally {
                if (request === latestRequest.current) {
                    setLoading(false);
                }
            }
        }, SEARCH_DEBOUNCE_MS);
        return () => clearTimeout(timer);
    }, [inputValue]);

    const handleChange = (value: string | string[] | null) => {
        const values = Array.isArray(value) ? value : [];
        const known = new Map([...props.selected, ...results].map((option) => [option.value, option]));
        props.onChange(values.flatMap((v) => (known.has(v) ? [known.get(v) as ComboboxOption] : [])));
    };

    return (
        <Combobox
            multiple={true}
            filter={false}
            value={props.selected.map((option) => option.value)}
            selectedOptions={props.selected}
            options={results}
            onChange={handleChange}
            inputValue={inputValue}
            onInputChange={setInputValue}
            loading={loading}
            loadingMessage={intl.formatMessage({defaultMessage: 'Searching…'})}
            emptyMessage={intl.formatMessage({defaultMessage: 'No results'})}
            placeholder={props.placeholder}
            aria-label={props.placeholder}
            disabled={props.disabled}
            portalContainer={getPortalTarget()}
            zIndex={PORTALED_MENU_Z_INDEX}
        />
    );
};

const TEAM_KIND = 'team';

const userOption = (user: UserProfile): ComboboxOption => ({
    value: user.id,
    label: user.username,
    leadingAvatar: {src: getProfilePictureUrl(user.id, user.last_picture_update), alt: user.username},
});

const teamOption = (team: Team, teamLabel: string): ComboboxOption => ({
    value: team.id,
    label: team.display_name,
    secondaryLabel: teamLabel,

    // Teams without a custom icon have no image to load, so they get a generic glyph.
    ...(team.last_team_icon_update ? {
        leadingAvatar: {src: getTeamIconUrl(team.id, team.last_team_icon_update), alt: team.display_name},
    } : {
        leadingVisual: <Icon glyph={<AccountMultipleOutlineIcon/>}/>,
    }),
});

type SelectUserProps = {
    userIDs: string[];
    teamIDs: string[];
    onChangeIDs: (userIds: string[], teamIds: string[]) => void;
    disabled?: boolean;
};

export const SelectUser = (props: SelectUserProps) => {
    const intl = useIntl();
    const teamLabel = intl.formatMessage({defaultMessage: 'Team'});
    const [selected, setSelected] = useState<ComboboxOption[]>([]);
    const teamIDs = useRef(new Set<string>());

    useEffect(() => {
        const loadSelected = async () => {
            const [users, teams] = await Promise.all([
                getProfilesByIds(props.userIDs),
                getTeamsByIds(props.teamIDs).then((found) => found.filter(Boolean)),
            ]);
            teams.forEach((team) => teamIDs.current.add(team.id));
            setSelected([...users.map(userOption), ...teams.map((team) => teamOption(team, teamLabel))]);
        };

        loadSelected();
    }, [props.userIDs, props.teamIDs, teamLabel]);

    const search = async (term: string) => {
        const [users, teams] = await Promise.all([
            getAutocompleteAllUsers(term),
            searchTeams(term),
        ]);
        teams.forEach((team) => teamIDs.current.add(team.id));
        return [
            ...users.users.filter((user: UserProfile) => !user.is_bot).map(userOption),
            ...teams.map((team) => teamOption(team, teamLabel)),
        ];
    };

    const handleChange = (options: ComboboxOption[]) => {
        const kinds = options.map((option) => (teamIDs.current.has(option.value) ? TEAM_KIND : 'user'));
        props.onChangeIDs(
            options.filter((_, i) => kinds[i] !== TEAM_KIND).map((option) => option.value),
            options.filter((_, i) => kinds[i] === TEAM_KIND).map((option) => option.value),
        );
    };

    return (
        <AsyncMultiPicker
            selected={selected}
            search={search}
            onChange={handleChange}
            placeholder={intl.formatMessage({defaultMessage: 'Search for people or teams'})}
            disabled={props.disabled}
        />
    );
};

const channelOption = (channel: ChannelWithTeamData): ComboboxOption => ({
    value: channel.id,
    label: channel.display_name,
    secondaryLabel: channel.team_display_name,
    leadingVisual: <Icon glyph={channel.type === 'O' ? <GlobeIcon/> : <LockIcon/>}/>,
});

type SelectChannelProps = {
    channelIDs: string[];
    onChangeChannelIDs: (channelIds: string[]) => void;
    disabled?: boolean;
};

export const SelectChannel = (props: SelectChannelProps) => {
    const intl = useIntl();
    const [selected, setSelected] = useState<ComboboxOption[]>([]);

    useEffect(() => {
        const loadSelected = async () => {
            if (props.channelIDs.length === 0) {
                setSelected([]);
                return;
            }
            const channels = await Promise.all(props.channelIDs.map((id) => getChannelById(id)));
            setSelected(channels.map(channelOption));
        };
        loadSelected();
    }, [props.channelIDs]);

    const search = async (term: string) => {
        const channels = await searchAllChannels(term);
        return channels.map(channelOption);
    };

    return (
        <AsyncMultiPicker
            selected={selected}
            search={search}
            onChange={(options) => props.onChangeChannelIDs(options.map((option) => option.value))}
            placeholder={intl.formatMessage({defaultMessage: 'Search for channels'})}
            disabled={props.disabled}
        />
    );
};
