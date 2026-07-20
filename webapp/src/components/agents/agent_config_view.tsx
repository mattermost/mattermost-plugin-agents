// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useMemo, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import {createAgent, updateAgent, uploadAgentAvatar} from '@/client';
import {UserAgent, CreateAgentRequest, UpdateAgentRequest, EnabledTool, ServiceInfo} from '@/types/agents';
import {ChannelAccessLevel, UserAccessLevel} from '@/components/system_console/bot';

import ConfigViewShell, {ConfigViewTab} from './config_view_shell';
import ConfigTab from './tabs/config_tab';
import AccessTab from './tabs/access_tab';
import McpsTab from './tabs/mcps_tab';

type Tab = 'config' | 'access' | 'mcps';

type Mode = 'create' | 'edit';

// AgentDraft holds the mutable form state. All fields correspond to UserAgent/CreateAgentRequest.
export type AgentDraft = {
    displayName: string;
    username: string;
    serviceId: string;
    customInstructions: string;
    channelAccessLevel: ChannelAccessLevel;
    channelIds: string[];
    userAccessLevel: UserAccessLevel;
    userIds: string[];
    teamIds: string[];
    adminUserIds: string[];
    enabledTools: EnabledTool[];
    autoEnableNewMCPTools: boolean;
    mcpDynamicToolLoading: boolean;
    model: string;
    enableVision: boolean;
    disableTools: boolean;
    enabledNativeTools: string[];
    reasoningEnabled: boolean;
    reasoningEffort: string;
    thinkingBudget: number;
    structuredOutputEnabled: boolean;
    maxToolTurns: number;
}

// DefaultMaxToolTurns mirrors llm.DefaultMaxToolTurns on the backend. Kept
// here so the create form pre-populates the field even before any service is
// selected.
export const DefaultMaxToolTurns = 30;
export const MaxAllowedMaxToolTurns = 250;

const emptyDraft: AgentDraft = {
    displayName: '',
    username: '',
    serviceId: '',
    customInstructions: '',
    channelAccessLevel: ChannelAccessLevel.All,
    channelIds: [],
    userAccessLevel: UserAccessLevel.All,
    userIds: [],
    teamIds: [],
    adminUserIds: [],
    enabledTools: [],
    autoEnableNewMCPTools: true,
    mcpDynamicToolLoading: true,
    model: '',
    enableVision: true,
    disableTools: false,
    enabledNativeTools: ['web_search'],
    reasoningEnabled: true,
    reasoningEffort: 'medium',
    thinkingBudget: 0,
    structuredOutputEnabled: false,
    maxToolTurns: DefaultMaxToolTurns,
};

function cloneDraft(draft: AgentDraft): AgentDraft {
    return {
        ...draft,
        channelIds: [...draft.channelIds],
        userIds: [...draft.userIds],
        teamIds: [...draft.teamIds],
        adminUserIds: [...draft.adminUserIds],
        enabledTools: [...draft.enabledTools],
        enabledNativeTools: [...draft.enabledNativeTools],
    };
}

function draftsEqual(a: AgentDraft, b: AgentDraft): boolean {
    return JSON.stringify(a) === JSON.stringify(b);
}

/**
 * Full-document create payload from the form draft. The backend uses the UI as the sole
 * source of truth for create-time defaults, so every field is sent explicitly.
 */
function draftToCreateAgentPayload(draft: AgentDraft): CreateAgentRequest {
    return {
        displayName: draft.displayName,
        username: draft.username,
        serviceID: draft.serviceId,
        customInstructions: draft.customInstructions,
        channelAccessLevel: draft.channelAccessLevel,
        channelIDs: draft.channelIds,
        userAccessLevel: draft.userAccessLevel,
        userIDs: draft.userIds,
        teamIDs: draft.teamIds,
        adminUserIDs: draft.adminUserIds,
        enabledMCPTools: draft.enabledTools,
        autoEnableNewMCPTools: draft.autoEnableNewMCPTools,
        mcpDynamicToolLoading: draft.mcpDynamicToolLoading,
        model: draft.model,
        enableVision: draft.enableVision,
        disableTools: draft.disableTools,
        enabledNativeTools: draft.enabledNativeTools,
        reasoningEnabled: draft.reasoningEnabled,
        reasoningEffort: draft.reasoningEffort,
        thinkingBudget: draft.thinkingBudget,
        structuredOutputEnabled: draft.structuredOutputEnabled,
        maxToolTurns: draft.maxToolTurns,
    };
}

/**
 * Full-document update payload from the form draft. PUT /agents/:id is a full-object
 * replacement, so every mutable field is sent on every save.
 */
function draftToUpdateAgentPayload(draft: AgentDraft): UpdateAgentRequest {
    return {
        displayName: draft.displayName,
        username: draft.username,
        serviceID: draft.serviceId,
        customInstructions: draft.customInstructions,
        channelAccessLevel: draft.channelAccessLevel,
        channelIDs: draft.channelIds,
        userAccessLevel: draft.userAccessLevel,
        userIDs: draft.userIds,
        teamIDs: draft.teamIds,
        adminUserIDs: draft.adminUserIds,
        enabledMCPTools: draft.enabledTools,
        autoEnableNewMCPTools: draft.autoEnableNewMCPTools,
        mcpDynamicToolLoading: draft.mcpDynamicToolLoading,
        model: draft.model,
        enableVision: draft.enableVision,
        disableTools: draft.disableTools,
        enabledNativeTools: draft.enabledNativeTools,
        reasoningEnabled: draft.reasoningEnabled,
        reasoningEffort: draft.reasoningEffort,
        thinkingBudget: draft.thinkingBudget,
        structuredOutputEnabled: draft.structuredOutputEnabled,
        maxToolTurns: draft.maxToolTurns,
    };
}

function agentToDraft(agent: UserAgent): AgentDraft {
    return {
        displayName: agent.displayName,
        username: agent.name,
        serviceId: agent.serviceID,
        customInstructions: agent.customInstructions,
        channelAccessLevel: agent.channelAccessLevel,
        channelIds: agent.channelIDs ?? [],
        userAccessLevel: agent.userAccessLevel,
        userIds: agent.userIDs ?? [],
        teamIds: agent.teamIDs ?? [],
        adminUserIds: agent.adminUserIDs ?? [],
        enabledTools: agent.enabledMCPTools ?? [],
        autoEnableNewMCPTools: agent.autoEnableNewMCPTools ?? false,
        mcpDynamicToolLoading: agent.mcpDynamicToolLoading ?? true,
        model: agent.model ?? '',
        enableVision: agent.enableVision ?? true,
        disableTools: agent.disableTools ?? false,
        enabledNativeTools: agent.enabledNativeTools ?? [],
        reasoningEnabled: agent.reasoningEnabled ?? true,
        reasoningEffort: agent.reasoningEffort || 'medium',
        thinkingBudget: agent.thinkingBudget ?? 0,
        structuredOutputEnabled: agent.structuredOutputEnabled ?? false,
        maxToolTurns: agent.maxToolTurns && agent.maxToolTurns > 0 ? agent.maxToolTurns : DefaultMaxToolTurns,
    };
}

type Props = {
    mode: Mode;
    agent?: UserAgent; // provided when mode === 'edit'
    services: ServiceInfo[]; // pre-fetched from parent
    onBack: () => void;
    onSaved: (agent: UserAgent) => void; // called after successful create or update
}

const DISCARD_CHANGES_TITLE_ID = 'discard-agent-changes-title';

const AgentConfigView = (props: Props) => {
    const {mode, agent, services, onBack, onSaved} = props;
    const intl = useIntl();

    const [activeTab, setActiveTab] = useState<Tab>('config');
    const initialDraft = useMemo(() => (agent ? agentToDraft(agent) : cloneDraft(emptyDraft)), [agent]);
    const [draft, setDraft] = useState<AgentDraft>(initialDraft);
    const [baselineDraft, setBaselineDraft] = useState<AgentDraft>(initialDraft);
    const [avatarFile, setAvatarFile] = useState<File | null>(null);
    const [saving, setSaving] = useState(false);
    const [errors, setErrors] = useState<Record<string, string>>({});

    // Leave MCPs tab if tools are disabled
    useEffect(() => {
        if (draft.disableTools && activeTab === 'mcps') {
            setActiveTab('config');
        }
    }, [draft.disableTools, activeTab]);

    const isDirty = useMemo(
        () => avatarFile !== null || !draftsEqual(draft, baselineDraft),
        [draft, baselineDraft, avatarFile],
    );

    const updateDraft = useCallback((updates: Partial<AgentDraft>) => {
        setDraft((prev) => ({...prev, ...updates}));
        setErrors((prev) => {
            const next = {...prev};
            for (const key of Object.keys(updates)) {
                delete next[key];
            }
            delete next.general;
            return next;
        });
    }, []);

    // Server-state reconciliation: applied to both the editable draft and the
    // baseline used for dirty detection. Used when a child tab (e.g. MCPs) drops
    // entries that no longer exist server-side. This must not mark the form as
    // dirty — the user didn't change anything (MM-69185).
    const reconcileEnabledTools = useCallback((cleaned: EnabledTool[]) => {
        const next = [...cleaned];
        setDraft((prev) => ({...prev, enabledTools: next}));
        setBaselineDraft((prev) => ({...prev, enabledTools: [...next]}));
    }, []);

    const validate = useCallback((): Record<string, string> => {
        const errs: Record<string, string> = {};
        if (!draft.displayName.trim()) {
            errs.displayName = intl.formatMessage({defaultMessage: 'Display name is required'});
        }
        if (!draft.username.trim()) {
            errs.username = intl.formatMessage({defaultMessage: 'Username is required'});
        } else if (!(/^[a-z][a-z0-9.\-_]*$/).test(draft.username)) {
            errs.username = intl.formatMessage({defaultMessage: 'Username must start with a letter and contain only lowercase letters, numbers, periods, hyphens, and underscores'});
        }
        if (!draft.serviceId) {
            errs.serviceId = intl.formatMessage({defaultMessage: 'AI Service is required'});
        }
        if (draft.maxToolTurns < 1 || draft.maxToolTurns > MaxAllowedMaxToolTurns) {
            errs.maxToolTurns = intl.formatMessage(
                {defaultMessage: 'Max tool turns must be between 1 and {max}'},
                {max: MaxAllowedMaxToolTurns},
            );
        }
        return errs;
    }, [draft, intl]);

    const handleSave = useCallback(async () => {
        const validationErrors = validate();
        if (Object.keys(validationErrors).length > 0) {
            setErrors(validationErrors);
            setActiveTab('config');
            return;
        }
        setErrors({});
        setSaving(true);

        try {
            let savedAgent: UserAgent;
            if (mode === 'create') {
                savedAgent = await createAgent(draftToCreateAgentPayload(draft));
            } else {
                savedAgent = await updateAgent(agent!.id, draftToUpdateAgentPayload(draft));
            }

            // Upload avatar if one was selected (two-step: create/update first, then avatar)
            if (avatarFile && savedAgent.id) {
                try {
                    await uploadAgentAvatar(savedAgent.id, avatarFile);
                } catch {
                    // Avatar upload failure is non-fatal — agent was still saved
                }
            }

            // Clear dirty state so onSaved -> onBack flow doesn't trigger discard prompt
            setBaselineDraft(cloneDraft(draft));
            setAvatarFile(null);
            onSaved(savedAgent);
        } catch (e: any) {
            const message = (typeof e?.message === 'string' ? e.message : '').trim();
            if (e?.status_code === 409 || (message.includes('username') && (message.includes('taken') || message.includes('conflict')))) {
                setErrors({username: intl.formatMessage({defaultMessage: 'This username is already taken'})});
                setActiveTab('config');
            } else if (e?.status_code === 403 && !message) {
                setErrors({general: intl.formatMessage({defaultMessage: 'You do not have permission to perform this action.'})});
            } else if (message) {
                // Prefer the server-provided message so validation errors
                // (e.g. oversized custom instructions) surface verbatim
                // instead of a misleading "please try again" hint.
                setErrors({general: message});
            } else {
                setErrors({general: intl.formatMessage({defaultMessage: 'Failed to save agent. Please try again.'})});
            }
        } finally {
            setSaving(false);
        }
    }, [mode, agent, draft, avatarFile, intl, onSaved, validate]);

    const handleTabChange = useCallback((id: string) => {
        setActiveTab(id as Tab);
    }, []);

    const title = mode === 'create' ? intl.formatMessage({defaultMessage: 'New Agent'}) : draft.displayName || intl.formatMessage({defaultMessage: 'Edit Agent'});

    const tabs: ConfigViewTab[] = useMemo(() => [
        {
            id: 'config',
            label: <FormattedMessage defaultMessage='Configuration'/>,
        },
        {
            id: 'access',
            label: <FormattedMessage defaultMessage='Access'/>,
        },
        {
            id: 'mcps',
            label: <FormattedMessage defaultMessage='MCPs'/>,
            disabled: draft.disableTools,
            ...(draft.disableTools ? {
                title: intl.formatMessage({defaultMessage: 'Enable Tools to configure MCP integrations'}),
            } : {}),
        },
    ], [draft.disableTools, intl]);

    return (
        <ConfigViewShell
            title={title}
            backAriaLabel={intl.formatMessage({defaultMessage: 'Back to agents'})}
            tabs={tabs}
            activeTabId={activeTab}
            onTabChange={handleTabChange}
            onBack={onBack}
            onSave={handleSave}
            isDirty={isDirty}
            saving={saving}
            discardTitleId={DISCARD_CHANGES_TITLE_ID}
            error={errors.general}
        >
            {activeTab === 'config' && (
                <ConfigTab
                    draft={draft}
                    onChange={updateDraft}
                    onAvatarChange={setAvatarFile}
                    botUserId={agent?.botUserID}
                    services={services}
                    errors={errors}
                    usernameLocked={mode === 'edit'}
                />
            )}
            {activeTab === 'access' && (
                <AccessTab
                    draft={draft}
                    onChange={updateDraft}
                />
            )}
            {activeTab === 'mcps' && (
                <McpsTab
                    enabledTools={draft.enabledTools}
                    autoEnableNewMCPTools={draft.autoEnableNewMCPTools}
                    mcpDynamicToolLoading={draft.mcpDynamicToolLoading}
                    onChange={(updates) => updateDraft(updates)}
                    onReconcileEnabledTools={reconcileEnabledTools}
                />
            )}
        </ConfigViewShell>
    );
};

export default AgentConfigView;
