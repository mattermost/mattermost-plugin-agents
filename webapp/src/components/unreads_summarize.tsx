// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

import {useSelectPost} from '@/hooks';

import {getChannelInterval} from '@/client';
import {useIsBasicsLicensed} from '@/license';

import {useBotlistForChannel} from '@/bots';

import IconAI from './assets/icon_ai';
import IconSparkleCheckmark from './assets/icon_sparkle_checkmark';
import IconSparkleQuestion from './assets/icon_sparkle_question';
import IconThreadSummarization from './assets/icon_thread_summarization';

import DotMenu, {DropdownMenu, DropdownMenuItem} from './dot_menu';
import {Divider, DropdownInfoOnlyVisibleToYou} from './dropdown_info';
import {DropdownBotSelector} from './bot_selector';

const AskAIButton = styled(DotMenu)`
	display: flex;
	height: 24px;
	align-items: center;
	align-self: center;
	gap: 6px;
	color: rgba(var(--new-message-separator-rgb), 1);
	background: rgba(var(--new-message-separator-rgb), 0.08);
	width: auto;
	padding: 4px 10px;
	margin-left: 4px;
	border-radius: 4px;
	pointer-events: auto;

	font-size: 11px;
	font-weight: 600;
	line-height: 16px;
	letter-spacing: 0.22px;

	&:hover {
		background: rgba(var(--new-message-separator-rgb), 0.12);
		color: rgba(var(--new-message-separator-rgb), 1);
	}

	&:active {
		background: rgba(var(--new-message-separator-rgb), 0.16);
		color: rgba(var(--new-message-separator-rgb), 1);
	}
`;

const SmallerIconAI = styled(IconAI)`
	width: 15px;
	height: 15px;
`;

const DropdownMenuItemStyled = styled(DropdownMenuItem)`
	display: flex;
	align-items: center;
	gap: 6px;
`;

const IconSparkleCheckmarkStyled = styled(IconSparkleCheckmark)`
	color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const IconSparkleQuestionStyled = styled(IconSparkleQuestion)`
	color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const StyledDropdownMenu = styled(DropdownMenu)`
	min-width: 240px;
`;

// ChannelID is undefined for threads view and threadID is undefined for channel view
interface Props {
    lastViewedAt: number;
    channelId: string;
    threadId: string;
}

const UnreadsSumarize = (props: Props) => {
    const selectPost = useSelectPost();
    const isBasicsLicensed = useIsBasicsLicensed();
    const {bots, activeBot, setActiveBot} = useBotlistForChannel(props.channelId);

    const summarizeNew = async () => {
        const result = await getChannelInterval(props.channelId, props.lastViewedAt, 0, 'summarize_unreads', '', activeBot?.username || '');
        selectPost(result.postid, result.channelid);
    };

    const actionItems = async () => {
        const result = await getChannelInterval(props.channelId, props.lastViewedAt, 0, 'action_items', '', activeBot?.username || '');
        selectPost(result.postid, result.channelid);
    };

    const openQuestions = async () => {
        const result = await getChannelInterval(props.channelId, props.lastViewedAt, 0, 'open_questions', '', activeBot?.username || '');
        selectPost(result.postid, result.channelid);
    };

    if (!isBasicsLicensed) {
        return null;
    }

    if (bots && bots.length === 0) {
        // No bots available (either unconfigured or filtered by permissions)
        return null;
    }

    return (
        <AskAIButton
            icon={<><SmallerIconAI/>
                <FormattedMessage defaultMessage=' Ask AI'/>
            </>}
            dropdownMenu={StyledDropdownMenu}
        >
            <DropdownBotSelector
                bots={bots ?? []}
                activeBot={activeBot}
                setActiveBot={setActiveBot}
            />
            <Divider/>
            <DropdownMenuItemStyled
                onClick={summarizeNew}
            >
                <IconThreadSummarization/>
                <FormattedMessage defaultMessage='Summarize new messages'/>
            </DropdownMenuItemStyled>
            <DropdownMenuItemStyled
                onClick={actionItems}
            >
                <IconSparkleCheckmarkStyled/>
                <FormattedMessage defaultMessage='Find action items'/>
            </DropdownMenuItemStyled>
            <DropdownMenuItemStyled
                onClick={openQuestions}
            >
                <IconSparkleQuestionStyled/>
                <FormattedMessage defaultMessage='Find open questions'/>
            </DropdownMenuItemStyled>
            <Divider/>
            <DropdownInfoOnlyVisibleToYou/>
        </AskAIButton>
    );
};

export default UnreadsSumarize;
