// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {ChannelAccessLevel, UserAccessLevel} from '@/components/system_console/bot';
import {ChannelAccessLevelItem, UserAccessLevelItem} from '@/components/system_console/llm_access';
import {ItemLabel, ItemList} from '@/components/system_console/item';
import {SelectUser} from '@/components/select';

import {AgentDraft} from '../agent_config_view';

type Props = {
    draft: AgentDraft;
    onChange: (updates: Partial<AgentDraft>) => void;
}

const AccessTab = (props: Props) => {
    const {draft, onChange} = props;
    const intl = useIntl();

    return (
        <SectionsContainer>
            {/* Channel Access Section */}
            <ItemList>
                <ChannelAccessLevelItem
                    label={intl.formatMessage({defaultMessage: 'Channel access'})}
                    level={draft.channelAccessLevel}
                    onChangeLevel={(level: ChannelAccessLevel) => onChange({channelAccessLevel: level})}
                    channelIDs={draft.channelIds}
                    onChangeChannelIDs={(ids: string[]) => onChange({channelIds: ids})}
                />
                <HelpTextInSecondColumn>
                    <FormattedMessage defaultMessage='Control which channels this agent can be mentioned in.'/>
                </HelpTextInSecondColumn>
            </ItemList>

            {/* User Access Section */}
            <ItemList>
                <UserAccessLevelItem
                    label={intl.formatMessage({defaultMessage: 'User access'})}
                    level={draft.userAccessLevel}
                    onChangeLevel={(level: UserAccessLevel) => onChange({userAccessLevel: level})}
                    userIDs={draft.userIds}
                    teamIDs={draft.teamIds}
                    onChangeIDs={(userIds: string[], teamIds: string[]) => onChange({userIds, teamIds})}
                />
                <HelpTextInSecondColumn>
                    <FormattedMessage defaultMessage='Control which users can interact with this agent.'/>
                </HelpTextInSecondColumn>
            </ItemList>

            {/* Admin Access Section */}
            <ItemList>
                <ItemLabel>
                    <FormattedMessage defaultMessage='Agent admins'/>
                </ItemLabel>
                <AdminsColumn>
                    <SelectUser
                        userIDs={draft.adminUserIds}
                        teamIDs={[]}
                        onChangeIDs={(
                            userIds: string[],
                            _teamIds: string[], // eslint-disable-line @typescript-eslint/no-unused-vars -- SelectUser passes (userIds, teamIds)
                        ) => onChange({adminUserIds: userIds})}
                    />
                    <HelpTextInline>
                        <FormattedMessage defaultMessage='These users can edit and delete this agent. The agent creator is always an admin.'/>
                    </HelpTextInline>
                </AdminsColumn>
            </ItemList>
        </SectionsContainer>
    );
};

// --- Styled Components ---

const SectionsContainer = styled.div`
    display: flex;
    flex-direction: column;
    gap: 32px;
`;

const HelpTextInSecondColumn = styled.div`
    grid-column: 2;
    margin-top: -16px;
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const AdminsColumn = styled.div`
    display: flex;
    flex-direction: column;
    gap: 8px;
`;

const HelpTextInline = styled.div`
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

export default AccessTab;
