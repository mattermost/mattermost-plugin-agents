// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {ChevronDownIcon} from '@mattermost/compass-icons/components';
import React from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

import {DotMenuButton} from '../dot_menu';

import {BotDropdown} from '../bot_selector';

import {LLMBot} from '@/bots';

import {Button} from './common';
import ToolProviderPopover, {UserMCPServerInfo} from './tool_provider_popover';

type Props = {
    currentTab: string
    bots: LLMBot[] | null
    activeBot: LLMBot | null
    setCurrentTab: (tab: string) => void
    selectPost: (postId: string) => void
    setActiveBot: (bot: LLMBot) => void
    disabledServers: string[]
    onDisabledServersChange: (servers: string[]) => void
    preloadedServers?: UserMCPServerInfo[]
}

const RHSHeader = (props: Props) => {
    let historyButton = null;
    if (props.currentTab === 'threads') {
        historyButton = (
            <ButtonDisabled>
                <i className='icon-clock-outline'/>
                <FormattedMessage defaultMessage='Chat history'/>
            </ButtonDisabled>
        );
    } else {
        historyButton = (
            <HistoryButton
                data-testid='chat-history'
                onClick={() => {
                    props.setCurrentTab('threads');
                    props.selectPost('');
                }}
            >
                <i className='icon-clock-outline'/>
                <FormattedMessage defaultMessage='View chat history'/>
            </HistoryButton>
        );
    }
    const currentBotName = props.activeBot?.displayName ?? '';
    return (
        <Header>
            {historyButton}
            {props.currentTab !== 'new' && (
                <NewChatButton
                    data-testid='new-chat'
                    className='new-button'
                    onClick={() => {
                        props.setCurrentTab('new');
                        props.selectPost('');
                    }}
                >
                    <i className='icon icon-pencil-outline'/>
                    <FormattedMessage defaultMessage='New chat'/>
                </NewChatButton>
            )}
            {props.currentTab === 'new' && (
                <RightControls>
                    <ToolProviderPopover
                        disabledServers={props.disabledServers}
                        onDisabledServersChange={props.onDisabledServersChange}
                        preloadedServers={props.preloadedServers}
                        enabledMCPTools={props.activeBot?.enabledMCPTools}
                        autoEnableNewMCPTools={props.activeBot?.autoEnableNewMCPTools}
                    />
                    {props.bots && (
                        <BotDropdown
                            bots={props.bots}
                            activeBot={props.activeBot}
                            setActiveBot={props.setActiveBot}
                            container={SelectorDropdown}
                            testId='bot-selector-rhs'
                        >
                            <>
                                {currentBotName}
                                <ChevronDownIcon/>
                            </>
                        </BotDropdown>
                    )}
                </RightControls>
            )}
        </Header>
    );
};

const HistoryButton = styled(Button)`
    height: 28px;
    padding: 8px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const ButtonDisabled = styled(Button)`
    height: 28px;
    padding: 8px;
	&:hover {
		background: transparent;
		color: rgb(var(--center-channel-color));
		cursor: unset;
	}
`;

const NewChatButton = styled(Button)`
    padding: 6px 12px;
	color: rgb(var(--link-color-rgb));
	&:hover {
		color: rgb(var(--link-color-rgb));
        background-color: rgba(var(--button-bg-rgb), 0.08);
	}

	&:active {
		background-color: rgba(var(--button-bg-rgb), 0.12);
	}
`;

const Header = styled.div`
    display: flex;
    height: 38px;
    padding: 0 16px 0 12px;
	justify-content: space-between;
	align-items: center;
    box-sizing: border-box;
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    flex-wrap: wrap;
`;

const RightControls = styled.div`
    display: flex;
    align-items: center;
    gap: 4px;
    margin-left: auto;
`;

const SelectorDropdown = styled(DotMenuButton)<{isActive: boolean}>`
	display: flex;
	align-items: center;
	padding: 2px 4px 2px 6px;
	border-radius: 4px;
	height: 20px;
	width: auto;
	max-width: 145px;
	overflow: ellipsis;

	font-size: 11px;
	font-weight: 600;
	line-height: 16px;

    color: ${(props) => (props.isActive ? 'var(--button-bg)' : 'var(--center-channel-color-rgb)')};
    background-color: ${(props) => (props.isActive ? 'rgba(var(--button-bg-rgb), 0.16)' : 'rgba(var(--center-channel-color-rgb), 0.08)')};

    &:hover {
        color: ${(props) => (props.isActive ? 'var(--button-bg)' : 'var(--center-channel-color-rgb)')};
        background-color: ${(props) => (props.isActive ? 'rgba(var(--button-bg-rgb), 0.16)' : 'rgba(var(--center-channel-color-rgb), 0.16)')};
    }
`;

export default React.memo(RHSHeader);
