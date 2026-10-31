// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage} from 'react-intl';

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
    floatingClassName?: string
}

type BotMenuContentProps = {
    bots: LLMBot[]
    activeBot: LLMBot | null
    setActiveBot: (bot: LLMBot) => void

    /** Role of the agent rows; buttons by default, 'menuitem' inside a role='menu' popup. */
    itemRole?: 'menuitem'
}

/** The "Choose an Agent" header and agent list, shared by every agent selector menu. */
export const BotMenuContent = (props: BotMenuContentProps) => {
    return (
        <>
            <MenuHeader>
                <MenuGroupHeading label={<FormattedMessage defaultMessage='Choose an Agent'/>}/>
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
                            role={props.itemRole}
                            onClick={() => {
                                props.setActiveBot(bot);
                            }}
                        />
                    );
                })}
            </BotList>
        </>
    );
};

export const BotDropdown = (props: BotDropdownProps) => {
    return (
        <DotMenu
            floatingClassName={props.floatingClassName}
            icon={props.children}
            title={props.activeBot?.displayName}
            dotMenuButton={props.container}
            dropdownMenu={BotDropdownMenu}
            testId={props.testId}
        >
            <BotMenuContent
                bots={props.bots}
                activeBot={props.activeBot}
                setActiveBot={props.setActiveBot}
            />
        </DotMenu>
    );
};

export const BotDropdownMenu = styled(DropdownMenu)`
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

	/* MenuItem's row is top-aligned for a 16px icon. These rows lead with a
	   24px avatar, so center the label and check with it. */
	button[class*='menu-item'] [class*='menu-item__content'] {
		align-items: center;
	}
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
