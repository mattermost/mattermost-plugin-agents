// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import styled from 'styled-components';

import {ChevronDownIcon} from '@mattermost/compass-icons/components';

import {Button} from '@mattermost/compass-ui/components/button';
import {MenuGroupHeading} from '@mattermost/compass-ui/components/menu-group-heading';
import {UserAvatar} from '@mattermost/compass-ui/components/user-avatar';

import {LLMBot} from '@/bots';

import {getProfilePictureUrl} from '@/client';

import {AGENTS_ROUTE} from './agents/agents_page';
import DotMenu, {DropdownMenu, DropdownMenuItem} from './dot_menu';

type DropdownBotSelectorProps = {
    bots: LLMBot[]
    activeBot: LLMBot | null
    setActiveBot: (bot: LLMBot) => void
}

export const DropdownBotSelector = (props: DropdownBotSelectorProps) => {
    return (
        <BotDropdown
            bots={props.bots}
            activeBot={props.activeBot}
            setActiveBot={props.setActiveBot}
            container={BotSelectorContainer}
        >
            <>
                <SelectMessage>
                    <FormattedMessage defaultMessage='Generate With:'/>
                </SelectMessage>
                <BotPill>
                    <BotPillName>{props.activeBot?.displayName}</BotPillName>
                    <ChevronDownIcon/>
                </BotPill>
            </>
        </BotDropdown>
    );
};

const BotPill = styled.div`
	display: flex;
	align-items: center;
	border-radius: 4px;
	font-size: 12px;
	font-weight: 600;
	line-height: 16px;
	padding: 2px 6px;
	max-width: 128px;
	color: var(--center-channel-color);
	background: rgba(var(--center-channel-color-rgb), 0.08);

	svg {
		flex-shrink: 0;
	}
`;

const BotPillName = styled.span`
	overflow: hidden;
	text-overflow: ellipsis;
	white-space: nowrap;
	min-width: 0;
`;

export const BotSelectorContainer = styled.div`
	display: flex;
	flex-direction: row;
	align-items: center;
	gap: 8px;

	margin: 8px 16px;
	color: rgba(var(--center-channel-color-rgb), 0.56);
`;

type BotDropdownProps = {
    bots: LLMBot[]
    activeBot: LLMBot | null
    setActiveBot: (bot: LLMBot) => void
    container: React.ReactNode
    children: React.ReactNode
    testId?: string
}

export const BotDropdown = (props: BotDropdownProps) => {
    const intl = useIntl();
    return (
        <DotMenu
            icon={props.children}
            title={props.activeBot?.displayName}
            dotMenuButton={props.container}
            dropdownMenu={StyledDropdownMenu}
            testId={props.testId}
        >
            <MenuHeader>
                <MenuGroupHeading label={intl.formatMessage({defaultMessage: 'Choose an Agent'})}/>
                <Button
                    emphasis='link'
                    size='x-small'
                    onClick={(e) => {
                        e.preventDefault();
                        if (window.WebappUtils?.browserHistory?.push) {
                            window.WebappUtils.browserHistory.push(AGENTS_ROUTE);
                            return;
                        }
                        window.location.assign(AGENTS_ROUTE);
                    }}
                >
                    <FormattedMessage defaultMessage='Manage'/>
                </Button>
            </MenuHeader>
            <BotList>
                {props.bots.map((bot) => {
                    return (
                        <DropdownMenuItem
                            key={bot.displayName}
                            label={bot.displayName}
                            leading={(
                                <UserAvatar
                                    src={getProfilePictureUrl(bot.id, bot.lastIconUpdate)}
                                    alt={bot.displayName}
                                    size='24'
                                />
                            )}
                            selected={props.activeBot?.id === bot.id}
                            onClick={() => {
                                props.setActiveBot(bot);
                            }}
                        />
                    );
                })}
            </BotList>
        </DotMenu>
    );
};

const StyledDropdownMenu = styled(DropdownMenu)`
	min-width: 270px;
	max-height: 400px;
	display: flex;
	flex-direction: column;
	overflow: hidden;
`;

const BotList = styled.div`
	display: flex;
	flex-direction: column;
	overflow-y: auto;
	flex: 1 1 auto;
	min-height: 0;
`;

const MenuHeader = styled.div`
	display: flex;
	flex-direction: row;
	align-items: center;
	justify-content: space-between;
	gap: 8px;
	padding-right: 16px;
`;

const SelectMessage = styled.div`
	font-size: 12px;
	font-weight: 600;
	line-height: 16px;
	letter-spacing: 0.24px;
	text-transform: uppercase;
`;
