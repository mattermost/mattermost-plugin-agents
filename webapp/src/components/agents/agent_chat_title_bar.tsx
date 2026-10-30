// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';
import PlusIcon from '@mattermost/compass-icons/components/plus';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {UserAvatar} from '@mattermost/compass-ui/components/user-avatar';
import {WithTooltip} from '@mattermost/shared/components/tooltip';

import {getProfilePictureUrl} from '@/client';
import type {LLMBot} from '@/bots';
import ContextUsageIndicator from '@/components/rhs/context_usage_indicator';
import ToolProviderPopover from '@/components/tool_providers/tool_provider_popover';
import {useConversationIdForThread} from '@/hooks/use_conversation_id_for_thread';

import AgentChatNameMenu from './agent_chat_name_menu';
import ChatTitleDropdown from './chat_title_dropdown';

// 'page' is the Agents product's chat header (a <header> bar with the level-1 heading); 'rhs' the
// RHS title, which sits in the host's own header.
export type ChatTitleSurface = 'page' | 'rhs';

interface AgentChatTitleBarProps {
    surface: ChatTitleSurface;
    bot: LLMBot;

    // The open chat, once it is known to be this agent's (the chat menu and the context ring act on it).
    rootId?: string;

    // A chat is open, even while it is still loading and its rootId is not yet validated. The new-chat
    // features (tools, switching agent) follow this, not rootId. Defaults to whether rootId is set.
    inThread?: boolean;

    // The agents a new chat can switch to.
    bots: LLMBot[];
    onSwitchAgent: (bot: LLMBot) => void;
    onSelectChat: (rootId: string) => void;

    // Starts a new chat with this agent.
    onNewChat: () => void;
    onViewAll: () => void;

    // Present only when the current user may edit this agent.
    onOpenSettings?: () => void;

    // Before the avatar (the RHS's Back button).
    leading?: React.ReactNode;

    // Under a host header that already shows the agent (its DM in the Channels app): no avatar and
    // no agent name next to the chat title.
    compact?: boolean;
}

const TEST_IDS: Record<ChatTitleSurface, {menu: string; newChat: string}> = {
    page: {menu: 'agent-chat-menu-button', newChat: 'agent-chat-new-chat-button'},
    rhs: {menu: 'bot-selector-rhs', newChat: 'new-chat'},
};

// The title of an agent chat, the same on both surfaces: the agent's avatar, the chat menu opened
// from the chat's title (the agent's name in a new chat), the agent's name once a chat is open, and at
// the end the tools button in a new chat or the context usage ring and New chat in an open one.
const AgentChatTitleBar = ({surface, bot, rootId, inThread = Boolean(rootId), bots, onSwitchAgent, onSelectChat, onNewChat, onViewAll, onOpenSettings, leading, compact = false}: AgentChatTitleBarProps) => {
    const conversationId = useConversationIdForThread(rootId);
    const testIds = TEST_IDS[surface];

    // Tools are for the new-chat state, and not for service account agents (no per-user connections
    // or preferences to manage).
    const showTools = !inThread && bot.useServiceAccountAuth !== true;

    const title = (
        <>
            {!compact && (
                <UserAvatar
                    src={getProfilePictureUrl(bot.id, bot.lastIconUpdate)}
                    alt=''
                    aria-hidden='true'
                    name={bot.displayName}
                    size='24'
                />
            )}
            <AgentChatNameMenu
                bot={bot}
                rootId={rootId}
                onOpenSettings={onOpenSettings}
                onSelectChat={onSelectChat}
                onNewChat={onNewChat}
                onViewAll={onViewAll}

                // An open chat belongs to its agent: switching starts from a new chat.
                {...(inThread ? {} : {agents: {bots, onSwitch: onSwitchAgent}})}
                renderTrigger={({ariaLabel, label, expanded, onClick}) => (
                    <ChatTitleDropdown
                        label={label}
                        ariaLabel={ariaLabel}
                        expanded={expanded}
                        onClick={onClick}
                        testId={testIds.menu}
                        heading={surface === 'page'}
                    />
                )}
            />
            {rootId && !compact && (
                <AgentName data-testid='agent-chat-agent-name'>
                    {bot.displayName}
                </AgentName>
            )}
        </>
    );
    const actions = (
        <Actions>
            {showTools && <ToolProviderPopover bot={bot}/>}
            {inThread && (
                <>
                    {conversationId && <ContextUsageIndicator conversationId={conversationId}/>}
                    <NewChatButton
                        onClick={onNewChat}
                        testId={testIds.newChat}
                    />
                </>
            )}
        </Actions>
    );

    if (surface === 'rhs') {
        return (
            <RHSContainer data-testid='rhs-title'>
                {leading}
                {title}
                {actions}
            </RHSContainer>
        );
    }
    return (
        <PageHeader
            $bordered={inThread}
            aria-label={bot.displayName}
        >
            <PageTitle>
                {leading}
                {title}
            </PageTitle>
            {actions}
        </PageHeader>
    );
};

// No compass token for the resting and hover washes of the text color behind New chat, which set it
// apart from the bare buttons next to the title.
const NEW_CHAT_FILL = 'rgba(var(--center-channel-color-rgb), 0.08)';
const NEW_CHAT_FILL_HOVER = 'rgba(var(--center-channel-color-rgb), 0.12)';

// The New chat button at the end of a chat title (and of the RHS chat history's title).
export const NewChatButton = ({onClick, testId}: {onClick: () => void; testId: string}) => {
    const intl = useIntl();
    const label = intl.formatMessage({defaultMessage: 'New chat'});
    return (
        <WithTooltip title={label}>
            <TooltipTrigger>
                <FilledIconButton
                    size='small'
                    padding='compact'
                    rounded={true}
                    icon={<Icon glyph={<PlusIcon/>}/>}
                    aria-label={label}
                    data-testid={testId}
                    onClick={onClick}
                />
            </TooltipTrigger>
        </WithTooltip>
    );
};

const FilledIconButton = styled(IconButton)`
    && {
        background-color: ${NEW_CHAT_FILL};
    }

    &&:hover:not(:disabled) {
        background-color: ${NEW_CHAT_FILL_HOVER};
    }
`;

const TooltipTrigger = styled.span`
    display: inline-flex;
`;

// The prototype's agent-chat header: 56px bar (spacing-m padding around a 32px row).
const PageHeader = styled.header<{$bordered: boolean}>`
    /* Above the new-chat body, whose lift (translateY) can overlap this bar and block its clicks. */
    position: relative;
    z-index: 1;
    display: flex;
    box-sizing: border-box;
    flex: 0 0 auto;
    align-items: center;
    justify-content: space-between;
    gap: var(--spacing-l);
    min-height: calc(var(--spacing-xxxl) + 2 * var(--spacing-m));
    padding: var(--spacing-m);
    border-bottom: var(--border-default);
    ${({$bordered}) => !$bordered && 'border-bottom-color: transparent;'}
    transition: border-color var(--duration-moderate) var(--ease-transition);

    @media (prefers-reduced-motion: reduce) {
        transition: none;
    }
`;

const PageTitle = styled.div`
    display: flex;
    flex: 1 1 auto;
    min-width: 0;
    align-items: center;
    gap: var(--spacing-xs);
    padding-inline: var(--spacing-xs);
`;

// The host pads its title area by 1.6rem on both sides (sass/layout/_sidebar-right.scss
// `.sidebar--right__title`). The title takes that padding back so it can set its own: 12px
// (--spacing-m) at the start, and 4px (--spacing-xxxs) at the end, where the New chat or tools
// button sits close to the host's own header buttons. It is a margin on this element rather than a
// rule on the host's class, so no other RHS panel is affected. Not a compass value: the 1.6rem
// mirrors host CSS.
export const HOST_TITLE_INLINE_PADDING = '1.6rem';

// min-width: 0 all the way down lets a long name ellipsize instead of pushing the host's expand,
// popout and close buttons out of the header. The host wraps the title in its own heading landmark
// (a span#rhsPanelTitle), so none is added here.
const RHSContainer = styled.span`
    display: flex;
    flex: 1 1 auto;
    min-width: 0;
    align-items: center;
    gap: var(--spacing-xxxs);
    margin-left: calc(-1 * ${HOST_TITLE_INLINE_PADDING} + var(--spacing-m));
    margin-right: calc(-1 * ${HOST_TITLE_INLINE_PADDING} + var(--spacing-xxxs));
`;

// Once a chat is open, the agent's name sits to the right of the title's dropdown, outside it:
// body-75 type in the dim text color. It gives way (ellipsis) before the title does.
const AgentName = styled.span`
    flex: 0 100 auto;
    min-width: 0;
    overflow: hidden;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    font-family: var(--font-family-body);
    font-size: var(--font-size-75);
    font-weight: var(--font-weight-regular);
    line-height: var(--line-height-75);
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const Actions = styled.span`
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    gap: var(--spacing-xxs);
    margin-inline-start: auto;
`;

export default AgentChatTitleBar;
