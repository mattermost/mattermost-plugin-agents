// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {useDispatch, useSelector} from 'react-redux';

import {PinOutlineIcon} from '@mattermost/compass-icons/components';
import {Post} from '@mattermost/types/posts';
import {GlobalState} from '@mattermost/types/store';

import styled from 'styled-components';

import {doReaction, doThreadAnalysis, pinChannelContextPost, unpinChannelContextPost} from '../client';

import {useSelectPost} from '@/hooks';

import {useIsLicensedFor} from '@/license';

import {useBotlistForChannel} from '@/bots';

import {
    canManageChannelContext,
    fetchChannelContextPosts,
    isChannelContextPinned,
    receivedChannelContextPosts,
    useChannelContextPosts,
} from '@/channel_context';

import IconAI from './assets/icon_ai';
import IconReactForMe from './assets/icon_react_for_me';
import IconSparkleCheckmark from './assets/icon_sparkle_checkmark';
import IconSparkleQuestion from './assets/icon_sparkle_question';
import DotMenu, {DisabledDropdownMenuItemStyled, DropdownMenu, DropdownMenuItem} from './dot_menu';
import IconThreadSummarization from './assets/icon_thread_summarization';
import {Divider, DropdownInfoOnlyVisibleToYou} from './dropdown_info';
import {DropdownBotSelector} from './bot_selector';

type Props = {
    post: Post,
    handleDropdownOpened?: (open: boolean) => void,
}

const PostMenu = (props: Props) => {
    const selectPost = useSelectPost();
    const intl = useIntl();
    const {bots, activeBot, setActiveBot} = useBotlistForChannel(props.post.channel_id);
    const post = props.post;
    const threadSummarizationLicensed = useIsLicensedFor('thread_summarization');
    const dispatch = useDispatch();
    const canPinToContext = useSelector((state: GlobalState) => canManageChannelContext(state, state.entities.channels.channels[post.channel_id])) &&
        !post.type?.startsWith('system_');
    const contextPosts = useChannelContextPosts(post.channel_id);
    const contextPinned = isChannelContextPinned(contextPosts, post.id);
    const contextFull = Boolean(contextPosts && contextPosts.posts.length >= contextPosts.max_posts);

    const analyzeThread = async (postId: string, analysisType: string) => {
        const result = await doThreadAnalysis(postId, analysisType, activeBot?.username || '');
        selectPost(result.postid, result.channelid);
    };

    const toggleContextPin = async (pin: boolean) => {
        try {
            const updated = pin ? await pinChannelContextPost(post.channel_id, post.id) : await unpinChannelContextPost(post.channel_id, post.id);
            dispatch(receivedChannelContextPosts(post.channel_id, updated));
        } catch {
            // A failed toggle (for example, a concurrent pin filled the
            // channel) re-syncs so the menu reflects the server's state.
            fetchChannelContextPosts(dispatch, post.channel_id);
        }
    };

    let contextPinItem = null;
    if (canPinToContext && contextPinned) {
        contextPinItem = (
            <DropdownMenuItem onClick={() => toggleContextPin(false)}>
                <span className='icon'><PinOutlineIcon size={18}/></span>
                <FormattedMessage defaultMessage='Unpin from agent context'/>
            </DropdownMenuItem>
        );
    } else if (canPinToContext && contextFull) {
        contextPinItem = (
            <DisabledDropdownMenuItemStyled>
                <FormattedMessage
                    defaultMessage='Agent context is full ({max} posts)'
                    values={{max: contextPosts?.max_posts}}
                />
            </DisabledDropdownMenuItemStyled>
        );
    } else if (canPinToContext) {
        contextPinItem = (
            <DropdownMenuItem onClick={() => toggleContextPin(true)}>
                <span className='icon'><PinOutlineIcon size={18}/></span>
                <FormattedMessage defaultMessage='Pin to agent context'/>
            </DropdownMenuItem>
        );
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
            onOpenChange={props.handleDropdownOpened}
        >
            <DropdownBotSelector
                bots={bots ?? []}
                activeBot={activeBot}
                setActiveBot={setActiveBot}
            />
            <Divider/>
            {threadSummarizationLicensed && (
                <>
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
                </>
            )}
            <DropdownMenuItem onClick={() => doReaction(post.id)}>
                <span className='icon'><IconReactForMe/></span>
                <FormattedMessage defaultMessage='React for me'/>
            </DropdownMenuItem>
            {contextPinItem && (
                <>
                    <Divider/>
                    {contextPinItem}
                </>
            )}
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
