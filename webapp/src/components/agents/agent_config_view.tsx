// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {ArrowLeftIcon} from '@mattermost/compass-icons/components';

import {Button} from '@mattermost/compass-ui/components/button';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';

import {createAgent, updateAgent, uploadAgentAvatar} from '@/client';
import {
    UserAgent,
    CreateAgentRequest,
    UpdateAgentRequest,
    EnabledTool,
    MaxCustomInstructionsRunes,
    ServiceInfo,
    DefaultMaxToolTurns,
    MaxAllowedMaxToolTurns,
    codePointLength,
} from '@/types/agents';
import {ChannelAccessLevel, UserAccessLevel} from '@/components/system_console/bot';
import ConfirmationDialog from '@/components/confirmation_dialog';
import {UnderlineTab, UnderlineTabs} from '@/components/underline_tabs';
import {useIsLicensedFor} from '@/license';
import {useABACSupport} from '@/utils/access_control';
import {useCurrentUserHasSystemPermission} from '@/utils/permissions';

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
    useServiceAccountAuth: boolean;
    model: string;
    enableVision: boolean;
    disableTools: boolean;
    enabledNativeTools: string[];
    reasoningEnabled: boolean;
    reasoningEffort: string;
    thinkingBudget: number;
    maxToolTurns: number;
}

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
    useServiceAccountAuth: false,
    model: '',
    enableVision: true,
    disableTools: false,
    enabledNativeTools: ['web_search'],
    reasoningEnabled: true,
    reasoningEffort: 'medium',
    thinkingBudget: 0,
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
        useServiceAccountAuth: draft.useServiceAccountAuth,
        model: draft.model,
        enableVision: draft.enableVision,
        disableTools: draft.disableTools,
        enabledNativeTools: draft.enabledNativeTools,
        reasoningEnabled: draft.reasoningEnabled,
        reasoningEffort: draft.reasoningEffort,
        thinkingBudget: draft.thinkingBudget,
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
        useServiceAccountAuth: draft.useServiceAccountAuth,
        model: draft.model,
        enableVision: draft.enableVision,
        disableTools: draft.disableTools,
        enabledNativeTools: draft.enabledNativeTools,
        reasoningEnabled: draft.reasoningEnabled,
        reasoningEffort: draft.reasoningEffort,
        thinkingBudget: draft.thinkingBudget,
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
        useServiceAccountAuth: agent.useServiceAccountAuth ?? false,
        model: agent.model ?? '',
        enableVision: agent.enableVision ?? true,
        disableTools: agent.disableTools ?? false,
        enabledNativeTools: agent.enabledNativeTools ?? [],
        reasoningEnabled: agent.reasoningEnabled ?? true,
        reasoningEffort: agent.reasoningEffort || 'medium',
        thinkingBudget: agent.thinkingBudget ?? 0,
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

    // Parent owns the manage_system check via useCurrentUserHasSystemPermission.
    const canEditServiceAccountAuth = useCurrentUserHasSystemPermission('manage_system');
    const {supported: abacSupported} = useABACSupport();
    const providerWebSearchLicensed = useIsLicensedFor('provider_web_search');

    const [activeTab, setActiveTab] = useState<Tab>('config');
    const initialDraft = useMemo(() => {
        if (agent) {
            return agentToDraft(agent);
        }
        const draft = cloneDraft(emptyDraft);
        if (services.length > 0) {
            draft.serviceId = services[0].id;
        }

        // Provider-native web search is on by default where it is available.
        if (!providerWebSearchLicensed) {
            draft.enabledNativeTools = draft.enabledNativeTools.filter((tool) => tool !== 'web_search');
        }
        return draft;
    }, [agent, services, providerWebSearchLicensed]);
    const [draft, setDraft] = useState<AgentDraft>(initialDraft);
    const [baselineDraft, setBaselineDraft] = useState<AgentDraft>(initialDraft);
    const [avatarFile, setAvatarFile] = useState<File | null>(null);
    const [saving, setSaving] = useState(false);
    const [errors, setErrors] = useState<Record<string, string>>({});
    const [showDiscardDialog, setShowDiscardDialog] = useState(false);
    const showDiscardDialogRef = useRef(false);
    showDiscardDialogRef.current = showDiscardDialog;

    // Soft-lock Access / MCP grants while SA stays on for non-admins.
    // Save stays enabled so managers can still edit day-to-day config (including
    // AI service, tools, and dynamic tool loading).
    const serviceAccountFieldsLocked = !canEditServiceAccountAuth && draft.useServiceAccountAuth;

    // The MCPs tab stays reachable while fields-locked so the off switch is available.
    const mcpsTabDisabled = draft.disableTools && !serviceAccountFieldsLocked;

    // Leave MCPs tab if tools are disabled
    useEffect(() => {
        if (mcpsTabDisabled && activeTab === 'mcps') {
            setActiveTab('config');
        }
    }, [mcpsTabDisabled, activeTab]);

    const isDirty = useMemo(
        () => avatarFile !== null || !draftsEqual(draft, baselineDraft),
        [draft, baselineDraft, avatarFile],
    );

    const requestBack = useCallback(() => {
        if (saving) {
            return;
        }
        if (showDiscardDialogRef.current) {
            return;
        }

        if (isDirty) {
            setShowDiscardDialog(true);
            return;
        }
        onBack();
    }, [isDirty, onBack, saving]);

    const handleDiscardConfirm = useCallback(() => {
        setShowDiscardDialog(false);
        onBack();
    }, [onBack]);

    const handleDiscardCancel = useCallback(() => {
        setShowDiscardDialog(false);
    }, []);

    // Escape key: same as back — confirm when there are unsaved changes.
    // Skip Escapes a child already handled (e.g. closing a picker menu or a nested dialog).
    useEffect(() => {
        const handler = (e: KeyboardEvent) => {
            if (e.key !== 'Escape' || e.defaultPrevented) {
                return;
            }
            if (showDiscardDialogRef.current) {
                return;
            }
            e.preventDefault();
            requestBack();
        };
        document.addEventListener('keydown', handler);
        return () => document.removeEventListener('keydown', handler);
    }, [requestBack]);

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
        if (codePointLength(draft.customInstructions) > MaxCustomInstructionsRunes) {
            errs.customInstructions = intl.formatMessage(
                {defaultMessage: 'Custom instructions must be {max} characters or fewer'},
                {max: intl.formatNumber(MaxCustomInstructionsRunes)},
            );
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

    const title = mode === 'create' ? intl.formatMessage({defaultMessage: 'New Agent'}) : draft.displayName || intl.formatMessage({defaultMessage: 'Edit Agent'});

    return (
        <>
            <ViewContainer>
                <ViewHeader>
                    <HeaderLeading>
                        <BackButton
                            icon={<Icon glyph={<ArrowLeftIcon/>}/>}
                            size='medium'
                            onClick={requestBack}
                            disabled={saving}
                            aria-label={intl.formatMessage({defaultMessage: 'Back to agents'})}
                        />
                        <ViewTitle>{title}</ViewTitle>
                    </HeaderLeading>
                </ViewHeader>

                <ConfigTabs role='tablist'>
                    <UnderlineTab
                        type='button'
                        role='tab'
                        id='agent-config-tab'
                        aria-controls='agent-config-panel'
                        aria-selected={activeTab === 'config'}
                        tabIndex={activeTab === 'config' ? 0 : -1}
                        $active={activeTab === 'config'}
                        onClick={() => setActiveTab('config')}
                    >
                        <FormattedMessage defaultMessage='Configuration'/>
                    </UnderlineTab>
                    <UnderlineTab
                        type='button'
                        role='tab'
                        id='agent-access-tab'
                        aria-controls='agent-access-panel'
                        aria-selected={activeTab === 'access'}
                        tabIndex={activeTab === 'access' ? 0 : -1}
                        $active={activeTab === 'access'}
                        onClick={() => setActiveTab('access')}
                    >
                        <FormattedMessage defaultMessage='Access'/>
                    </UnderlineTab>
                    <UnderlineTab
                        type='button'
                        role='tab'
                        id='agent-mcps-tab'
                        aria-controls='agent-mcps-panel'
                        aria-selected={activeTab === 'mcps'}
                        tabIndex={activeTab === 'mcps' ? 0 : -1}
                        $active={activeTab === 'mcps'}
                        disabled={mcpsTabDisabled}
                        title={mcpsTabDisabled ? intl.formatMessage({defaultMessage: 'Enable Tools to configure MCP integrations'}) : undefined}
                        onClick={() => setActiveTab('mcps')}
                    >
                        <FormattedMessage defaultMessage='MCPs'/>
                    </UnderlineTab>
                </ConfigTabs>

                <ViewBody>
                    {errors.general && (
                        <Notice
                            type='danger'
                            title={errors.general}
                        />
                    )}
                    {serviceAccountFieldsLocked && (
                        <div role='status'>
                            <Notice
                                type='warning'
                                title={<FormattedMessage defaultMessage='This agent uses service account authentication. Access and MCP tool grants require a system administrator while that setting is enabled. Other settings can still be saved, or turn the setting off on the MCPs tab.'/>}
                            />
                        </div>
                    )}

                    {activeTab === 'config' && (
                        <div
                            role='tabpanel'
                            id='agent-config-panel'
                            aria-labelledby='agent-config-tab'
                        >
                            <ConfigTab
                                draft={draft}
                                onChange={updateDraft}
                                onAvatarChange={setAvatarFile}
                                botUserId={agent?.botUserID}
                                services={services}
                                errors={errors}
                                usernameLocked={mode === 'edit'}
                            />
                        </div>
                    )}
                    {activeTab === 'access' && (
                        <div
                            role='tabpanel'
                            id='agent-access-panel'
                            aria-labelledby='agent-access-tab'
                        >
                            <AccessTab
                                draft={draft}
                                baselineUserAccessLevel={baselineDraft.userAccessLevel}
                                onChange={updateDraft}
                                serviceAccountFieldsLocked={serviceAccountFieldsLocked}
                                agentId={agent?.id}
                                abacSupported={abacSupported}
                                isSystemAdmin={canEditServiceAccountAuth}
                            />
                        </div>
                    )}
                    {activeTab === 'mcps' && (
                        <div
                            role='tabpanel'
                            id='agent-mcps-panel'
                            aria-labelledby='agent-mcps-tab'
                        >
                            <McpsTab
                                agentId={agent?.id}
                                enabledTools={draft.enabledTools}
                                autoEnableNewMCPTools={draft.autoEnableNewMCPTools}
                                useServiceAccountAuth={draft.useServiceAccountAuth}
                                serviceAccountFieldsLocked={serviceAccountFieldsLocked}
                                canEditServiceAccountAuth={canEditServiceAccountAuth}
                                onChange={(updates) => updateDraft(updates)}
                                onReconcileEnabledTools={reconcileEnabledTools}
                            />
                        </div>
                    )}
                </ViewBody>

                <ViewFooter>
                    <Button
                        emphasis='tertiary'
                        onClick={requestBack}
                        disabled={saving}
                    >
                        <FormattedMessage defaultMessage='Cancel'/>
                    </Button>
                    <Button
                        emphasis='primary'
                        onClick={handleSave}
                        loading={saving}
                    >
                        {saving ? <FormattedMessage defaultMessage='Saving...'/> : <FormattedMessage defaultMessage='Save'/>
                        }
                    </Button>
                </ViewFooter>
            </ViewContainer>
            <ConfirmationDialog
                show={showDiscardDialog}
                titleId={DISCARD_CHANGES_TITLE_ID}
                title={<FormattedMessage defaultMessage='Discard changes?'/>}
                message={(
                    <FormattedMessage defaultMessage='You have unsaved changes. If you close now, those changes will be lost.'/>
                )}
                confirmButtonText={<FormattedMessage defaultMessage='Discard'/>}
                cancelButtonText={<FormattedMessage defaultMessage='Keep editing'/>}
                onConfirm={handleDiscardConfirm}
                onCancel={handleDiscardCancel}
                isDestructive={true}
                managedAccessibility={true}
                zIndex={2100}
            />
        </>
    );
};

// --- Styled Components ---

const ViewContainer = styled.div`
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
    width: 100%;
`;

const ViewHeader = styled.div`
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: var(--spacing-xxxxxl) 0 var(--spacing-l) 0;
    flex-shrink: 0;
`;

const ConfigTabs = styled(UnderlineTabs)`
    padding: 0 var(--spacing-l);
`;

const HeaderLeading = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-xs);
    min-width: 0;
`;

const ViewTitle = styled.h1`
    font-family: var(--font-family-heading, 'Metropolis', sans-serif);
    font-weight: var(--font-weight-semibold);
    font-size: var(--font-size-500);
    line-height: var(--line-height-500);
    color: var(--center-channel-color);
    margin: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
`;

const BackButton = styled(IconButton)`
    margin-left: calc(-1 * var(--spacing-xs));
`;

const ViewBody = styled.div`
    padding: var(--spacing-xxxl) var(--spacing-l);
    flex: 1;
    min-height: 0;
    overflow-y: auto;
`;

const Notice = styled(SectionNotice)`
    margin-bottom: var(--spacing-l);
`;

const ViewFooter = styled.div`
    display: flex;
    justify-content: flex-end;
    align-items: center;
    padding: var(--spacing-l) 0;
    gap: var(--spacing-xs);
    flex-shrink: 0;
    background: var(--center-channel-bg);
    border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;

export default AgentConfigView;
