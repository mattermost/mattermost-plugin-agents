// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useEffect, useCallback} from 'react';
import styled from 'styled-components';
import AsyncSelect from 'react-select/async';
import {StylesConfig, MultiValue} from 'react-select';
import {LockIcon, GlobeIcon} from '@mattermost/compass-icons/components';

import {UserProfile} from '@mattermost/types/users';
import {ChannelType, ChannelWithTeamData} from '@mattermost/types/channels';

import {getAutocompleteAllUsers, getChannelById, getProfilePictureUrl, getProfilesByIds, getTeamIconUrl, getTeamsByIds, searchAllChannels, searchTeams} from '../client';

function selectMenuPortalTarget(): HTMLElement | null {
    if (typeof document === 'undefined') {
        return null;
    }

    // Prefer #root so portaled menus inherit Mattermost theme CSS variables (same as the main app).
    return document.getElementById('root') ?? document.body;
}

type Option = {
    value: string;
    label: string;
};

type TeamOption = Option & {
    isTeam: true;
    displayName: string;
    icon?: string;
};

type UserOption = Option & {
    isTeam?: false;
    avatar: string;
};

type UserOrTeamOption = UserOption | TeamOption;

type ChannelOption = Option & {
    type: ChannelType;
    teamName: string;
};

type SelectProps<T extends Option> = {
    value: T[];
    onChange: (newValue: MultiValue<T>) => void;
    loadOptions: (inputValue: string) => Promise<T[]>;
    formatOptionLabel: (option: T) => React.ReactNode;
    placeholder: string;
};

const LabelContainer = styled.div`
    display: flex;
    align-items: center;
    gap: 5px;
    font-size: 14px;
`;

const Avatar = styled.img`
    width: 20px;
    height: 20px;
    border-radius: 50%;
    margin-right: 8px;
`;

const ChannelName = styled.span`
    font-weight: 600;
`;

const TeamName = styled.span`
    color: rgba(var(--center-channel-color-rgb), 0.56);
    font-weight: normal;
    margin-left: 5px;
`;

const ChannelIcon = styled.span`
    margin-right: 8px;
    display: flex;
    align-items: center;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

function SelectComponent<T extends Option>(props: SelectProps<T>) {
    const loadOptions = async (inputValue: string) => {
        return props.loadOptions(inputValue);
    };

    const selectStyles: StylesConfig<T, true> = {
        control: (base, state) => ({
            ...base,
            minHeight: '38px',
            backgroundColor: 'var(--center-channel-bg)',
            borderColor: state.isFocused ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.16)',
            boxShadow: state.isFocused ? 'none' : '0px 1px 1px rgba(0, 0, 0, 0.075) inset',
            '&:hover': {
                borderColor: state.isFocused ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.16)',
            },
        }),
        valueContainer: (base) => ({
            ...base,
            padding: '2px 8px',
        }),
        placeholder: (base) => ({
            ...base,
            color: 'rgba(var(--center-channel-color-rgb), 0.48)',
        }),
        input: (base) => ({
            ...base,
            margin: '0',
            color: 'var(--center-channel-color)',
        }),
        indicatorSeparator: () => ({
            display: 'none',
        }),
        dropdownIndicator: (base) => ({
            ...base,
            padding: '4px',
            color: 'rgba(var(--center-channel-color-rgb), 0.56)',
            '&:hover': {
                color: 'rgba(var(--center-channel-color-rgb), 0.72)',
            },
        }),
        loadingIndicator: (base) => ({
            ...base,
            color: 'rgba(var(--center-channel-color-rgb), 0.56)',
        }),
        multiValue: (base) => ({
            ...base,
            backgroundColor: 'rgba(var(--center-channel-color-rgb), 0.08)',
            borderRadius: '16px',
        }),
        multiValueLabel: (base) => ({
            ...base,
            color: 'var(--center-channel-color)',
        }),
        multiValueRemove: (base) => ({
            ...base,
            color: 'rgba(var(--center-channel-color-rgb), 0.56)',
            cursor: 'pointer',
            borderRadius: '50%',
            padding: '0',
            margin: '5px',
            '&:hover': {
                backgroundColor: 'rgba(var(--center-channel-color-rgb), 0.08)',
                color: 'rgba(var(--center-channel-color-rgb), 0.72)',
            },
        }),
        menu: (base) => ({
            ...base,
            backgroundColor: 'var(--center-channel-bg)',
            border: '1px solid rgba(var(--center-channel-color-rgb), 0.16)',
            borderRadius: '4px',
            boxShadow: '0 4px 12px rgba(0, 0, 0, 0.15)',
        }),
        menuList: (base) => ({
            ...base,
            backgroundColor: 'var(--center-channel-bg)',
        }),
        option: (base, state) => {
            let backgroundColor = 'transparent';
            if (state.isSelected) {
                backgroundColor = 'rgba(var(--center-channel-color-rgb), 0.12)';
            } else if (state.isFocused) {
                backgroundColor = 'rgba(var(--center-channel-color-rgb), 0.08)';
            }
            return {
                ...base,
                backgroundColor,
                color: 'var(--center-channel-color)',
            };
        },
        menuPortal: (base) => ({
            ...base,
            zIndex: 10000,
        }),
    };

    return (
        <AsyncSelect<T, true>
            isMulti={true}
            isClearable={false}
            value={props.value}
            onChange={props.onChange}
            loadOptions={loadOptions}
            formatOptionLabel={props.formatOptionLabel}
            placeholder={props.placeholder}
            menuPortalTarget={selectMenuPortalTarget()}
            menuPosition='fixed'
            styles={selectStyles}
            defaultOptions={true}
        />
    );
}

type SelectUserProps = {
    userIDs: string[];
    teamIDs: string[];
    onChangeIDs: (userIds: string[], teamIds: string[]) => void;
};

export const SelectUser = (props: SelectUserProps) => {
    const [selectedOptions, setSelectedOptions] = useState<UserOrTeamOption[]>([]);

    useEffect(() => {
        const loadSelectedOptions = async () => {
            const [users, teams] = await Promise.all([
                getProfilesByIds(props.userIDs),
                getTeamsByIds(props.teamIDs).then((teams) => teams.filter(Boolean)),
            ]);

            const userOptions = users.map((user: UserProfile) => ({
                value: user.id,
                label: user.username,
                avatar: getProfilePictureUrl(user.id, user.last_picture_update),
                isTeam: false as const,
            }));

            const teamOptions = teams.map((team) => ({
                value: team.id,
                label: team.name,
                displayName: team.display_name,
                icon: getTeamIconUrl(team.id, team.update_at),
                isTeam: true as const,
            }));

            setSelectedOptions([...userOptions, ...teamOptions]);
        };

        loadSelectedOptions();
    }, [props.userIDs, props.teamIDs]);

    const loadOptions = async (inputValue: string) => {
        const [users, teams] = await Promise.all([
            getAutocompleteAllUsers(inputValue),
            searchTeams(inputValue),
        ]);

        const userOptions = users.users.
            filter((user: UserProfile) => !user.is_bot).
            map((user: UserProfile) => ({
                value: user.id,
                label: user.username,
                avatar: getProfilePictureUrl(user.id, user.last_picture_update),
                isTeam: false as const,
            }));

        const teamOptions = teams.map((team) => ({
            value: team.id,
            label: team.name,
            displayName: team.display_name,
            icon: getTeamIconUrl(team.id, team.update_at),
            isTeam: true as const,
        }));

        return [...userOptions, ...teamOptions];
    };

    const TeamOptionLabel = ({option}: {option: TeamOption}) => {
        const [showAvatar, setShowAvatar] = useState(true);

        const handleImageError = useCallback(() => {
            setShowAvatar(false);
        }, []);

        return (
            <LabelContainer>
                {showAvatar && option.icon && (
                    <Avatar
                        src={option.icon}
                        onError={handleImageError}
                    />
                )}
                <span>{option.displayName}</span>
                <TeamIndicator>{'TEAM'}</TeamIndicator>
            </LabelContainer>
        );
    };

    const formatOptionLabel = (option: UserOrTeamOption) => {
        if (option.isTeam) {
            return <TeamOptionLabel option={option}/>;
        }

        return (
            <LabelContainer>
                <Avatar src={option.avatar}/>
                {option.label}
            </LabelContainer>
        );
    };

    const handleChange = (newValue: MultiValue<UserOrTeamOption>) => {
        const userIds: string[] = [];
        const teamIds: string[] = [];

        newValue.forEach((option) => {
            if (option.isTeam) {
                teamIds.push(option.value);
            } else {
                userIds.push(option.value);
            }
        });

        props.onChangeIDs(userIds, teamIds);
    };

    return (
        <SelectComponent<UserOrTeamOption>
            value={selectedOptions}
            onChange={handleChange}
            loadOptions={loadOptions}
            formatOptionLabel={formatOptionLabel}
            placeholder='Search for people or teams'
        />
    );
};

type SelectChannelProps = {
    channelIDs: string[];
    onChangeChannelIDs: (channelIds: string[]) => void;
};

export const SelectChannel = (props: SelectChannelProps) => {
    const [selectedOptions, setSelectedOptions] = useState<ChannelOption[]>([]);

    useEffect(() => {
        const loadSelectedOptions = async () => {
            if (props.channelIDs.length > 0) {
                const channels = await Promise.all(props.channelIDs.map((id) => getChannelById(id)));
                const options = channels.map((channel: ChannelWithTeamData) => ({
                    value: channel.id,
                    label: channel.display_name,
                    type: channel.type,
                    teamName: channel.team_display_name,
                }));
                setSelectedOptions(options);
            } else {
                setSelectedOptions([]);
            }
        };
        loadSelectedOptions();
    }, [props.channelIDs]);

    const loadOptions = async (inputValue: string) => {
        const channels = await searchAllChannels(inputValue);
        return channels.map((channel: ChannelWithTeamData) => ({
            value: channel.id,
            label: channel.display_name,
            type: channel.type,
            teamName: channel.team_display_name,
        }));
    };

    const formatOptionLabel = (option: ChannelOption) => (
        <LabelContainer>
            <ChannelIcon>
                {option.type === 'O' ? <GlobeIcon size={16}/> : <LockIcon size={16}/>}
            </ChannelIcon>
            <ChannelName>{option.label}</ChannelName>
            <TeamName>
                {'('}{option.teamName}{')'}
            </TeamName>
        </LabelContainer>
    );

    return (
        <SelectComponent<ChannelOption>
            value={selectedOptions}
            onChange={(newValue) => props.onChangeChannelIDs(newValue.map((option) => option.value))}
            loadOptions={loadOptions}
            formatOptionLabel={formatOptionLabel}
            placeholder='Search for channels'
        />
    );
};
const TeamIndicator = styled.span`
    margin-left: 8px;
    padding: 2px 4px;
    background: rgba(var(--center-channel-color-rgb), 0.08);
    border-radius: 4px;
    font-size: 10px;
    font-weight: 600;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;
