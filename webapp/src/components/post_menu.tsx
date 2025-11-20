// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import {Post} from '@mattermost/types/posts';

import styled from 'styled-components';

import {doReaction, doThreadAnalysis} from '../client';

import {useSelectPost} from '@/hooks';

import {useIsBasicsLicensed} from '@/license';

import {useBotlistForChannel} from '@/bots';

import IconAI from './assets/icon_ai';
import IconReactForMe from './assets/icon_react_for_me';
import IconSparkleCheckmark from './assets/icon_sparkle_checkmark';
import IconSparkleQuestion from './assets/icon_sparkle_question';
import DotMenu, {DropdownMenu, DropdownMenuItem} from './dot_menu';
import IconThreadSummarization from './assets/icon_thread_summarization';
import {Divider, DropdownInfoOnlyVisibleToYou} from './dropdown_info';
import {DropdownBotSelector} from './bot_selector';

type Props = {
    post: Post,
}

const PostMenu = (props: Props) => {
    const selectPost = useSelectPost();
    const intl = useIntl();
    const {bots, activeBot, setActiveBot} = useBotlistForChannel(props.post.channel_id);
    const post = props.post;
    const isBasicsLicensed = useIsBasicsLicensed();

    const analyzeThread = async (postId: string, analysisType: string) => {
        const result = await doThreadAnalysis(postId, analysisType, activeBot?.username || '');
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
        <DotMenu
            icon={<IconAI/>}
            title={intl.formatMessage({defaultMessage: 'AI Actions'})}
            dropdownMenu={StyledDropdownMenu}
            testId='ai-actions-menu'
        >
            <DropdownBotSelector
                bots={bots ?? []}
                activeBot={activeBot}
                setActiveBot={setActiveBot}
            />
            <Divider/>
            <DropdownMenuItem onClick={() => analyzeThread(post.id, 'summarize_thread')}>
                <span className='icon'><IconThreadSummarization/></span>
                <FormattedMessage defaultMessage='Summarize Thread'/>
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => analyzeThread(post.id, 'action_items')}>
                <span className='icon'><IconSparkleCheckmarkStyled/></span>
                <FormattedMessage defaultMessage='Find action items'/>
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => analyzeThread(post.id, 'open_questions')}>
                <span className='icon'><IconSparkleQuestionStyled/></span>
                <FormattedMessage defaultMessage='Find open questions'/>
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => doReaction(post.id)}>
                <span className='icon'><IconReactForMe/></span>
                <FormattedMessage defaultMessage='React for me'/>
            </DropdownMenuItem>
            <Divider/>
            <DropdownInfoOnlyVisibleToYou/>
        </DotMenu>
    );
};

const IconSparkleCheckmarkStyled = styled(IconSparkleCheckmark)`
	color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const IconSparkleQuestionStyled = styled(IconSparkleQuestion)`
	color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const StyledDropdownMenu = styled(DropdownMenu)`
	min-width: 240px;
`;

export default PostMenu;
