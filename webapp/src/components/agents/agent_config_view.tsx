// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {ArrowLeftIcon} from '@mattermost/compass-icons/components';

import {createAgent, updateAgent, uploadAgentAvatar} from '@/client';
import {UserAgent, CreateAgentRequest, UpdateAgentRequest, EnabledTool, ServiceInfo} from '@/types/agents';
import {ChannelAccessLevel, UserAccessLevel} from '@/components/system_console/bot';
import {PrimaryButton, TertiaryButton} from '@/components/assets/buttons';
import ConfirmationDialog from '@/components/confirmation_dialog';

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
    model: '',
    enableVision: true,
    disableTools: false,
    enabledNativeTools: ['web_search'],
    reasoningEnabled: true,
    reasoningEffort: 'medium',
    thinkingBudget: 0,
    structuredOutputEnabled: false,
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
    const [showDiscardDialog, setShowDiscardDialog] = useState(false);
    const showDiscardDialogRef = useRef(false);
    showDiscardDialogRef.current = showDiscardDialog;

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

    // Escape key: same as back — confirm when there are unsaved changes
    useEffect(() => {
        const handler = (e: KeyboardEvent) => {
            if (e.key !== 'Escape') {
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
                            type='button'
                            onClick={requestBack}
                            disabled={saving}
                            aria-label={intl.formatMessage({defaultMessage: 'Back to agents'})}
                        >
                            <ArrowLeftIcon size={20}/>
                        </BackButton>
                        <ViewTitle>{title}</ViewTitle>
                    </HeaderLeading>
                </ViewHeader>

                <TabsContainer>
                    <TabButton
                        $active={activeTab === 'config'}
                        onClick={() => setActiveTab('config')}
                    >
                        <FormattedMessage defaultMessage='Configuration'/>
                    </TabButton>
                    <TabButton
                        $active={activeTab === 'access'}
                        onClick={() => setActiveTab('access')}
                    >
                        <FormattedMessage defaultMessage='Access'/>
                    </TabButton>
                    <TabButton
                        $active={activeTab === 'mcps'}
                        disabled={draft.disableTools}
                        title={draft.disableTools ? intl.formatMessage({defaultMessage: 'Enable Tools to configure MCP integrations'}) : ''}
                        onClick={() => {
                            if (!draft.disableTools) {
                                setActiveTab('mcps');
                            }
                        }}
                    >
                        <FormattedMessage defaultMessage='MCPs'/>
                    </TabButton>
                </TabsContainer>

                <ViewBody>
                    {errors.general && <ErrorBanner>{errors.general}</ErrorBanner>}

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
                        />
                    )}
                </ViewBody>

                <ViewFooter>
                    <CancelButton
                        type='button'
                        onClick={requestBack}
                        disabled={saving}
                    >
                        <FormattedMessage defaultMessage='Cancel'/>
                    </CancelButton>
                    <SaveButton
                        onClick={handleSave}
                        disabled={saving}
                    >
                        {saving ? <FormattedMessage defaultMessage='Saving...'/> : <FormattedMessage defaultMessage='Save'/>
                        }
                    </SaveButton>
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
    padding: 48px 0 16px 0;
    flex-shrink: 0;
`;

const HeaderLeading = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
`;

const ViewTitle = styled.h1`
    font-family: 'Metropolis', sans-serif;
    font-weight: 600;
    font-size: 22px;
    line-height: 28px;
    color: var(--center-channel-color);
    margin: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
`;

const BackButton = styled.button`
    background: none;
    border: none;
    cursor: pointer;
    padding: 8px;
    margin-left: -8px;
    border-radius: 4px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    display: flex;
    align-items: center;
    justify-content: center;

    &:hover:not(:disabled) {
        background: rgba(var(--center-channel-color-rgb), 0.08);
        color: var(--center-channel-color);
    }

    &:disabled {
        cursor: not-allowed;
        opacity: 0.4;
    }
`;

const TabsContainer = styled.div`
    display: flex;
    box-sizing: border-box;
    width: 100%;
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    flex-shrink: 0;
`;

const TabButton = styled.button<{$active: boolean}>`
    padding: 12px 16px;
    border: none;
    background: none;
    cursor: pointer;
    font-size: 14px;
    font-weight: 600;
    color: ${(p) => (p.$active ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.64)')};
    border-bottom: 2px solid ${(p) => (p.$active ? 'var(--button-bg)' : 'transparent')};
    transition: color 0.2s ease, border-color 0.2s ease;
    margin-bottom: -1px;

    &:hover:not(:disabled) {
        color: ${(p) => (p.$active ? 'var(--button-bg)' : 'var(--center-channel-color)')};
    }

    &:disabled {
        opacity: 0.4;
        cursor: not-allowed;
    }
`;

const ViewBody = styled.div`
    padding: 32px 16px;
    flex: 1;
    min-height: 0;
    overflow-y: auto;
`;

const ErrorBanner = styled.div`
    padding: 10px 12px;
    margin-bottom: 16px;
    background: rgba(var(--dnd-indicator-rgb, 210, 75, 78), 0.08);
    border-radius: 4px;
    border: 1px solid rgba(var(--dnd-indicator-rgb, 210, 75, 78), 0.3);
    color: var(--dnd-indicator, #D24B4E);
    font-size: 14px;
`;

const ViewFooter = styled.div`
    display: flex;
    justify-content: flex-end;
    align-items: center;
    padding: 16px 0;
    gap: 8px;
    flex-shrink: 0;
    background: var(--center-channel-bg);
    border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;

const CancelButton = styled(TertiaryButton)`
    height: 40px;
`;

const SaveButton = styled(PrimaryButton)`
    height: 40px;
`;

export default AgentConfigView;
