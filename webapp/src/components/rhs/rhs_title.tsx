// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback} from 'react';
import {useDispatch, useSelector} from 'react-redux';
import {FormattedMessage, useIntl} from 'react-intl';
import styled from 'styled-components';
import ArrowLeftIcon from '@mattermost/compass-icons/components/arrow-left';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {WithTooltip} from '@mattermost/shared/components/tooltip';

//@ts-ignore it exists
import aiIcon from '../../../../assets/bot_icon.png';

import {useBotlist, type LLMBot} from '@/bots';
import AgentChatTitleBar, {HOST_TITLE_INLINE_PADDING, NewChatButton} from '@/components/agents/agent_chat_title_bar';
import {useAgentsRoute} from '@/components/agents/use_agents_route';
import manifest from '@/manifest';
import type {RHSTab} from '@/redux';
import {selectPost, showRHSList} from '@/redux_actions';

import {useSelectedThreadBot} from './use_selected_thread_bot';

// The RHS header title: Back to the chat history, then the agent chat's title (the same as the Agents
// product's chat header). The chat history has a generic title with New chat.
const RHSTitle = () => {
    const intl = useIntl();
    const {bots, activeBot, setActiveBot} = useBotlist();
    const dispatch = useDispatch();

    const listTab: RHSTab = useSelector((state: any) => state['plugins-' + manifest.id]?.rhsTab) ?? 'new';
    const thread = useSelectedThreadBot(bots);
    const threadOpen = Boolean(thread.rootId);
    const onHistory = !threadOpen && listTab === 'threads';
    const {toAll} = useAgentsRoute();

    // The agent of the open chat (it can differ from the active agent that new chats use), else the
    // active agent.
    const titleBot = (threadOpen && thread.bot) || activeBot;

    // A new chat with an agent: it becomes the active agent (saved, as a pick in the RHS always is).
    const newChatWith = useCallback((bot: LLMBot) => {
        if (bot.id !== activeBot?.id) {
            setActiveBot(bot);
        }
        dispatch(showRHSList('new'));
    }, [activeBot, setActiveBot, dispatch]);

    const backLabel = intl.formatMessage({defaultMessage: 'Back to chat history'});
    const back = onHistory ? null : (
        <WithTooltip title={backLabel}>
            <TooltipTrigger>
                <IconButton
                    size='small'
                    padding='compact'
                    icon={<Icon glyph={<ArrowLeftIcon/>}/>}
                    aria-label={backLabel}
                    data-testid='chat-history'
                    onClick={() => dispatch(showRHSList('threads'))}
                />
            </TooltipTrigger>
        </WithTooltip>
    );

    // The chat history lists every agent's chats, so it has the generic title (no agent menu),
    // as do loading, no agents, and an unresolved active agent.
    if (onHistory || !bots || bots.length === 0 || !titleBot) {
        return (
            <Container data-testid='rhs-title'>
                {back}
                <FallbackIcon
                    src={aiIcon}
                    alt=''
                />
                <Name><FormattedMessage defaultMessage='Agents'/></Name>
                {(onHistory || threadOpen) && (
                    <Actions>
                        <NewChatButton
                            onClick={() => dispatch(showRHSList('new'))}
                            testId='new-chat'
                        />
                    </Actions>
                )}
            </Container>
        );
    }

    return (
        <AgentChatTitleBar
            surface='rhs'
            leading={back}
            bot={titleBot}
            rootId={thread.rootId}
            bots={bots}
            onSwitchAgent={newChatWith}
            onSelectChat={(rootId) => dispatch(selectPost(rootId))}
            onNewChat={() => newChatWith(titleBot)}
            onViewAll={toAll}
        />
    );
};

// The generic title keeps the host's padding, plus 4px (--spacing-xxxs) at the start.
const Container = styled.span`
    display: flex;
    flex: 1 1 auto;
    min-width: 0;
    align-items: center;
    gap: var(--spacing-xs);
    margin-left: var(--spacing-xxxs);
    margin-right: calc(-1 * ${HOST_TITLE_INLINE_PADDING} + var(--spacing-xxxs));
`;

const Actions = styled.span`
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    margin-inline-start: auto;
`;

const TooltipTrigger = styled.span`
    display: inline-flex;
`;

const FallbackIcon = styled.img`
    width: var(--icon-size-24);
    height: var(--icon-size-24);
    flex: 0 0 auto;
    border-radius: var(--radius-pill);
`;

const Name = styled.span`
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
`;

export default RHSTitle;
