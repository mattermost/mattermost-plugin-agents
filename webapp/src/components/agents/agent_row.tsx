// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useRef, useEffect, useCallback} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {
    DotsHorizontalIcon,
    PencilOutlineIcon,
    TrashCanOutlineIcon,
} from '@mattermost/compass-icons/components';

import {getProfilePictureUrl} from '@/client';

import {UserAgent, ServiceInfo} from '@/types/agents';

type Props = {
    agent: UserAgent;
    services: ServiceInfo[];
    canManage: boolean;
    onEdit: (agent: UserAgent) => void;
    onDelete: (agent: UserAgent) => void;
}

const AgentRow = (props: Props) => {
    const {agent, services, canManage, onEdit, onDelete} = props;
    const [menuOpen, setMenuOpen] = useState(false);
    const menuRef = useRef<HTMLDivElement>(null);
    const intl = useIntl();

    const avatarUrl = getProfilePictureUrl(agent.botUserID ?? '', 0);
    const autoEnableNewMCPTools = agent.autoEnableNewMCPTools ?? false;
    const toolCount = autoEnableNewMCPTools ? 0 : (agent.enabledMCPTools?.length ?? 0);
    const service = services.find((s) => s.id === agent.serviceID);
    const serviceUnavailable = agent.serviceID && !service;

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

    // Close menu on outside click
    useEffect(() => {
        if (!menuOpen) {
            return () => {
                // No mousedown listener while menu is closed
            };
        }
        const handler = (e: MouseEvent) => {
            if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
                setMenuOpen(false);
            }
        };
        document.addEventListener('mousedown', handler);
        return () => document.removeEventListener('mousedown', handler);
    }, [menuOpen]);

    const handleEdit = useCallback(() => {
        setMenuOpen(false);
        onEdit(agent);
    }, [agent, onEdit]);

    const handleDelete = useCallback(() => {
        setMenuOpen(false);
        onDelete(agent);
    }, [agent, onDelete]);

    return (
        <RowContainer>
            <Avatar
                src={avatarUrl}
                alt={agent.displayName || agent.name || 'agent avatar'}
            />
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
            {canManage && (
                <ActionsColumn ref={menuRef}>
                    <MenuButton
                        onClick={(e) => {
                            e.stopPropagation();
                            setMenuOpen((prev) => !prev);
                        }}
                        aria-label={intl.formatMessage({defaultMessage: 'Agent actions'})}
                    >
                        <DotsHorizontalIcon size={18}/>
                    </MenuButton>
                    {menuOpen && (
                        <DropdownMenu>
                            <MenuItem onClick={handleEdit}>
                                <PencilOutlineIcon size={16}/>
                                <FormattedMessage defaultMessage='Edit'/>
                            </MenuItem>
                            <MenuItemDanger onClick={handleDelete}>
                                <TrashCanOutlineIcon size={16}/>
                                <FormattedMessage defaultMessage='Delete'/>
                            </MenuItemDanger>
                        </DropdownMenu>
                    )}
                </ActionsColumn>
            )}
        </RowContainer>
    );
};

// --- Styled Components ---

const RowContainer = styled.div`
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: 8px;
    height: 60px;
    padding: 0 16px;
    border-radius: 4px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    background: var(--center-channel-bg, #fff);

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.04);
    }
`;

const Avatar = styled.img`
    width: 24px;
    height: 24px;
    border-radius: 50%;
    flex-shrink: 0;
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

const ActionsColumn = styled.div`
    position: relative;
    flex-shrink: 0;
`;

const MenuButton = styled.button`
    width: 32px;
    height: 32px;
    padding: 8px;
    border: none;
    background: transparent;
    border-radius: 4px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.08);
        color: rgba(var(--center-channel-color-rgb), 0.72);
    }
`;

const DropdownMenu = styled.div`
    position: absolute;
    top: 100%;
    right: 0;
    z-index: 10;
    min-width: 160px;
    padding: 4px 0;
    margin-top: 4px;
    background: var(--center-channel-bg, #fff);
    border-radius: 4px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    box-shadow: 0px 8px 24px rgba(0, 0, 0, 0.12);
`;

const MenuItem = styled.button`
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    padding: 8px 16px;
    border: none;
    background: transparent;
    font-size: 14px;
    color: var(--center-channel-color);
    cursor: pointer;
    text-align: left;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.08);
    }
`;

const MenuItemDanger = styled(MenuItem)`
    color: var(--dnd-indicator, #D24B4E);

    &:hover {
        background: rgba(var(--dnd-indicator-rgb, 210, 75, 78), 0.08);
    }
`;

export default AgentRow;
