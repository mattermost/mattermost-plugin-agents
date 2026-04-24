// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {SelectChannel} from '@/components/select';
import {AgentSchedule, MinScheduleIntervalSeconds, BoundParamTargetChannelSentinel} from '@/types/agents';
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

type IntervalUnit = 'hours' | 'days';

// Default new-schedule interval is 1 day. Hours/days are the only units
// exposed in the UI; the backend stores IntervalSeconds and enforces the
// MinScheduleIntervalSeconds lower bound regardless.
const defaultIntervalHours = 24;

type Props = {
    schedules: AgentSchedule[];
    onChange: (scheds: AgentSchedule[]) => void;
}

function newScheduleDraft(): AgentSchedule {
    return {
        intervalSeconds: defaultIntervalHours * 3600,
        prompt: '',
        targetChannelID: '',
        allowedTools: ['create_post'],
        boundParams: {create_post: {channel_id: BoundParamTargetChannelSentinel}},
        enabled: true,
    };
}

function intervalToParts(seconds: number): {value: number; unit: IntervalUnit} {
    // Prefer days when the interval is a clean multiple; otherwise hours.
    if (seconds > 0 && seconds % 86400 === 0) {
        return {value: seconds / 86400, unit: 'days'};
    }
    return {value: Math.max(1, Math.round(seconds / 3600)), unit: 'hours'};
}

function partsToInterval(value: number, unit: IntervalUnit): number {
    const perUnit = unit === 'days' ? 86400 : 3600;
    return Math.max(MinScheduleIntervalSeconds, value * perUnit);
}

function formatUnixTimestamp(unix: number | undefined): string {
    if (!unix) {
        return '';
    }
    return new Date(unix * 1000).toISOString();
}

function formatMillis(ms: number | undefined): string {
    if (!ms) {
        return '';
    }
    return new Date(ms).toISOString();
}

const SchedulesTab = (props: Props) => {
    const {schedules, onChange} = props;
    const intl = useIntl();

    const handleAdd = useCallback(() => {
        onChange([...schedules, newScheduleDraft()]);
    }, [schedules, onChange]);

    const handleRemove = useCallback((index: number) => {
        const next = schedules.slice();
        next.splice(index, 1);
        onChange(next);
    }, [schedules, onChange]);

    const handlePatch = useCallback((index: number, patch: Partial<AgentSchedule>) => {
        const next = schedules.map((s, i) => (i === index ? {...s, ...patch} : s));
        onChange(next);
    }, [schedules, onChange]);

    return (
        <SectionsContainer>
            <div>
                <SectionTitle>
                    <FormattedMessage defaultMessage='Schedules'/>
                </SectionTitle>
                <SectionDescription>
                    <FormattedMessage defaultMessage='Run this agent on a recurring interval (at least every 1 hour). The agent must call the create_post tool to post into the target channel.'/>
                </SectionDescription>
            </div>

            {schedules.length === 0 ? (
                <EmptyState>
                    <FormattedMessage defaultMessage='No schedules configured yet.'/>
                </EmptyState>
            ) : (
                schedules.map((sched, i) => {
                    const parts = intervalToParts(sched.intervalSeconds);
                    return (
                        <Row key={sched.id ?? `new-${i}`}>
                            <RowHeader>
                                <RowTitle>
                                    <FormattedMessage defaultMessage='Schedule'/>
                                    {' '}{'#'}{i + 1}
                                    {sched.lastError ? <ErrorChip title={sched.lastError}>
                                        <FormattedMessage defaultMessage='Error'/>
                                    </ErrorChip> : null}
                                </RowTitle>
                                <RowControls>
                                    <ToggleLabel>
                                        <input
                                            type='checkbox'
                                            checked={sched.enabled}
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
                                        <FormattedMessage defaultMessage='Run every'/>
                                    </FieldLabel>
                                    <IntervalRow>
                                        <IntervalInput
                                            type='number'
                                            min={1}
                                            value={parts.value}
                                            onChange={(e) => handlePatch(i, {
                                                intervalSeconds: partsToInterval(Number(e.target.value) || 1, parts.unit),
                                            })}
                                        />
                                        <UnitSelect
                                            value={parts.unit}
                                            onChange={(e) => handlePatch(i, {
                                                intervalSeconds: partsToInterval(parts.value, e.target.value as IntervalUnit),
                                            })}
                                        >
                                            <option value='hours'>{intl.formatMessage({defaultMessage: 'hours'})}</option>
                                            <option value='days'>{intl.formatMessage({defaultMessage: 'days'})}</option>
                                        </UnitSelect>
                                    </IntervalRow>
                                </Field>

                                <Field>
                                    <FieldLabel>
                                        <FormattedMessage defaultMessage='Target channel'/>
                                    </FieldLabel>
                                    <SelectChannel
                                        channelIDs={sched.targetChannelID ? [sched.targetChannelID] : []}
                                        onChangeChannelIDs={(ids) => handlePatch(i, {
                                            targetChannelID: ids[ids.length - 1] ?? '',
                                            boundParams: defaultBoundParamsForAllowedTools(sched.allowedTools),
                                        })}
                                    />
                                </Field>

                                <Field>
                                    <FieldLabel>
                                        <FormattedMessage defaultMessage='Prompt'/>
                                    </FieldLabel>
                                    <PromptTextarea
                                        rows={5}
                                        placeholder={intl.formatMessage({defaultMessage: 'Describe what the agent should do at each run. {{.Now}} expands to the current UTC timestamp.'})}
                                        value={sched.prompt}
                                        onChange={(e) => handlePatch(i, {prompt: e.target.value})}
                                    />
                                </Field>

                                <Field>
                                    <FieldLabel>
                                        <FormattedMessage defaultMessage='Allowed tools (comma-separated)'/>
                                    </FieldLabel>
                                    <ToolsInput
                                        placeholder='create_post, search_posts'
                                        value={serializeAllowedTools(sched.allowedTools)}
                                        onChange={(e) => {
                                            const parsed = parseAllowedTools(e.target.value);
                                            handlePatch(i, {
                                                allowedTools: parsed,
                                                boundParams: defaultBoundParamsForAllowedTools(parsed),
                                            });
                                        }}
                                    />
                                </Field>

                                <Health>
                                    <HealthLine>
                                        <HealthLabel>
                                            <FormattedMessage defaultMessage='Next fire (UTC)'/>
                                        </HealthLabel>
                                        <HealthValue>{formatUnixTimestamp(sched.nextFireAt) || '—'}</HealthValue>
                                    </HealthLine>
                                    <HealthLine>
                                        <HealthLabel>
                                            <FormattedMessage defaultMessage='Last fire (UTC)'/>
                                        </HealthLabel>
                                        <HealthValue>{formatMillis(sched.lastFireAt) || '—'}</HealthValue>
                                    </HealthLine>
                                    {sched.lastError ? (
                                        <HealthLine>
                                            <HealthLabel>
                                                <FormattedMessage defaultMessage='Last error'/>
                                            </HealthLabel>
                                            <HealthValue>{sched.lastError}</HealthValue>
                                        </HealthLine>
                                    ) : null}
                                </Health>
                            </RowBody>
                        </Row>
                    );
                })
            )}

            <BottomActions>
                <AddTriggerButton
                    type='button'
                    onClick={handleAdd}
                >
                    <FormattedMessage defaultMessage='Add schedule'/>
                </AddTriggerButton>
            </BottomActions>
        </SectionsContainer>
    );
};

// --- Styled Components ---

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

const IntervalRow = styled.div`
    display: flex;
    gap: 8px;
    align-items: center;
`;

const IntervalInput = styled.input`
    width: 100px;
    padding: 8px 12px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    font-size: 14px;
    background: var(--center-channel-bg);
    color: var(--center-channel-color);
`;

const UnitSelect = styled.select`
    padding: 8px 12px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    font-size: 14px;
    background: var(--center-channel-bg);
    color: var(--center-channel-color);
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

const Health = styled.div`
    margin-top: 4px;
    padding: 8px 12px;
    background: rgba(var(--center-channel-color-rgb), 0.04);
    border-radius: 4px;
    display: flex;
    flex-direction: column;
    gap: 4px;
`;

const HealthLine = styled.div`
    display: flex;
    gap: 12px;
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const HealthLabel = styled.span`
    font-weight: 600;
    min-width: 110px;
`;

const HealthValue = styled.span`
    font-family: var(--font-family-monospace, monospace);
`;

const ErrorChip = styled.span`
    background: rgba(var(--dnd-indicator-rgb, 210, 75, 78), 0.12);
    color: var(--dnd-indicator, #D24B4E);
    padding: 2px 6px;
    font-size: 11px;
    font-weight: 600;
    border-radius: 4px;
`;

export default SchedulesTab;
