// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';

import {Timestamp} from '@/mm_webapp';

import {GrayPill} from '../pill';

const ThreadItemContainer = styled.div`
    padding: 16px;
    cursor: pointer;
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.12)
`;

const Title = styled.div`
    color: var(--center-channel-color);
    display: flex;
    align-items: center;
    margin-bottom: 4px;
    justify-content: space-between;
`;

const TitleText = styled.div`
    font-size: 14px;
    font-weight: 600;
    text-overflow: ellipsis;
    overflow: hidden;
    white-space: nowrap;
`;

const TurnCount = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.64);
    font-weight: 600;
`;

const LastActivityDate = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.64);
    font-size: 12px;
    font-weight: 400;
    white-space: nowrap;
    margin-left: 13px;
`;

const Label = styled(GrayPill)`
	padding: 0 4px;
	font-size: 10px;
	font-weight: 600;
	line-height: 16px;
`;

const Footer = styled.div`
	display: flex;
	flex-direction: row;
	gap: 10px;
	margin-top: 12px;
`;

type Props = {
    postTitle: string;
    turnCount: number;
    lastActivityDate: number;
    label: string;
    onClick: () => void;
}

const DefaultTitle = 'Conversation with Agents';

export default function ThreadItem(props: Props) {
    const turnText = props.turnCount === 1 ? '1 message' : `${props.turnCount} messages`;
    return (
        <ThreadItemContainer onClick={props.onClick}>
            <Title>
                <TitleText>{props.postTitle || DefaultTitle}</TitleText>
                <LastActivityDate>
                    <Timestamp // Matches the timestap format in the threads view
                        value={props.lastActivityDate}
                        units={['now', 'minute', 'hour', 'day', 'week']}
                        useTime={false}
                        day={'numeric'}
                    />
                </LastActivityDate>
            </Title>
            <Footer>
                <Label>{props.label}</Label>
                <TurnCount>{turnText}</TurnCount>
            </Footer>
        </ThreadItemContainer>
    );
}
