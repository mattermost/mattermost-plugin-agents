// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {SelectChannel} from '@/components/select';
import {
    AgentSubscription,
    BoundParamTargetChannelSentinel,
    SubscriptionEventMessagePosted,
} from '@/types/agents';
import {TertiaryButton} from '@/components/assets/buttons';

import {
    AddTriggerButton,
    BottomActions,
    EmptyState,
    Row,
    RowBody,
    RowHeader,
    SectionDescription,
    SectionTitle,
    SectionsContainer,
    defaultBoundParamsForAllowedTools,
    parseAllowedTools,
    serializeAllowedTools,
} from './triggers_shared';

type Props = {
    subscriptions: AgentSubscription[];
    onChange: (subs: AgentSubscription[]) => void;
}

// newSubscriptionDraft returns a blank subscription with sensible defaults:
// message_posted event (the only V1 option), enabled, no bound params yet.
// The backend assigns an ID on save when the draft's id field is empty.
function newSubscriptionDraft(): AgentSubscription {
    return {
        event: SubscriptionEventMessagePosted,
        scopeChannelID: '',
        targetChannelID: '',
        prompt: '',
        allowedTools: ['create_post'],
        boundParams: {create_post: {channel_id: BoundParamTargetChannelSentinel}},
        enabled: true,
    };
}

const SubscriptionsTab = (props: Props) => {
    const {subscriptions, onChange} = props;
    const intl = useIntl();

    const handleAdd = useCallback(() => {
        onChange([...subscriptions, newSubscriptionDraft()]);
    }, [subscriptions, onChange]);

    const handleRemove = useCallback((index: number) => {
        const next = subscriptions.slice();
        next.splice(index, 1);
        onChange(next);
    }, [subscriptions, onChange]);

    const handlePatch = useCallback((index: number, patch: Partial<AgentSubscription>) => {
        const next = subscriptions.map((s, i) => (i === index ? {...s, ...patch} : s));
        onChange(next);
    }, [subscriptions, onChange]);

    return (
        <SectionsContainer>
            <div>
                <SectionTitle>
                    <FormattedMessage defaultMessage='Subscriptions'/>
                </SectionTitle>
                <SectionDescription>
                    <FormattedMessage defaultMessage='Run this agent automatically when a message is posted to a specific channel. The agent receives the post and must call the create_post tool to reply.'/>
                </SectionDescription>
            </div>

            {subscriptions.length === 0 ? (
                <EmptyState>
                    <FormattedMessage defaultMessage='No subscriptions configured yet.'/>
                </EmptyState>
            ) : (
                subscriptions.map((sub, i) => (
                    <Row key={sub.id ?? `new-${i}`}>
                        <RowHeader>
                            <RowTitle>
                                <FormattedMessage defaultMessage='Subscription'/>
                                {' '}{'#'}{i + 1}
                                {sub.lastError ? <ErrorChip title={sub.lastError}>
                                    <FormattedMessage defaultMessage='Error'/>
                                </ErrorChip> : null}
                            </RowTitle>
                            <RowControls>
                                <ToggleLabel>
                                    <input
                                        type='checkbox'
                                        checked={sub.enabled}
                                        onChange={(e) => handlePatch(i, {enabled: e.target.checked})}
                                    />
                                    <FormattedMessage defaultMessage='Enabled'/>
                                </ToggleLabel>
                                <RemoveButton
                                    type='button'
                                    onClick={() => handleRemove(i)}
                                >
                                    <FormattedMessage defaultMessage='Remove'/>
                                </RemoveButton>
                            </RowControls>
                        </RowHeader>

                        <RowBody>
                            <Field>
                                <FieldLabel>
                                    <FormattedMessage defaultMessage='Scope channel (triggering channel)'/>
                                </FieldLabel>
                                <SelectChannel
                                    channelIDs={sub.scopeChannelID ? [sub.scopeChannelID] : []}
                                    onChangeChannelIDs={(ids) => handlePatch(i, {scopeChannelID: ids[ids.length - 1] ?? ''})}
                                />
                            </Field>

                            <Field>
                                <FieldLabel>
                                    <FormattedMessage defaultMessage='Target channel (where the agent posts)'/>
                                </FieldLabel>
                                <SelectChannel
                                    channelIDs={sub.targetChannelID ? [sub.targetChannelID] : []}
                                    onChangeChannelIDs={(ids) => handlePatch(i, {
                                        targetChannelID: ids[ids.length - 1] ?? '',
                                        boundParams: defaultBoundParamsForAllowedTools(sub.allowedTools),
                                    })}
                                />
                            </Field>

                            <Field>
                                <FieldLabel>
                                    <FormattedMessage defaultMessage='Prompt'/>
                                </FieldLabel>
                                <PromptTextarea
                                    rows={5}
                                    placeholder={intl.formatMessage({defaultMessage: 'You may reference {{.Username}}, {{.Message}}, {{.PostID}}, and {{.Now}}.'})}
                                    value={sub.prompt}
                                    onChange={(e) => handlePatch(i, {prompt: e.target.value})}
                                />
                            </Field>

                            <Field>
                                <FieldLabel>
                                    <FormattedMessage defaultMessage='Allowed tools (comma-separated)'/>
                                </FieldLabel>
                                <ToolsInput
                                    placeholder='create_post, search_posts'
                                    value={serializeAllowedTools(sub.allowedTools)}
                                    onChange={(e) => {
                                        const parsed = parseAllowedTools(e.target.value);
                                        handlePatch(i, {
                                            allowedTools: parsed,
                                            boundParams: defaultBoundParamsForAllowedTools(parsed),
                                        });
                                    }}
                                />
                            </Field>
                        </RowBody>
                    </Row>
                ))
            )}

            <BottomActions>
                <AddTriggerButton
                    type='button'
                    onClick={handleAdd}
                >
                    <FormattedMessage defaultMessage='Add subscription'/>
                </AddTriggerButton>
            </BottomActions>
        </SectionsContainer>
    );
};

// --- Styled Components (tab-local; shared primitives come from triggers_shared) ---

const RowTitle = styled.div`
    font-weight: 600;
    font-size: 14px;
    color: var(--center-channel-color);
    display: flex;
    align-items: center;
    gap: 8px;
`;

const RowControls = styled.div`
    display: flex;
    align-items: center;
    gap: 16px;
`;

const ToggleLabel = styled.label`
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 13px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    cursor: pointer;
`;

const RemoveButton = styled(TertiaryButton)`
    height: 32px;
    padding: 0 12px;
`;

const Field = styled.div`
    display: flex;
    flex-direction: column;
    gap: 6px;
`;

const FieldLabel = styled.label`
    font-size: 12px;
    font-weight: 600;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    text-transform: uppercase;
    letter-spacing: 0.5px;
`;

const PromptTextarea = styled.textarea`
    width: 100%;
    box-sizing: border-box;
    padding: 10px 12px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    font-family: inherit;
    font-size: 14px;
    resize: vertical;
    background: var(--center-channel-bg);
    color: var(--center-channel-color);
`;

const ToolsInput = styled.input`
    width: 100%;
    box-sizing: border-box;
    padding: 8px 12px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    font-family: inherit;
    font-size: 14px;
    background: var(--center-channel-bg);
    color: var(--center-channel-color);
`;

const ErrorChip = styled.span`
    background: rgba(var(--dnd-indicator-rgb, 210, 75, 78), 0.12);
    color: var(--dnd-indicator, #D24B4E);
    padding: 2px 6px;
    font-size: 11px;
    font-weight: 600;
    border-radius: 4px;
`;

export default SubscriptionsTab;
