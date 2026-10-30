// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {
    DotsHorizontalIcon,
    ExportVariantIcon,
    PencilOutlineIcon,
    TrashCanOutlineIcon,
} from '@mattermost/compass-icons/components';
import MessageOutlineIcon from '@mattermost/compass-icons/components/message-outline';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {Button} from '@mattermost/compass-ui/components/button';
import {Tag} from '@mattermost/compass-ui/components/tag';
import {UserAvatar} from '@mattermost/compass-ui/components/user-avatar';
import {WithTooltip} from '@mattermost/shared/components/tooltip';

import {getProfilePictureUrl} from '@/client';
import DotMenu, {DropdownMenuItem} from '@/components/dot_menu';

import {AgentInactiveReason, UserAgent, ServiceInfo} from '@/types/agents';

type Props = {
    agent: UserAgent;
    services: ServiceInfo[];
    servicesLoaded: boolean;
    canManage: boolean;
    onStartChat: (botUserId: string) => void;
    onEdit: (agent: UserAgent) => void;
    onDelete: (agent: UserAgent) => void;
    onExport: (agent: UserAgent) => void;
}

const AgentRow = (props: Props) => {
    const {agent, services, servicesLoaded, canManage, onStartChat, onEdit, onDelete, onExport} = props;
    const intl = useIntl();

    const avatarUrl = getProfilePictureUrl(agent.botUserID ?? '', 0);
    const autoEnableNewMCPTools = agent.autoEnableNewMCPTools ?? false;
    const toolCount = autoEnableNewMCPTools ? 0 : (agent.enabledMCPTools?.length ?? 0);
    const service = services.find((s) => s.id === agent.serviceID);

    // Only infer a missing service once the list has loaded; users without
    // agent-management permission never fetch it, so empty means unknown.
    const serviceMissing = Boolean(servicesLoaded && agent.serviceID && !service);
    let inactiveReason = agent.inactiveReason;
    if (!inactiveReason && serviceMissing) {
        inactiveReason = 'service_unavailable';
    }

    let mcpBadge: React.ReactNode = null;
    if (autoEnableNewMCPTools) {
        mcpBadge = (
            <Tag label={<FormattedMessage defaultMessage='All MCP tools'/>}/>
        );
    } else if (toolCount > 0) {
        mcpBadge = (
            <Tag
                label={intl.formatMessage(
                    {defaultMessage: '{count, plural, one {# tool} other {# tools}}'},
                    {count: toolCount},
                )}
            />
        );
    }

    const handleMenuItemEdit = useCallback(() => {
        onEdit(agent);
    }, [agent, onEdit]);

    const handleMenuItemDelete = useCallback(() => {
        onDelete(agent);
    }, [agent, onDelete]);

    const handleMenuItemExport = useCallback(() => {
        onExport(agent);
    }, [agent, onExport]);

    // The chat route is keyed by the bot user id; without one there is nothing
    // safe to navigate to, so the row is not actionable.
    const botUserId = agent.botUserID || '';
    const actionable = botUserId !== '';
    const agentLabel = agent.displayName || agent.name;
    const unavailableId = `agent-row-unavailable-${agent.id}`;

    const handleRowActivate = useCallback(() => {
        if (actionable) {
            onStartChat(botUserId);
        }
    }, [actionable, botUserId, onStartChat]);

    const handleRowKeyDown = useCallback(
        (e: React.KeyboardEvent) => {
            if (actionable && (e.key === 'Enter' || e.key === ' ')) {
                e.preventDefault();
                onStartChat(botUserId);
            }
        },
        [actionable, botUserId, onStartChat],
    );

    // The row's primary action is RowMain, a button that holds only text and the avatar. Its ::after
    // stretches over the whole row so a click anywhere starts a chat, while the other controls (the
    // hover button, the badges, the actions menu) are siblings above it, never inside a button.
    return (
        <RowContainer
            $clickable={actionable}
            data-testid='agent-row'
        >
            <RowMain
                role='button'
                tabIndex={0}
                onClick={handleRowActivate}
                onKeyDown={handleRowKeyDown}
                {...(actionable ? {} : {'aria-disabled': true, 'aria-describedby': unavailableId})}
                aria-label={intl.formatMessage(
                    {defaultMessage: 'Start a new chat with {name}'},
                    {name: agentLabel},
                )}
            >
                {!actionable && (
                    <VisuallyHidden id={unavailableId}>
                        <FormattedMessage defaultMessage='This agent has no bot account, so a chat cannot be started.'/>
                    </VisuallyHidden>
                )}
                <Avatar
                    src={avatarUrl}
                    alt=''
                    aria-hidden='true'
                    size='24'
                />
                <NameColumn>
                    <DisplayName>{agent.displayName}</DisplayName>
                    <Username>{'@'}{agent.name}</Username>
                </NameColumn>
            </RowMain>
            {actionable && (
                <NewChatSlot>
                    <Button
                        emphasis='dim'
                        size='x-small'
                        leadingIcon={<Icon glyph={<MessageOutlineIcon/>}/>}
                        aria-label={intl.formatMessage({defaultMessage: 'Start new chat with {agent}'}, {agent: agentLabel})}
                        data-testid='agent-row-new-chat'
                        onClick={handleRowActivate}
                    >
                        {intl.formatMessage({defaultMessage: 'Start new chat'})}
                    </Button>
                </NewChatSlot>
            )}
            <BadgesColumn>
                {inactiveReason && (
                    <WithTooltip title={<InactiveReasonMessage reason={inactiveReason}/>}>
                        <Tag
                            type='danger'
                            tabIndex={0}
                            label={<FormattedMessage defaultMessage='Inactive'/>}
                        />
                    </WithTooltip>
                )}
                {mcpBadge}
            </BadgesColumn>
            {canManage && (
                <ActionsColumn>
                    <DotMenu
                        icon={<DotsHorizontalIcon/>}
                        title={intl.formatMessage({defaultMessage: 'Agent actions'})}
                        placement='bottom-end'

                        // Rendered in place so the menu stays inside the row's DOM subtree.
                        portal={false}
                    >
                        <DropdownMenuItem
                            icon={<PencilOutlineIcon/>}
                            label={<FormattedMessage defaultMessage='Edit'/>}
                            onClick={handleMenuItemEdit}
                        />
                        <DropdownMenuItem
                            icon={<ExportVariantIcon/>}
                            label={<FormattedMessage defaultMessage='Export'/>}
                            onClick={handleMenuItemExport}
                        />
                        <DropdownMenuItem
                            icon={<TrashCanOutlineIcon/>}
                            label={<FormattedMessage defaultMessage='Delete'/>}
                            onClick={handleMenuItemDelete}
                            destructive={true}
                        />
                    </DotMenu>
                </ActionsColumn>
            )}
        </RowContainer>
    );
};

const InactiveReasonMessage = ({reason}: {reason: AgentInactiveReason}) => {
    switch (reason) {
    case 'service_not_licensed':
        return <FormattedMessage defaultMessage='This agent uses an LLM service that is not active on your current plan. Only the first configured service is active; multiple LLM services are available on Enterprise plans and above. Edit the agent to choose the active service.'/>;
    case 'agent_limit':
        return <FormattedMessage defaultMessage='Your current plan has reached its limit of active AI agents, and agents created earlier take the available slots. Delete an earlier agent, or upgrade your plan for more agents.'/>;
    case 'service_unavailable':
        return <FormattedMessage defaultMessage='This agent’s LLM service was deleted or is missing required settings. Edit the agent to choose another service, or complete the service configuration.'/>;
    default:
        return <FormattedMessage defaultMessage='This agent’s configuration is incomplete. Edit the agent to fix it.'/>;
    }
};

// --- Styled Components ---

const RowContainer = styled.div<{$clickable: boolean}>`
    position: relative;
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: var(--spacing-xs);
    height: 60px;
    padding: 0 var(--spacing-l);
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    background: var(--center-channel-bg, #fff);
    cursor: ${({$clickable}) => ($clickable ? 'pointer' : 'default')};

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.04);
    }
`;

// Not positioned itself, so its ::after covers the whole row (RowContainer is the positioned ancestor).
const RowMain = styled.div`
    display: flex;
    flex: 0 1 auto;
    flex-direction: row;
    align-items: center;
    gap: var(--spacing-xs);
    min-width: 0;
    outline: none;

    &::after {
        position: absolute;
        inset: 0;
        content: '';
    }

    &:focus-visible::after {
        box-shadow: inset 0 0 0 2px rgba(var(--button-bg-rgb, 28, 88, 217), 0.4);
    }
`;

const Avatar = styled(UserAvatar)`
    flex-shrink: 0;
`;

const NameColumn = styled.div`
    display: flex;
    flex: 0 1 auto;
    flex-direction: row;
    align-items: center;
    gap: var(--spacing-xs);
    min-width: 0;
`;

const DisplayName = styled.div`
    font-family: var(--font-family-body, 'Open Sans', sans-serif);
    font-size: var(--font-size-100);
    font-weight: var(--font-weight-semibold);
    line-height: var(--line-height-100);
    color: var(--center-channel-color);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
`;

const Username = styled.div`
    font-family: var(--font-family-body, 'Open Sans', sans-serif);
    font-size: var(--font-size-100);
    font-weight: var(--font-weight-regular);
    line-height: var(--line-height-100);
    color: rgba(var(--center-channel-color-rgb), 0.75);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
`;

// Shown only while the row is hovered or has focus inside it (the button itself stays in the tab order).
const NewChatSlot = styled.span`
    position: relative;
    z-index: 1;
    display: inline-flex;
    flex-shrink: 0;
    opacity: 0;
    transition: opacity var(--duration-quick) var(--ease-transition);

    ${RowContainer}:hover &,
    ${RowContainer}:focus-within & {
        opacity: 1;
    }

    @media (prefers-reduced-motion: reduce) {
        transition: none;
    }
`;

const BadgesColumn = styled.div`
    position: relative;
    z-index: 1;
    display: flex;
    margin-left: auto;
    flex-direction: row;
    gap: var(--spacing-xs);
    align-items: center;
    flex-shrink: 0;
`;

const ActionsColumn = styled.div`
    position: relative;
    z-index: 1;
    flex-shrink: 0;
`;

const VisuallyHidden = styled.span`
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
`;

export default AgentRow;
