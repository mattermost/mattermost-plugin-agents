// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useCallback} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {
    DotsHorizontalIcon,
    PencilOutlineIcon,
    TrashCanOutlineIcon,
} from '@mattermost/compass-icons/components';
//eslint-disable-next-line import/no-unresolved -- react-bootstrap is external
import {OverlayTrigger, Tooltip} from 'react-bootstrap';

import {Tag} from '@mattermost/compass-ui/components/tag';
import {UserAvatar} from '@mattermost/compass-ui/components/user-avatar';

import {getProfilePictureUrl} from '@/client';
import {getPortalTarget} from '@/utils/dom';
import DotMenu, {DropdownMenuItem} from '@/components/dot_menu';

import {AgentInactiveReason, UserAgent, ServiceInfo} from '@/types/agents';

type Props = {
    agent: UserAgent;
    services: ServiceInfo[];
    servicesLoaded: boolean;
    canManage: boolean;
    onEdit: (agent: UserAgent) => void;
    onDelete: (agent: UserAgent) => void;
}

const AgentRow = (props: Props) => {
    const {agent, services, servicesLoaded, canManage, onEdit, onDelete} = props;
    const [menuOpen, setMenuOpen] = useState(false);
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

    const handleRowActivate = useCallback(() => {
        if (!canManage || menuOpen) {
            return;
        }
        onEdit(agent);
    }, [canManage, menuOpen, agent, onEdit]);

    const handleRowKeyDown = useCallback(
        (e: React.KeyboardEvent) => {
            // Keys pressed on nested controls (badge triggers, the actions menu) bubble here too.
            if (!canManage || menuOpen || e.target !== e.currentTarget) {
                return;
            }
            if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault();
                onEdit(agent);
            }
        },
        [canManage, menuOpen, agent, onEdit],
    );

    return (
        <RowContainer
            $clickable={canManage}
            {...(canManage ? {
                onClick: handleRowActivate,
                onKeyDown: handleRowKeyDown,
                role: 'button',
                tabIndex: 0,
                'aria-label': intl.formatMessage(
                    {defaultMessage: 'Edit agent {name}'},
                    {name: agent.displayName || agent.name},
                ),
            } : {})}
        >
            <RowMain>
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
                <BadgesColumn>
                    {inactiveReason && (
                        <OverlayTrigger
                            placement='top'
                            container={getPortalTarget}
                            overlay={
                                <Tooltip id={`inactive-agent-tooltip-${agent.id}`}>
                                    <InactiveReasonMessage reason={inactiveReason}/>
                                </Tooltip>
                            }
                        >
                            <BadgeTrigger tabIndex={0}>
                                <Tag
                                    type='danger'
                                    label={<FormattedMessage defaultMessage='Inactive'/>}
                                />
                            </BadgeTrigger>
                        </OverlayTrigger>
                    )}
                    {!canManage && (
                        <OverlayTrigger
                            placement='top'
                            container={getPortalTarget}
                            overlay={
                                <Tooltip id={`read-only-agent-tooltip-${agent.id}`}>
                                    <FormattedMessage
                                        defaultMessage='Mention @{username} in a channel or direct message to chat with this agent.'
                                        values={{username: agent.name}}
                                    />
                                </Tooltip>
                            }
                        >
                            <BadgeTrigger tabIndex={0}>
                                <Tag label={<FormattedMessage defaultMessage='Read only'/>}/>
                            </BadgeTrigger>
                        </OverlayTrigger>
                    )}
                    {mcpBadge}
                </BadgesColumn>
            </RowMain>
            {canManage && (
                <ActionsColumn>
                    <DotMenu
                        icon={<DotsHorizontalIcon/>}
                        title={intl.formatMessage({defaultMessage: 'Agent actions'})}
                        placement='bottom-end'
                        onOpenChange={setMenuOpen}

                        // Rendered in place so the menu stays inside the row's DOM subtree.
                        portal={false}
                    >
                        <DropdownMenuItem
                            icon={<PencilOutlineIcon/>}
                            label={<FormattedMessage defaultMessage='Edit'/>}
                            onClick={handleMenuItemEdit}
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
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: var(--spacing-xs);
    height: 60px;
    padding: 0 var(--spacing-l);
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    background: var(--center-channel-bg, #fff);
    cursor: ${({$clickable}) => ($clickable ? 'pointer' : 'default')};
    outline: none;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.04);
    }

    ${({$clickable}) =>
        $clickable &&
        `
        &:focus-visible {
            box-shadow: inset 0 0 0 2px rgba(var(--button-bg-rgb, 28, 88, 217), 0.4);
        }
    `}
`;

const RowMain = styled.div`
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: var(--spacing-xs);
    flex: 1;
    min-width: 0;
`;

const Avatar = styled(UserAvatar)`
    flex-shrink: 0;
`;

const NameColumn = styled.div`
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: var(--spacing-xs);
    flex: 1;
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

const BadgesColumn = styled.div`
    display: flex;
    flex-direction: row;
    gap: var(--spacing-xs);
    align-items: center;
    flex-shrink: 0;
`;

const BadgeTrigger = styled.span`
    display: inline-flex;
    flex-shrink: 0;
    cursor: default;
`;

const ActionsColumn = styled.div`
    flex-shrink: 0;
`;

export default AgentRow;
