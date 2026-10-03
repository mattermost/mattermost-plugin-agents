// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage} from 'react-intl';
import styled, {css} from 'styled-components';
import {LightbulbOutlineIcon, ExclamationThickIcon} from '@mattermost/compass-icons/components';

import {PopoverMenuDivider} from '@mattermost/compass-ui/components/popover-menu';

const DropdownMenuItemInfo = styled.div`
	display: flex;
	align-items: flex-start;
	gap: 8px;

	font-size: 12px;
	font-weight: 400;
	line-height: 16px;
	color: rgba(var(--center-channel-color-rgb), 0.72);

	max-width: 240px;
	padding: 8px 16px;
`;

const iconStyling = css`
	min-width: 22px;
	min-height: 22px;

	padding: 4px;

	color: rgba(var(--center-channel-color-rgb), 0.56);
	background: rgba(var(--center-channel-color-rgb), 0.08);
	border-radius: 16px;
`;

const LightbulbOutlineIconStyled = styled(LightbulbOutlineIcon)`
	${iconStyling}
`;

const ExclamationThickIconStyled = styled(ExclamationThickIcon)`
	${iconStyling}
`;

export const Divider = PopoverMenuDivider;

export const DropdownInfoOnlyVisibleToYou = () => {
    return (
        <DropdownMenuItemInfo>
            <LightbulbOutlineIconStyled/>
            <FormattedMessage defaultMessage='Agents post responses in the right panel which will only be visible to you.'/>
        </DropdownMenuItemInfo>
    );
};

export const DropdownChannelBlocked = () => {
    return (
        <DropdownMenuItemInfo>
            <ExclamationThickIconStyled/>
            <FormattedMessage defaultMessage="Sorry, this channel has been blocked from Agents's AI features"/>
        </DropdownMenuItemInfo>
    );
};
