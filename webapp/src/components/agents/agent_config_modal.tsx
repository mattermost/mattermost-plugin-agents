// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {CloseIcon} from '@mattermost/compass-icons/components';

import {createAgent, updateAgent, uploadAgentAvatar} from '@/client';
import {UserAgent, CreateAgentRequest, UpdateAgentRequest, EnabledTool, ServiceInfo} from '@/types/agents';
import {ChannelAccessLevel, UserAccessLevel} from '@/components/system_console/bot';
import {PrimaryButton, TertiaryButton} from '@/components/assets/buttons';

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
    model: '',
    enableVision: true,
    disableTools: false,
    enabledNativeTools: ['web_search'],
    reasoningEnabled: true,
    reasoningEffort: 'medium',
    thinkingBudget: 0,
    structuredOutputEnabled: false,
};

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
    show: boolean;
    mode: Mode;
    agent?: UserAgent; // provided when mode === 'edit'
    services: ServiceInfo[]; // pre-fetched from parent
    onClose: () => void;
    onSaved: (agent: UserAgent) => void; // called after successful create or update
}

const AgentConfigModal = (props: Props) => {
    const {show, mode, agent, services, onClose, onSaved} = props;
    const intl = useIntl();

    const [activeTab, setActiveTab] = useState<Tab>('config');
    const [draft, setDraft] = useState<AgentDraft>(emptyDraft);
    const [avatarFile, setAvatarFile] = useState<File | null>(null);
    const [saving, setSaving] = useState(false);
    const [errors, setErrors] = useState<Record<string, string>>({});

    // Reset form when modal opens
    useEffect(() => {
        if (show) {
            setActiveTab('config');
            setDraft(agent ? agentToDraft(agent) : emptyDraft);
            setAvatarFile(null);
            setErrors({});
        }
    }, [show, agent]);

    // Leave MCPs tab if tools are disabled
    useEffect(() => {
        if (draft.disableTools && activeTab === 'mcps') {
            setActiveTab('config');
        }
    }, [draft.disableTools, activeTab]);

    // Escape key to close
    useEffect(() => {
        if (!show) {
            return () => {
                // No keydown listener registered while modal is hidden
            };
        }
        const handler = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                onClose();
            }
        };
        document.addEventListener('keydown', handler);
        return () => document.removeEventListener('keydown', handler);
    }, [show, onClose]);

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

            onSaved(savedAgent);
        } catch (e: any) {
            const message = e?.message || '';
            if (e?.status_code === 409 || (message.includes('username') && (message.includes('taken') || message.includes('conflict')))) {
                setErrors({username: intl.formatMessage({defaultMessage: 'This username is already taken'})});
                setActiveTab('config');
            } else if (e?.status_code === 403) {
                setErrors({general: intl.formatMessage({defaultMessage: 'You do not have permission to perform this action.'})});
            } else {
                setErrors({general: intl.formatMessage({defaultMessage: 'Failed to save agent. Please try again.'})});
            }
        } finally {
            setSaving(false);
        }
    }, [mode, agent, draft, avatarFile, intl, onSaved, validate]);

    if (!show) {
        return null;
    }

    const title = mode === 'create' ?
        intl.formatMessage({defaultMessage: 'New Agent'}) :
        draft.displayName || intl.formatMessage({defaultMessage: 'Edit Agent'});

    return (
        <ModalOverlay onClick={onClose}>
            <ModalContainer onClick={(e) => e.stopPropagation()}>
                <ModalHeader>
                    <ModalTitle>{title}</ModalTitle>
                    <CloseButton onClick={onClose}>
                        <CloseIcon size={20}/>
                    </CloseButton>
                </ModalHeader>

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

                <ModalBody>
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
                            onChange={(updates) => updateDraft(updates)}
                        />
                    )}
                </ModalBody>

                <ModalFooter>
                    <CancelButton onClick={onClose}>
                        <FormattedMessage defaultMessage='Cancel'/>
                    </CancelButton>
                    <SaveButton
                        onClick={handleSave}
                        disabled={saving}
                    >
                        {saving ?
                            <FormattedMessage defaultMessage='Saving...'/> :
                            <FormattedMessage defaultMessage='Save'/>
                        }
                    </SaveButton>
                </ModalFooter>
            </ModalContainer>
        </ModalOverlay>
    );
};

// --- Styled Components ---

const ModalOverlay = styled.div`
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    background-color: rgba(0, 0, 0, 0.64);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 2000;
`;

// Fixed-height modal keeps the top edge anchored so it doesn't jump around when
// the active tab or AI Service selection changes how tall the body content is.
// The body scrolls internally (see ModalBody) when content exceeds the frame.
const ModalContainer = styled.div`
    background-color: var(--center-channel-bg);
    border-radius: 12px;
    width: 700px;
    height: min(720px, 85vh);
    min-height: 0;
    display: flex;
    flex-direction: column;
    overflow: hidden;
    box-shadow: 0px 8px 24px rgba(0, 0, 0, 0.12);
`;

const ModalHeader = styled.div`
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 24px 32px 0;
`;

const ModalTitle = styled.h2`
    font-weight: 600;
    font-size: 22px;
    line-height: 28px;
    color: var(--center-channel-color);
    margin: 0;
`;

const CloseButton = styled.button`
    background: none;
    border: none;
    cursor: pointer;
    padding: 10px;
    border-radius: 4px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    display: flex;
    align-items: center;
    justify-content: center;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.08);
    }
`;

const TabsContainer = styled.div`
    display: flex;
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    margin: 16px 32px 0;
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

    &:first-child {
        padding-left: 0;
    }
`;

const ModalBody = styled.div`
    padding: 24px 32px;
    overflow-y: auto;
    flex: 1;
    min-height: 0;
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

const ModalFooter = styled.div`
    display: flex;
    justify-content: flex-end;
    align-items: center;
    padding: 16px 32px 24px;
    gap: 8px;
    border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;

const CancelButton = styled(TertiaryButton)`
    height: 40px;
`;

const SaveButton = styled(PrimaryButton)`
    height: 40px;
`;

export default AgentConfigModal;
