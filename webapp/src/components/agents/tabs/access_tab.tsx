// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';

import {ChannelAccessLevel, UserAccessLevel} from '@/components/system_console/bot';
import {ChannelAccessLevelItem, UserAccessLevelItem} from '@/components/system_console/llm_access';
import {FormRow, ItemLabel, ItemList} from '@/components/system_console/item';
import {SelectUser} from '@/components/select';
import PolicyEditor from '@/components/access_control/policy_editor';
import {useIsLicensedFor} from '@/license';

import {AgentDraft} from '../agent_config_view';

type Props = {
    draft: AgentDraft;
    onChange: (updates: Partial<AgentDraft>) => void;

    /** Soft-lock Access controls while service account auth is on for non-admins. */
    serviceAccountFieldsLocked: boolean;

    // Baseline saved access level, used to detect switching away from
    // attribute-based access before save.
    baselineUserAccessLevel: UserAccessLevel;

    // Stable agent ID; undefined while creating (policy authoring needs a
    // saved agent).
    agentId?: string;
    abacSupported: boolean;
    isSystemAdmin: boolean;
}

const AccessTab = (props: Props) => {
    const {draft, onChange, serviceAccountFieldsLocked, baselineUserAccessLevel, agentId, abacSupported, isSystemAdmin} = props;
    const intl = useIntl();
    const abacLicensed = useIsLicensedFor('attribute_based_access');

    const attributeBasedSelected = draft.userAccessLevel === UserAccessLevel.AttributeBased;
    const switchingAwayFromAttributeBased =
        Boolean(agentId) &&
        baselineUserAccessLevel === UserAccessLevel.AttributeBased &&
        draft.userAccessLevel !== UserAccessLevel.AttributeBased;

    const policyEditor = agentId && abacSupported ? (
        <PolicyEditor
            resourceType='agent'
            resourceId={agentId}
            resourceDisplayName={draft.displayName}
            allowSimplified={true}
            allowAdvanced={isSystemAdmin}
            agentIdForAuthz={agentId}
            hideWhenEmpty={!attributeBasedSelected}
            allowEdit={abacLicensed}
        />
    ) : null;

    let attributeBasedContent: React.ReactNode = null;
    if (attributeBasedSelected && !policyEditor) {
        if (abacSupported) {
            attributeBasedContent = (
                <PolicyNote
                    type='info'
                    title={<FormattedMessage defaultMessage='Save the agent first, then define who can use it. Until a policy is defined, all users can use this agent.'/>}
                />
            );
        } else {
            attributeBasedContent = (
                <PolicyNote
                    type='danger'
                    title={<FormattedMessage defaultMessage='Attribute-based access is configured but not available on this server; users are currently denied access.'/>}
                />
            );
        }
    }

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
                    disabled={serviceAccountFieldsLocked}
                    helpText={<FormattedMessage defaultMessage='Control which channels this agent can be mentioned in.'/>}
                />
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
                    disabled={serviceAccountFieldsLocked}
                    showAttributeBased={(abacSupported && abacLicensed) || attributeBasedSelected}
                    attributeBasedDescription={attributeBasedContent}
                    helpText={<FormattedMessage defaultMessage='Control which users can interact with this agent.'/>}
                />
            </ItemList>

            {switchingAwayFromAttributeBased && (
                <SwitchAwayWarning
                    type='warning'
                    title={<FormattedMessage defaultMessage="Saving will remove this agent's attribute-based access policy. Access will be controlled only by the setting above."/>}
                />
            )}

            {policyEditor && (
                <PolicyEditorWrapper disabled={serviceAccountFieldsLocked || switchingAwayFromAttributeBased}>
                    {policyEditor}
                </PolicyEditorWrapper>
            )}

            {/* Admin Access Section */}
            <ItemList>
                <FormRow>
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
                            disabled={serviceAccountFieldsLocked}
                        />
                        <HelpTextInline>
                            <FormattedMessage defaultMessage='These users can edit and delete this agent. The agent creator is always an admin.'/>
                        </HelpTextInline>
                    </AdminsColumn>
                </FormRow>
            </ItemList>
        </SectionsContainer>
    );
};

// --- Styled Components ---

const SectionsContainer = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xxxl);
`;

const AdminsColumn = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs);
`;

const HelpTextInline = styled.div`
    font-size: var(--font-size-75);
    font-weight: var(--font-weight-regular);
    line-height: var(--line-height-75);
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const PolicyNote = styled(SectionNotice)`
    margin-top: var(--spacing-xs);
`;

const SwitchAwayWarning = styled(SectionNotice)`
    width: 90%;
    margin-top: var(--spacing-m);
`;

const PolicyEditorWrapper = styled.fieldset`
    margin-top: var(--spacing-m);
    padding: 0;
    border: 0;
    min-inline-size: 0;
    width: 90%;

    /* PolicyEditor returns null when hidden/empty; don't keep a flex gap slot. */
    &:empty {
        display: none;
    }
`;

export default AccessTab;
