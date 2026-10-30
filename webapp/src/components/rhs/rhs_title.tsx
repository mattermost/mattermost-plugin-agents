// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback} from 'react';
import {useDispatch, useSelector} from 'react-redux';
import {FormattedMessage, useIntl} from 'react-intl';
import styled from 'styled-components';
import ArrowLeftIcon from '@mattermost/compass-icons/components/arrow-left';
import PlusIcon from '@mattermost/compass-icons/components/plus';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {UserAvatar} from '@mattermost/compass-ui/components/user-avatar';
import {WithTooltip} from '@mattermost/shared/components/tooltip';

//@ts-ignore it exists
import aiIcon from '../../../../assets/bot_icon.png';

import {useBotlist, type LLMBot} from '@/bots';
import {getProfilePictureUrl} from '@/client';
import AgentChatNameMenu from '@/components/agents/agent_chat_name_menu';
import ChatTitleDropdown from '@/components/agents/chat_title_dropdown';
import {useAgentsRoute} from '@/components/agents/use_agents_route';
import ToolProviderPopover from '@/components/tool_providers/tool_provider_popover';
import manifest from '@/manifest';
import type {RHSTab} from '@/redux';

import {useConversationIdForThread} from '@/hooks/use_conversation_id_for_thread';

import ContextUsageIndicator from './context_usage_indicator';

const AgentAvatar = ({bot}: {bot: LLMBot}) => (
    <UserAvatar
        src={getProfilePictureUrl(bot.id, bot.lastIconUpdate)}
        alt=''
        aria-hidden='true'
        name={bot.displayName}
        size='24'
    />
);

// The RHS header title: Back to the chat history, the chat (or the agent, in a new chat) with the
// same menu as the Agents product's chat header (recent chats, new, rename, view all agents) plus
// a Switch agent submenu, and at the end the tools button on a new chat, or the context usage ring
// and New chat everywhere else.
// The host wraps the title in its own heading landmark (a span#rhsPanelTitle), so none is added here.
const RHSTitle = () => {
    const intl = useIntl();
    const {bots, activeBot, setActiveBot} = useBotlist();
    const dispatch = useDispatch();

    const pluginState = useSelector((state: any) => state['plugins-' + manifest.id]);
    const threadOpen = Boolean(pluginState?.selectedPostId);
    const listTab: RHSTab = pluginState?.rhsTab ?? 'new';
    const onNewChat = !threadOpen && listTab === 'new';
    const onHistory = !threadOpen && listTab === 'threads';
    const activeConversationId = useConversationIdForThread(pluginState?.selectedPostId);
    const {toAll} = useAgentsRoute();

    // The agent of the open chat: the DM partner, or the root's author for chats an agent started
    // (as the RHS body finds it). It can differ from the active agent that new chats use.
    const selectedRoot = useSelector((state: any) => (
        (pluginState?.selectedPostId && state.entities?.posts?.posts?.[pluginState.selectedPostId]) || null
    ));

    // Switching agents behaves like starting a new chat with the new agent (leave the open
    // thread and show the new-chat tab); choosing the current agent changes nothing.
    const switchAgent = useCallback((bot: LLMBot) => {
        setActiveBot(bot);
        dispatch({type: 'SELECT_AI_POST', postId: ''});
        dispatch({type: 'AI_RHS_SET_TAB', tab: 'new'});
    }, [setActiveBot, dispatch]);
    const startNewChat = useCallback(() => {
        dispatch({type: 'SELECT_AI_POST', postId: ''});
        dispatch({type: 'AI_RHS_SET_TAB', tab: 'new'});
    }, [dispatch]);

    const newChatLabel = intl.formatMessage({defaultMessage: 'New chat'});
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
                    onClick={() => {
                        dispatch({type: 'SELECT_AI_POST', postId: ''});
                        dispatch({type: 'AI_RHS_SET_TAB', tab: 'threads'});
                    }}
                />
            </TooltipTrigger>
        </WithTooltip>
    );

    // The end of the title: the tools button on a new chat (SA agents have no per-user connections
    // or preferences to manage), New chat (with the context ring) on the history and an open chat.
    let actions: React.ReactNode = null;
    if (onNewChat) {
        if (activeBot && activeBot.useServiceAccountAuth !== true) {
            actions = <ToolProviderPopover bot={activeBot}/>;
        }
    } else {
        actions = (
            <>
                {activeConversationId && <ContextUsageIndicator conversationId={activeConversationId}/>}
                <WithTooltip title={newChatLabel}>
                    <TooltipTrigger>
                        <NewChatButton
                            size='small'
                            padding='compact'
                            rounded={true}
                            icon={<Icon glyph={<PlusIcon/>}/>}
                            aria-label={newChatLabel}
                            data-testid='new-chat'
                            onClick={startNewChat}
                        />
                    </TooltipTrigger>
                </WithTooltip>
            </>
        );
    }
    const end = actions && <Actions>{actions}</Actions>;

    // The chat history lists every agent's chats, so it has the generic title (no agent menu),
    // as do loading, no agents, and an unresolved active agent.
    if (onHistory || !bots || bots.length === 0 || !activeBot) {
        return (
            <Container data-testid='rhs-title'>
                {back}
                <FallbackIcon
                    src={aiIcon}
                    alt=''
                />
                <Name><FormattedMessage defaultMessage='Agents'/></Name>
                {end}
            </Container>
        );
    }

    const menuBot = (threadOpen && bots.find((b) => (selectedRoot?.channel_id && b.dmChannelID === selectedRoot.channel_id) || b.id === selectedRoot?.user_id)) || activeBot;
    return (
        <Container
            data-testid='rhs-title'
            $tight={true}
        >
            {back}
            <AgentAvatar bot={menuBot}/>
            <AgentChatNameMenu
                bot={menuBot}
                rootId={pluginState?.selectedPostId || ''}
                agents={{bots, onSwitch: switchAgent}}
                onSelectChat={(rootId) => dispatch({type: 'SELECT_AI_POST', postId: rootId})}
                onNewChat={startNewChat}
                onViewAll={toAll}
                renderTrigger={({ariaLabel, label, expanded, onClick}) => (
                    <ChatTitleDropdown
                        label={label}
                        ariaLabel={ariaLabel}
                        expanded={expanded}
                        onClick={onClick}
                        testId='bot-selector-rhs'
                    />
                )}
            />
            {end}
        </Container>
    );
};

// The Back button sits flush with the header's start; without it the title is inset.
// min-width: 0 all the way down lets a long name ellipsize instead of pushing the
// host's expand, popout and close buttons out of the header.
// The host pads its title area by 1.6rem on both sides (sass/layout/_sidebar-right.scss
// `.sidebar--right__title`). The title takes that padding back so it can set its own: 12px
// (--spacing-m) at the start, and 4px (--spacing-xxxs) at the end, where the New chat or tools
// button sits close to the host's own header buttons. It is a margin on this element rather than a
// rule on the host's class, so no other RHS panel is affected. Not a compass value: the 1.6rem
// mirrors host CSS.
const HOST_TITLE_INLINE_PADDING = '1.6rem';

// `tight` is the chat views' title (a new chat and an open chat): its elements sit closer and its
// start takes the host's padding back down to 12px. The chat history's generic title keeps the host's
// padding, plus 4px (--spacing-xxxs).
const Container = styled.span<{$tight?: boolean}>`
    display: flex;
    flex: 1 1 auto;
    min-width: 0;
    align-items: center;
    gap: ${({$tight}) => ($tight ? 'var(--spacing-xxxs)' : 'var(--spacing-xs)')};
    margin-left: ${({$tight}) => ($tight ? `calc(-1 * ${HOST_TITLE_INLINE_PADDING} + var(--spacing-m))` : 'var(--spacing-xxxs)')};
    margin-right: calc(-1 * ${HOST_TITLE_INLINE_PADDING} + var(--spacing-xxxs));
`;

const Actions = styled.span`
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    gap: var(--spacing-xxs);
    margin-inline-start: auto;
`;

// No compass token for the resting and hover washes of the text color behind New chat, which set it
// apart from the bare back button next to the title.
const NEW_CHAT_FILL = 'rgba(var(--center-channel-color-rgb), 0.08)';
const NEW_CHAT_FILL_HOVER = 'rgba(var(--center-channel-color-rgb), 0.12)';

const TooltipTrigger = styled.span`
    display: inline-flex;
`;

const NewChatButton = styled(IconButton)`
    && {
        background-color: ${NEW_CHAT_FILL};
    }

    &&:hover:not(:disabled) {
        background-color: ${NEW_CHAT_FILL_HOVER};
    }
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
