// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useState} from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';
import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import PlusIcon from '@mattermost/compass-icons/components/plus';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {UserAvatar} from '@mattermost/compass-ui/components/user-avatar';

import {getProfilePictureUrl, getUserMCPTools, getUserToolPreferences} from '@/client';
import type {LLMBot} from '@/bots';
import ToolProviderPopover, {type UserMCPServerInfo} from '@/components/rhs/tool_provider_popover';

import AgentChatNameMenu from './agent_chat_name_menu';
import {useAgentsRoute} from './use_agents_route';

// No compass token for the prototype's translucent hover fill (rgba(center-channel-color-rgb, 0.08)).
const HOVER_PERCENT = '8%';

// Caps the name button so a long chat title never pushes the tools button and the new-chat button
// off the bar. No compass token for a width of this size.
const TITLE_MAX_WIDTH = 'min(100%, 40rem)';

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
                        <TitleButton
                            type='button'
                            aria-label={ariaLabel}
                            title={label}
                            aria-haspopup='menu'
                            aria-expanded={expanded}
                            data-testid='agent-chat-menu-button'
                            onClick={onClick}
                        >
                            <Name>{label}</Name>
                            <Icon
                                glyph={<ChevronDownIcon/>}
                                size='16'
                            />
                        </TitleButton>
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
                <IconButton
                    size='small'
                    padding='compact'
                    icon={<Icon glyph={<PlusIcon/>}/>}
                    aria-label={newChatLabel}
                    title={rootId ? newChatLabel : intl.formatMessage({defaultMessage: 'You\'re already in a new chat'})}
                    disabled={!rootId}
                    data-testid='agent-chat-new-chat-button'
                    onClick={onNewChat}
                />
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

const TitleButton = styled.button`
    display: inline-flex;
    max-width: ${TITLE_MAX_WIDTH};
    align-items: center;
    min-width: 0;
    margin: 0;
    padding: 0 var(--spacing-xxxs) 0 var(--spacing-xxs);
    border: none;
    border-radius: var(--radius-s);
    background: transparent;
    color: var(--center-channel-color);
    cursor: pointer;
    text-align: left;

    &:hover,
    &[aria-expanded='true'] {
        background: color-mix(in srgb, var(--center-channel-color) ${HOVER_PERCENT}, transparent);
    }

    &:focus-visible {
        outline: var(--spacing-xxxxs) solid var(--focus-ring-color);
        outline-offset: var(--spacing-xxxxs);
    }
`;

const Name = styled.h1`
    min-width: 0;
    margin: 0;
    overflow: hidden;
    font-family: var(--font-family-heading);
    font-size: var(--font-size-200);
    font-weight: var(--font-weight-semibold);
    line-height: var(--line-height-200);
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--center-channel-color);
`;

const Actions = styled.div`
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    gap: var(--spacing-xxs);
`;

export default AgentChatHeader;
