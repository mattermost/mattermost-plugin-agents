// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {ChannelAccessLevel, UserAccessLevel} from '@/components/system_console/bot';
import {ChannelAccessLevelItem, UserAccessLevelItem} from '@/components/system_console/llm_access';
import {ItemList} from '@/components/system_console/item';
import {SelectUser} from '@/components/select';

import {AgentDraft} from '../agent_config_modal';

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
            <Section>
                <SectionTitle>
                    <FormattedMessage defaultMessage='Channel access'/>
                </SectionTitle>
                <SectionDescription>
                    <FormattedMessage defaultMessage='Control which channels this agent can be mentioned in.'/>
                </SectionDescription>
                <ItemList>
                    <ChannelAccessLevelItem
                        label={intl.formatMessage({defaultMessage: 'Channel access level'})}
                        level={draft.channelAccessLevel}
                        onChangeLevel={(level: ChannelAccessLevel) => onChange({channelAccessLevel: level})}
                        channelIDs={draft.channelIds}
                        onChangeChannelIDs={(ids: string[]) => onChange({channelIds: ids})}
                    />
                </ItemList>
            </Section>

            {/* User Access Section */}
            <Section>
                <SectionTitle>
                    <FormattedMessage defaultMessage='User access'/>
                </SectionTitle>
                <SectionDescription>
                    <FormattedMessage defaultMessage='Control which users can interact with this agent.'/>
                </SectionDescription>
                <ItemList>
                    <UserAccessLevelItem
                        label={intl.formatMessage({defaultMessage: 'User access level'})}
                        level={draft.userAccessLevel}
                        onChangeLevel={(level: UserAccessLevel) => onChange({userAccessLevel: level})}
                        userIDs={draft.userIds}
                        teamIDs={draft.teamIds}
                        onChangeIDs={(userIds: string[], teamIds: string[]) => onChange({userIds, teamIds})}
                    />
                </ItemList>
            </Section>

            {/* Admin Access Section */}
            <Section>
                <SectionTitle>
                    <FormattedMessage defaultMessage='Agent admins'/>
                </SectionTitle>
                <SectionDescription>
                    <FormattedMessage defaultMessage='These users can edit and delete this agent. The agent creator is always an admin.'/>
                </SectionDescription>
                <SelectUser
                    userIDs={draft.adminUserIds}
                    teamIDs={[]}
                    onChangeIDs={(
                        userIds: string[],
                        _teamIds: string[], // eslint-disable-line @typescript-eslint/no-unused-vars -- SelectUser passes (userIds, teamIds)
                    ) => onChange({adminUserIds: userIds})}
                />
            </Section>
        </SectionsContainer>
    );
};

// --- Styled Components ---

const SectionsContainer = styled.div`
    display: flex;
    flex-direction: column;
    gap: 32px;
`;

const Section = styled.div`
    display: flex;
    flex-direction: column;
    gap: 16px;
`;

const SectionTitle = styled.h3`
    font-size: 16px;
    font-weight: 600;
    color: var(--center-channel-color);
    margin: 0;
`;

const SectionDescription = styled.p`
    font-size: 14px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    margin: 0;
    line-height: 20px;
`;

export default AccessTab;
