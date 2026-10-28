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
    rootId?: string;

    // Present only when the current user may edit this agent.
    onOpenSettings?: () => void;
    onNewChat: () => void;

    // Called after the open chat was archived.
    onArchived: () => void;
}

// The agent name opens the chat menu, as the prototype's header title button does.
const AgentChatHeader = ({bot, rootId, onOpenSettings, onNewChat, onArchived}: AgentChatHeaderProps) => {
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
    const showTools = !rootId && bot.useServiceAccountAuth !== true;

    return (
        <Root aria-label={bot.displayName}>
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
                    onArchived={onArchived}
                    renderTrigger={({ariaLabel, label, expanded, onClick}) => (
                        <ChatTitleDropdown
                            label={label}
                            ariaLabel={ariaLabel}
                            expanded={expanded}
                            onClick={onClick}
                            testId='agent-chat-menu-button'
                            heading={true}
                            subtitle={rootId ? bot.displayName : ''}
                            subtitlePlacement='beside'
                        />
                    )}
                />
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
                <WithTooltip title={rootId ? newChatLabel : intl.formatMessage({defaultMessage: 'You\'re already in a new chat'})}>
                    <TooltipTrigger>
                        <IconButton
                            size='small'
                            padding='compact'
                            icon={<Icon glyph={<PlusIcon/>}/>}
                            aria-label={newChatLabel}
                            disabled={!rootId}
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
const Root = styled.header`
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
    background: var(--center-channel-bg);
    border-bottom: var(--border-default);
`;

const Title = styled.div`
    display: flex;
    flex: 1 1 auto;
    min-width: 0;
    align-items: center;
    gap: var(--spacing-xs);
    padding-inline: var(--spacing-xs);
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
