// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useCallback} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {UserAgent, ServiceInfo} from '@/types/agents';

import Avatar from './avatar';
import RowActionsMenu from './row_actions_menu';

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

    const autoEnableNewMCPTools = agent.autoEnableNewMCPTools ?? false;
    const toolCount = autoEnableNewMCPTools ? 0 : (agent.enabledMCPTools?.length ?? 0);
    const service = services.find((s) => s.id === agent.serviceID);

    // Only flag a missing service once the list has loaded; users without
    // agent-management permission never fetch it, so empty means unknown.
    const serviceUnavailable = servicesLoaded && agent.serviceID && !service;

    let mcpBadge: React.ReactNode = null;
    if (autoEnableNewMCPTools) {
        mcpBadge = (
            <Badge>
                <FormattedMessage defaultMessage='All MCP tools'/>
            </Badge>
        );
    } else if (toolCount > 0) {
        mcpBadge = (
            <Badge>
                {intl.formatMessage(
                    {defaultMessage: '{count, plural, one {# tool} other {# tools}}'},
                    {count: toolCount},
                )}
            </Badge>
        );
    }

    const handleEdit = useCallback(() => {
        onEdit(agent);
    }, [agent, onEdit]);

    const handleDelete = useCallback(() => {
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
            if (!canManage || menuOpen) {
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
                <Avatar userId={agent.botUserID ?? ''}/>
                <NameColumn>
                    <DisplayName>{agent.displayName}</DisplayName>
                    <Username>{'@'}{agent.name}</Username>
                </NameColumn>
                <BadgesColumn>
                    {serviceUnavailable && (
                        <ServiceWarningBadge>
                            <FormattedMessage defaultMessage='Service unavailable'/>
                        </ServiceWarningBadge>
                    )}
                    {mcpBadge}
                </BadgesColumn>
            </RowMain>
            {canManage && (
                <RowActionsMenu
                    ariaLabel={intl.formatMessage({defaultMessage: 'Agent actions'})}
                    onEdit={handleEdit}
                    onDelete={handleDelete}
                    onOpenChange={setMenuOpen}
                />
            )}
        </RowContainer>
    );
};

// --- Styled Components ---

const RowContainer = styled.div<{$clickable: boolean}>`
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: 8px;
    height: 60px;
    padding: 0 16px;
    border-radius: 4px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
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
            box-shadow: 0 0 0 2px rgba(var(--button-bg-rgb, 28, 88, 217), 0.4);
        }
    `}
`;

const RowMain = styled.div`
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: 8px;
    flex: 1;
    min-width: 0;
`;

const NameColumn = styled.div`
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: 8px;
    flex: 1;
    min-width: 0;
`;

const DisplayName = styled.div`
    font-family: 'Open Sans', sans-serif;
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    color: var(--center-channel-color);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
`;

const Username = styled.div`
    font-family: 'Open Sans', sans-serif;
    font-size: 14px;
    font-weight: 400;
    line-height: 20px;
    color: rgba(var(--center-channel-color-rgb), 0.75);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
`;

const BadgesColumn = styled.div`
    display: flex;
    flex-direction: row;
    gap: 8px;
    align-items: center;
    flex-shrink: 0;
`;

const ServiceWarningBadge = styled.span`
    padding: 2px 8px;
    border-radius: 4px;
    background: rgba(var(--dnd-indicator-rgb, 210, 75, 78), 0.08);
    color: var(--dnd-indicator, #D24B4E);
    font-size: 12px;
    white-space: nowrap;
`;

const Badge = styled.span`
    font-family: 'Open Sans', sans-serif;
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.75);
    white-space: nowrap;
`;

export default AgentRow;
