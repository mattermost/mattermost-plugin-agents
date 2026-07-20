// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useMemo, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {getAgents} from '@/client';
import {SelectChannel, SingleSelect, BaseSelectOption} from '@/components/select';
import {UserAgent} from '@/types/agents';
import {
    AIProviderTypeAgent,
    Automation,
    AutomationUpdate,
    getAIPromptAction,
    getTriggerChannelID,
    getTriggerType,
    Trigger,
    TriggerType,
} from '@/types/automations';

import AgentSelector from './agent_selector';
import ConfigViewShell, {ConfigViewTab} from './config_view_shell';
import TriggerSelector from './trigger_selector';

type Tab = 'chat' | 'settings';

type Props = {
    automation: Automation;
    onBack: () => void;
    onSaved: (update: AutomationUpdate) => void;
};

const DISCARD_CHANGES_TITLE_ID = 'discard-automation-changes-title';

const DEFAULT_PROMPT = 'Post a friendly reminder asking the team to drop their standup update in the thread before 10:00 AM.';
const DEFAULT_CONTEXT = 'All public channels in the team + invocation context';

const INTERVAL_HOURLY = '1h';
const INTERVAL_DAILY = '24h';
const INTERVAL_WEEKLY = '168h';

function cloneAutomation(automation: Automation): Automation {
    return JSON.parse(JSON.stringify(automation));
}

function automationsEqual(a: Automation, b: Automation): boolean {
    return JSON.stringify(a) === JSON.stringify(b);
}

function toUpdate(automation: Automation): AutomationUpdate {
    return {
        name: automation.name,
        enabled: automation.enabled,
        trigger: automation.trigger,
        actions: automation.actions,
    };
}

function ensureAIPromptAction(automation: Automation): Automation {
    const existing = getAIPromptAction(automation);
    if (existing) {
        return automation;
    }
    return {
        ...automation,
        actions: [
            {
                id: 'run-agent',
                ai_prompt: {
                    prompt: DEFAULT_PROMPT,
                    provider_type: AIProviderTypeAgent,
                    provider_id: '',
                },
            },
        ],
    };
}

function buildTrigger(type: TriggerType, previous: Trigger): Trigger {
    const channelId = getTriggerChannelID(previous);
    const teamId = previous.channel_created?.team_id ||
        previous.user_joined_team?.team_id ||
        '';
    const interval = previous.schedule?.interval || INTERVAL_DAILY;
    const startAt = previous.schedule?.start_at;

    switch (type) {
    case 'message_posted':
        return {message_posted: {channel_id: channelId}};
    case 'membership_changed':
        return {membership_changed: {channel_id: channelId, action: 'joined'}};
    case 'channel_created':
        return {channel_created: {team_id: teamId}};
    case 'user_joined_team':
        return {user_joined_team: {team_id: teamId}};
    case 'schedule':
    default:
        return {
            schedule: {
                channel_id: channelId,
                interval,
                ...(startAt ? {start_at: startAt} : {}),
            },
        };
    }
}

function withTriggerChannelID(trigger: Trigger, channelId: string): Trigger {
    if (trigger.message_posted) {
        return {message_posted: {...trigger.message_posted, channel_id: channelId}};
    }
    if (trigger.membership_changed) {
        return {membership_changed: {...trigger.membership_changed, channel_id: channelId}};
    }
    if (trigger.schedule) {
        return {schedule: {...trigger.schedule, channel_id: channelId}};
    }
    return trigger;
}

function withScheduleInterval(trigger: Trigger, interval: string): Trigger {
    if (!trigger.schedule) {
        return trigger;
    }
    return {schedule: {...trigger.schedule, interval}};
}

function withAgentId(automation: Automation, agentId: string): Automation {
    const next = ensureAIPromptAction(automation);
    return {
        ...next,
        actions: next.actions.map((action) => (
            action.ai_prompt ? {
                ...action,
                ai_prompt: {
                    ...action.ai_prompt,
                    provider_type: AIProviderTypeAgent,
                    provider_id: agentId,
                },
            } : action
        )),
    };
}

function withPrompt(automation: Automation, prompt: string): Automation {
    const next = ensureAIPromptAction(automation);
    return {
        ...next,
        actions: next.actions.map((action) => (
            action.ai_prompt ? {
                ...action,
                ai_prompt: {
                    ...action.ai_prompt,
                    prompt,
                },
            } : action
        )),
    };
}

function resolveInitialAgent(agents: UserAgent[], agentId: string): UserAgent | null {
    if (agents.length === 0) {
        return null;
    }
    if (agentId) {
        const byId = agents.find((a) => a.id === agentId);
        if (byId) {
            return byId;
        }
    }
    return agents[0];
}

const AutomationConfigView = ({automation, onBack, onSaved}: Props) => {
    const intl = useIntl();
    const [activeTab, setActiveTab] = useState<Tab>('settings');
    const initialAutomation = useMemo(() => ensureAIPromptAction(cloneAutomation(automation)), [automation]);
    const [draft, setDraft] = useState<Automation>(initialAutomation);
    const [baseline, setBaseline] = useState<Automation>(initialAutomation);
    const [context, setContext] = useState(DEFAULT_CONTEXT);
    const [saving, setSaving] = useState(false);
    const [agents, setAgents] = useState<UserAgent[]>([]);
    const [agentsLoading, setAgentsLoading] = useState(true);

    useEffect(() => {
        let cancelled = false;
        const load = async () => {
            try {
                setAgentsLoading(true);
                const result = await getAgents();
                if (cancelled) {
                    return;
                }
                const loaded = result.agents || [];
                setAgents(loaded);
                const currentAgentId = getAIPromptAction(initialAutomation)?.provider_id || '';
                const selected = resolveInitialAgent(loaded, currentAgentId);
                if (selected && selected.id !== currentAgentId) {
                    const next = withAgentId(initialAutomation, selected.id);
                    setDraft(next);
                    setBaseline(next);
                }
            } catch {
                if (!cancelled) {
                    setAgents([]);
                }
            } finally {
                if (!cancelled) {
                    setAgentsLoading(false);
                }
            }
        };
        load();
        return () => {
            cancelled = true;
        };
    }, [initialAutomation]);

    const isDirty = useMemo(
        () => !automationsEqual(draft, baseline),
        [draft, baseline],
    );

    const triggerType = getTriggerType(draft.trigger) || 'schedule';
    const channelId = getTriggerChannelID(draft.trigger);
    const interval = draft.trigger.schedule?.interval || INTERVAL_DAILY;
    const agentId = getAIPromptAction(draft)?.provider_id || '';
    const prompt = getAIPromptAction(draft)?.prompt || '';

    const handleAgentChange = useCallback((agent: UserAgent) => {
        setDraft((prev) => withAgentId(prev, agent.id));
    }, []);

    const handleTriggerChange = useCallback((nextType: TriggerType) => {
        setDraft((prev) => ({
            ...prev,
            trigger: buildTrigger(nextType, prev.trigger),
        }));
    }, []);

    const handleChannelsChange = useCallback((channelIds: string[]) => {
        const nextChannelId = channelIds[0] || '';
        setDraft((prev) => ({
            ...prev,
            trigger: withTriggerChannelID(prev.trigger, nextChannelId),
        }));
    }, []);

    const handleIntervalChange = useCallback((option: BaseSelectOption | null) => {
        if (!option) {
            return;
        }
        setDraft((prev) => ({
            ...prev,
            trigger: withScheduleInterval(prev.trigger, option.value),
        }));
    }, []);

    const handleContextChange = useCallback((option: BaseSelectOption | null) => {
        if (option) {
            setContext(option.value);
        }
    }, []);

    const handlePromptChange = useCallback((e: React.ChangeEvent<HTMLTextAreaElement>) => {
        setDraft((prev) => withPrompt(prev, e.target.value));
    }, []);

    const handleTabChange = useCallback((id: string) => {
        setActiveTab(id as Tab);
    }, []);

    const handleSave = useCallback(() => {
        setSaving(true);
        setBaseline(draft);
        onSaved(toUpdate(draft));
        setSaving(false);
    }, [draft, onSaved]);

    const tabs: ConfigViewTab[] = useMemo(() => [
        {
            id: 'chat',
            label: <FormattedMessage defaultMessage='Chat'/>,
        },
        {
            id: 'settings',
            label: <FormattedMessage defaultMessage='Settings'/>,
        },
    ], []);

    const showScheduleConfig = triggerType === 'schedule';
    const showChannelConfig = triggerType === 'schedule' ||
        triggerType === 'message_posted' ||
        triggerType === 'membership_changed';

    const intervalOptions = useMemo((): BaseSelectOption[] => [
        {value: INTERVAL_HOURLY, label: intl.formatMessage({defaultMessage: 'Hourly'})},
        {value: INTERVAL_DAILY, label: intl.formatMessage({defaultMessage: 'Daily'})},
        {value: INTERVAL_WEEKLY, label: intl.formatMessage({defaultMessage: 'Weekly'})},
    ], [intl]);

    const selectedInterval = useMemo(
        () => intervalOptions.find((option) => option.value === interval) ?? intervalOptions[1],
        [intervalOptions, interval],
    );

    const contextOptions = useMemo((): BaseSelectOption[] => [
        {value: DEFAULT_CONTEXT, label: DEFAULT_CONTEXT},
        {
            value: 'Invocation channel only',
            label: intl.formatMessage({defaultMessage: 'Invocation channel only'}),
        },
    ], [intl]);

    const selectedContext = useMemo(
        () => contextOptions.find((option) => option.value === context) ?? contextOptions[0],
        [contextOptions, context],
    );

    return (
        <ConfigViewShell
            title={draft.name || intl.formatMessage({defaultMessage: 'Edit automation'})}
            backAriaLabel={intl.formatMessage({defaultMessage: 'Back to automations'})}
            tabs={tabs}
            activeTabId={activeTab}
            onTabChange={handleTabChange}
            onBack={onBack}
            onSave={handleSave}
            isDirty={isDirty}
            saving={saving}
            saveLabel={<FormattedMessage defaultMessage='Save changes'/>}
            discardTitleId={DISCARD_CHANGES_TITLE_ID}
        >
            {activeTab === 'chat' && (
                <ChatPlaceholder>
                    <FormattedMessage defaultMessage='Chat for this automation will appear here.'/>
                </ChatPlaceholder>
            )}
            {activeTab === 'settings' && (
                <SettingsForm>
                    <FormRow>
                        <FieldLabel>
                            <FormattedMessage defaultMessage='Agent'/>
                        </FieldLabel>
                        <FieldControl>
                            <AgentSelector
                                agents={agents}
                                value={agentId}
                                onChange={handleAgentChange}
                                loading={agentsLoading}
                            />
                        </FieldControl>
                    </FormRow>

                    <FormRow $alignTop={true}>
                        <FieldLabel>
                            <FormattedMessage defaultMessage='What starts the automation?'/>
                        </FieldLabel>
                        <FieldControl>
                            <TriggerSelector
                                value={triggerType}
                                onChange={handleTriggerChange}
                            />
                            {showScheduleConfig && (
                                <TriggerDetails>
                                    <SingleSelect
                                        value={selectedInterval}
                                        options={intervalOptions}
                                        onChange={handleIntervalChange}
                                        isSearchable={false}
                                        aria-label={intl.formatMessage({defaultMessage: 'Frequency'})}
                                    />
                                </TriggerDetails>
                            )}
                            {showChannelConfig && (
                                <ChannelSelectWrap>
                                    <SelectChannel
                                        channelIDs={channelId ? [channelId] : []}
                                        onChangeChannelIDs={handleChannelsChange}
                                    />
                                </ChannelSelectWrap>
                            )}
                        </FieldControl>
                    </FormRow>

                    <FormRow $alignTop={true}>
                        <FieldLabel>
                            <FormattedMessage defaultMessage='What should the automation do?'/>
                        </FieldLabel>
                        <FieldControl>
                            <InstructionsInput
                                value={prompt}
                                onChange={handlePromptChange}
                                rows={4}
                                aria-label={intl.formatMessage({defaultMessage: 'What should the automation do?'})}
                            />
                        </FieldControl>
                    </FormRow>

                    <FormRow>
                        <FieldLabel>
                            <FormattedMessage defaultMessage='Where the automation can read from'/>
                        </FieldLabel>
                        <FieldControl>
                            <SingleSelect
                                value={selectedContext}
                                options={contextOptions}
                                onChange={handleContextChange}
                                isSearchable={false}
                                aria-label={intl.formatMessage({defaultMessage: 'Where the automation can read from'})}
                            />
                        </FieldControl>
                    </FormRow>
                </SettingsForm>
            )}
        </ConfigViewShell>
    );
};

const ChatPlaceholder = styled.div`
    padding: 40px 0;
    text-align: center;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    font-size: 14px;
`;

const SettingsForm = styled.div`
    display: flex;
    flex-direction: column;
    gap: 24px;
    max-width: 800px;
`;

const FormRow = styled.div<{$alignTop?: boolean}>`
    display: grid;
    grid-template-columns: minmax(180px, 240px) minmax(0, 1fr);
    gap: 24px;
    align-items: ${(p) => (p.$alignTop ? 'flex-start' : 'center')};
`;

const FieldLabel = styled.div`
    font-family: 'Open Sans', sans-serif;
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    color: var(--center-channel-color);
    padding-top: 10px;
`;

const FieldControl = styled.div`
    display: flex;
    flex-direction: column;
    gap: 8px;
    min-width: 0;
`;

const TriggerDetails = styled.div`
    display: grid;
    grid-template-columns: 1fr;
    gap: 8px;
`;

const ChannelSelectWrap = styled.div`
    width: 100%;
`;

const InstructionsInput = styled.textarea`
    width: 100%;
    min-height: 96px;
    padding: 10px 12px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    background: var(--center-channel-bg);
    color: var(--center-channel-color);
    font-family: 'Open Sans', sans-serif;
    font-size: 14px;
    line-height: 20px;
    resize: vertical;

    &:focus {
        outline: none;
        border-color: var(--button-bg);
        box-shadow: inset 0 0 0 1px var(--button-bg);
    }
`;

export default AutomationConfigView;
