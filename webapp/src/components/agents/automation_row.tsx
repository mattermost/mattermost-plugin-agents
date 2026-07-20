// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {
    ClockOutlineIcon,
    MessageTextOutlineIcon,
    AccountPlusOutlineIcon,
    MessagePlusOutlineIcon,
    AccountMultiplePlusOutlineIcon,
} from '@mattermost/compass-icons/components';

import {ToggleSwitch} from '@/components/toggle_switch';

import {
    Automation,
    getTriggerType,
} from '@/types/automations';

import Avatar from './avatar';

import RowActionsMenu from './row_actions_menu';

type Props = {
    item: Automation;
    agentDisplayName?: string;
    agentUserId?: string;
    creatorDisplayName?: string;
    triggerLabel: string;
    onToggle: (id: string, enabled: boolean) => void;
    onEdit: (item: Automation) => void;
    onDelete: (item: Automation) => void;
};

const AutomationRow = ({
    item,
    agentDisplayName,
    agentUserId,
    creatorDisplayName,
    triggerLabel,
    onToggle,
    onEdit,
    onDelete,
}: Props) => {
    const intl = useIntl();
    const resolvedAgentName = agentDisplayName || '';
    const resolvedCreatorName = creatorDisplayName || '';

    const handleToggle = useCallback((enabled: boolean) => {
        onToggle(item.id, enabled);
    }, [item.id, onToggle]);

    const handleEdit = useCallback(() => {
        onEdit(item);
    }, [item, onEdit]);

    const handleDelete = useCallback(() => {
        onDelete(item);
    }, [item, onDelete]);

    return (
        <Row>
            <IconBox aria-hidden={true}>
                {iconForTrigger(getTriggerType(item.trigger))}
            </IconBox>
            <TextBlock>
                <RowTitle>{item.name}</RowTitle>
                <RowMeta>
                    <span>{triggerLabel}</span>
                    {resolvedCreatorName && (
                        <>
                            <MetaSeparator>{'·'}</MetaSeparator>
                            <span>
                                <FormattedMessage
                                    defaultMessage='By {name}'
                                    values={{name: resolvedCreatorName}}
                                />
                            </span>
                        </>
                    )}
                    {resolvedAgentName && (
                        <>
                            <MetaSeparator>{'·'}</MetaSeparator>
                            <AgentChip>
                                <Avatar
                                    userId={agentUserId || ''}
                                    size='small'
                                />
                                {resolvedAgentName}
                            </AgentChip>
                        </>
                    )}
                </RowMeta>
            </TextBlock>
            <Actions>
                <ToggleSwitch
                    checked={item.enabled}
                    onChange={handleToggle}
                    size='medium'
                    ariaLabel={intl.formatMessage(
                        {defaultMessage: 'Enable {title}'},
                        {title: item.name},
                    )}
                />
                <RowActionsMenu
                    ariaLabel={intl.formatMessage({defaultMessage: 'More actions'})}
                    onEdit={handleEdit}
                    onDelete={handleDelete}
                />
            </Actions>
        </Row>
    );
};

function iconForTrigger(triggerType: ReturnType<typeof getTriggerType>) {
    switch (triggerType) {
    case 'membership_changed':
        return <AccountPlusOutlineIcon size={20}/>;
    case 'channel_created':
        return <MessagePlusOutlineIcon size={20}/>;
    case 'user_joined_team':
        return <AccountMultiplePlusOutlineIcon size={20}/>;
    case 'message_posted':
        return <MessageTextOutlineIcon size={20}/>;
    case 'schedule':
    default:
        return <ClockOutlineIcon size={20}/>;
    }
}

const Row = styled.div`
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: 16px;
    padding: 16px 0;
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.08);

    &:last-child {
        border-bottom: none;
    }
`;

const IconBox = styled.div`
    display: flex;
    align-items: center;
    justify-content: center;
    width: 40px;
    height: 40px;
    flex-shrink: 0;
    border-radius: 4px;
    background: rgba(var(--center-channel-color-rgb), 0.04);
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const TextBlock = styled.div`
    display: flex;
    flex-direction: column;
    gap: 2px;
    flex: 1;
    min-width: 0;
`;

const RowTitle = styled.div`
    font-family: 'Open Sans', sans-serif;
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    color: var(--center-channel-color);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const RowMeta = styled.div`
    display: flex;
    flex-direction: row;
    align-items: center;
    flex-wrap: wrap;
    gap: 0;
    font-family: 'Open Sans', sans-serif;
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const MetaSeparator = styled.span`
    margin: 0 6px;
`;

const AgentChip = styled.span`
    display: inline-flex;
    align-items: center;
    gap: 4px;
`;

const Actions = styled.div`
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: 8px;
    flex-shrink: 0;
`;

export default AutomationRow;
