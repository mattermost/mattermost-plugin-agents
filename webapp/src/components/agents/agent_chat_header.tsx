// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useState} from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';
import PlusIcon from '@mattermost/compass-icons/components/plus';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {UserAvatar} from '@mattermost/compass-ui/components/user-avatar';
import {WithTooltip} from '@mattermost/shared/components/tooltip';

import {getProfilePictureUrl, getUserMCPTools, getUserToolPreferences} from '@/client';
import type {LLMBot} from '@/bots';
import ToolProviderPopover, {type UserMCPServerInfo} from '@/components/rhs/tool_provider_popover';

import AgentChatNameMenu from './agent_chat_name_menu';
import ChatTitleDropdown from './chat_title_dropdown';
import {useAgentsRoute} from './use_agents_route';

interface AgentChatHeaderProps {
    bot: LLMBot;

    // The open chat, once it is known to be this agent's (the chat menu acts on it).
    rootId?: string;

    // A chat is open, even while it is still loading and its rootId is not yet validated. The new-chat
    // features (tools, switching agent) follow this, not rootId. Defaults to whether rootId is set.
    inThread?: boolean;

    // Present only when the current user may edit this agent.
    onOpenSettings?: () => void;
    onNewChat: () => void;

    // The agents to offer in a new chat's "Switch agent" submenu. An open chat belongs to its agent,
    // so it never offers the switch.
    bots?: LLMBot[];
}

// The agent name opens the chat menu, as the prototype's header title button does.
const AgentChatHeader = ({bot, rootId, inThread = Boolean(rootId), onOpenSettings, onNewChat, bots}: AgentChatHeaderProps) => {
    const intl = useIntl();
    const {toChat, toAll} = useAgentsRoute();

    // Tool providers: fed exactly as the RHS feeds its popover.
    const [disabledServers, setDisabledServers] = useState<string[]>([]);
    const [preloadedServers, setPreloadedServers] = useState<UserMCPServerInfo[]>([]);
    useEffect(() => {
        getUserToolPreferences().then((prefs) => setDisabledServers(prefs.disabled_servers || [])).catch(() => null);
        getUserMCPTools().then((response) => setPreloadedServers(response.servers)).catch(() => null);
    }, []);

    const newChatLabel = intl.formatMessage({defaultMessage: 'New chat'});

    // Like the RHS: the tools button is for the new-chat state, and not for service
    // account agents (no per-user connections or preferences to manage).
    const showTools = !inThread && bot.useServiceAccountAuth !== true;

    return (
        <Root
            $bordered={inThread}
            aria-label={bot.displayName}
        >
            <Title>
                <UserAvatar
                    src={getProfilePictureUrl(bot.id, bot.lastIconUpdate)}
                    alt=''
                    aria-hidden='true'
                    name={bot.displayName}
                    size='24'
                />
                <AgentChatNameMenu
                    bot={bot}
                    rootId={rootId}
                    onOpenSettings={onOpenSettings}
                    onSelectChat={(chatRootId) => toChat(bot.id, chatRootId)}
                    onNewChat={onNewChat}
                    onViewAll={toAll}
                    {...(!inThread && bots ? {agents: {bots, onSwitch: (other: LLMBot) => toChat(other.id)}} : {})}
                    renderTrigger={({ariaLabel, label, expanded, onClick}) => (
                        <ChatTitleDropdown
                            label={label}
                            ariaLabel={ariaLabel}
                            expanded={expanded}
                            onClick={onClick}
                            testId='agent-chat-menu-button'
                            heading={true}
                        />
                    )}
                />
                {rootId && (
                    <AgentName data-testid='agent-chat-agent-name'>
                        {bot.displayName}
                    </AgentName>
                )}
            </Title>
            <Actions>
                {showTools && (
                    <ToolProviderPopover
                        disabledServers={disabledServers}
                        onDisabledServersChange={setDisabledServers}
                        preloadedServers={preloadedServers}
                        enabledMCPTools={bot.enabledMCPTools}
                        autoEnableNewMCPTools={bot.autoEnableNewMCPTools}
                    />
                )}
                <WithTooltip title={inThread ? newChatLabel : intl.formatMessage({defaultMessage: 'You\'re already in a new chat'})}>
                    <TooltipTrigger>
                        <IconButton
                            size='small'
                            padding='compact'
                            icon={<Icon glyph={<PlusIcon/>}/>}
                            aria-label={newChatLabel}
                            disabled={!inThread}
                            data-testid='agent-chat-new-chat-button'
                            onClick={onNewChat}
                        />
                    </TooltipTrigger>
                </WithTooltip>
            </Actions>
        </Root>
    );
};

// The prototype's agent-chat header: 56px bar (spacing-m padding around a 32px row).
const Root = styled.header<{$bordered: boolean}>`
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

const Title = styled.div`
    display: flex;
    flex: 1 1 auto;
    min-width: 0;
    align-items: center;
    gap: var(--spacing-xs);
    padding-inline: var(--spacing-xs);
`;

// Once a chat has its own title, the agent's name sits to the right of the title's dropdown, outside
// it: body-75 type in the dim text color. It gives way (ellipsis) before the buttons on the right do.
const AgentName = styled.span`
    flex: 0 1 auto;
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

// A disabled button fires no pointer events, so the tooltip listens on this wrapper.
const TooltipTrigger = styled.span`
    display: inline-flex;
`;

const Actions = styled.div`
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    gap: var(--spacing-xxs);
`;

export default AgentChatHeader;
