// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

import {SelectUser, SelectChannel} from '../select';

import {ChannelAccessLevel, UserAccessLevel} from './bot';

import {HelpText, ItemLabel, StyledRadio} from './item';

const AllowTypes = styled.div`
	margin-bottom: 24px;
	display: grid;
	grid-template-columns: auto 1fr;
	grid-row-gap: 8px;
	grid-column-gap: 8px;
	grid-align-items: center;
`;

const MainContainer = styled.div`
`;

const SelectWrapper = styled.div`
    margin-top: 8px;
    width: 90%;
`;

type UserAccessLevelProps = {
    label: string;
    level: UserAccessLevel;
    onChangeLevel: (level: UserAccessLevel) => void;
    userIDs: string[];
    teamIDs: string[];
    onChangeIDs: (userIds: string[], teamIds: string[]) => void;
};

export const UserAccessLevelItem = (props: UserAccessLevelProps) => {
    return (
        <>
            <ItemLabel>{props.label}</ItemLabel>
            <MainContainer>
                <AllowTypes>
                    <StyledRadio
                        type='radio'
                        value={UserAccessLevel.All}
                        checked={props.level === UserAccessLevel.All}
                        onChange={() => props.onChangeLevel(UserAccessLevel.All)}
                    />
                    <FormattedMessage defaultMessage='Allow for all users'/>
                    <StyledRadio
                        type='radio'
                        value={UserAccessLevel.Allow}
                        checked={props.level === UserAccessLevel.Allow}
                        onChange={() => props.onChangeLevel(UserAccessLevel.Allow)}
                    />
                    <FormattedMessage defaultMessage='Allow for selected users'/>
                    <StyledRadio
                        type='radio'
                        value={UserAccessLevel.Block}
                        checked={props.level === UserAccessLevel.Block}
                        onChange={() => props.onChangeLevel(UserAccessLevel.Block)}
                    />
                    <FormattedMessage defaultMessage='Block selected users'/>
                </AllowTypes>
                {props.level !== UserAccessLevel.All && (
                    <SelectWrapper>
                        <ItemLabel>
                            {props.level === UserAccessLevel.Allow ? 'Allow list' : 'Block list'}
                        </ItemLabel>
                        <SelectUser
                            userIDs={props.userIDs}
                            teamIDs={props.teamIDs}
                            onChangeIDs={props.onChangeIDs}
                        />
                        <HelpText>
                            {props.level === UserAccessLevel.Allow ? (
                                <FormattedMessage defaultMessage='Enter users to allow for this bot'/>
                            ) : (
                                <FormattedMessage defaultMessage='Enter users to block for this bot'/>
                            )}
                        </HelpText>
                    </SelectWrapper>
                )}
            </MainContainer>
        </>
    );
};

type ChannelAccessLevelProps = {
    label: string;
    level: ChannelAccessLevel;
    onChangeLevel: (level: ChannelAccessLevel) => void;
    channelIDs: string[];
    onChangeChannelIDs: (channelIDs: string[]) => void;
};

export const ChannelAccessLevelItem = (props: ChannelAccessLevelProps) => {
    return (
        <>
            <ItemLabel>{props.label}</ItemLabel>
            <MainContainer>
                <AllowTypes>
                    <StyledRadio
                        type='radio'
                        value={ChannelAccessLevel.All}
                        checked={props.level === ChannelAccessLevel.All}
                        onChange={() => props.onChangeLevel(ChannelAccessLevel.All)}
                    />
                    <FormattedMessage defaultMessage='Allow for all channels'/>
                    <StyledRadio
                        type='radio'
                        value={ChannelAccessLevel.Allow}
                        checked={props.level === ChannelAccessLevel.Allow}
                        onChange={() => props.onChangeLevel(ChannelAccessLevel.Allow)}
                    />
                    <FormattedMessage defaultMessage='Allow for selected channels'/>
                    <StyledRadio
                        type='radio'
                        value={ChannelAccessLevel.Block}
                        checked={props.level === ChannelAccessLevel.Block}
                        onChange={() => props.onChangeLevel(ChannelAccessLevel.Block)}
                    />
                    <FormattedMessage defaultMessage='Block selected channels'/>
                    <StyledRadio
                        type='radio'
                        value={ChannelAccessLevel.None}
                        checked={props.level === ChannelAccessLevel.None}
                        onChange={() => props.onChangeLevel(ChannelAccessLevel.None)}
                    />
                    <FormattedMessage defaultMessage='Block all channels'/>
                </AllowTypes>
                {(props.level === ChannelAccessLevel.Allow || props.level === ChannelAccessLevel.Block) && (
                    <SelectWrapper>
                        <ItemLabel>
                            {props.level === ChannelAccessLevel.Allow ? 'Allow list' : 'Block list'}
                        </ItemLabel>
                        <SelectChannel
                            channelIDs={props.channelIDs}
                            onChangeChannelIDs={props.onChangeChannelIDs}
                        />
                        <HelpText>
                            {props.level === ChannelAccessLevel.Allow ? (
                                <FormattedMessage defaultMessage='Enter channels to allow for this bot'/>
                            ) : (
                                <FormattedMessage defaultMessage='Enter channels to block for this bot'/>
                            )}
                        </HelpText>
                    </SelectWrapper>
                )}
            </MainContainer>
        </>
    );
};

