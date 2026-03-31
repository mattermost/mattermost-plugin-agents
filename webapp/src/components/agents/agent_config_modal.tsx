// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {CloseIcon} from '@mattermost/compass-icons/components';

import {createAgent, updateAgent, uploadAgentAvatar} from '@/client';
import {UserAgent, CreateAgentRequest, UpdateAgentRequest, EnabledTool} from '@/types/agents';
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
};

function agentToDraft(agent: UserAgent): AgentDraft {
    return {
        displayName: agent.displayName,
        username: agent.username,
        serviceId: agent.serviceId,
        customInstructions: agent.customInstructions,
        channelAccessLevel: agent.channelAccessLevel,
        channelIds: agent.channelIds ?? [],
        userAccessLevel: agent.userAccessLevel,
        userIds: agent.userIds ?? [],
        teamIds: agent.teamIds ?? [],
        adminUserIds: agent.adminUserIds ?? [],
        enabledTools: agent.enabledTools ?? [],
    };
}

type Props = {
    show: boolean;
    mode: Mode;
    agent?: UserAgent;           // provided when mode === 'edit'
    onClose: () => void;
    onSaved: (agent: UserAgent) => void;  // called after successful create or update
}

const AgentConfigModal = (props: Props) => {
    const {show, mode, agent, onClose, onSaved} = props;
    const intl = useIntl();

    const [activeTab, setActiveTab] = useState<Tab>('config');
    const [draft, setDraft] = useState<AgentDraft>(emptyDraft);
    const [avatarFile, setAvatarFile] = useState<File | null>(null);
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState<string | null>(null);

    // Reset form when modal opens
    useEffect(() => {
        if (show) {
            setActiveTab('config');
            setDraft(agent ? agentToDraft(agent) : emptyDraft);
            setAvatarFile(null);
            setError(null);
        }
    }, [show, agent]);

    // Escape key to close
    useEffect(() => {
        if (!show) {
            return;
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
    }, []);

    const handleSave = useCallback(async () => {
        // Validation
        if (!draft.displayName.trim()) {
            setError(intl.formatMessage({defaultMessage: 'Display name is required.'}));
            setActiveTab('config');
            return;
        }
        if (!draft.username.trim()) {
            setError(intl.formatMessage({defaultMessage: 'Username is required.'}));
            setActiveTab('config');
            return;
        }
        if (!draft.serviceId) {
            setError(intl.formatMessage({defaultMessage: 'An AI service must be selected.'}));
            setActiveTab('config');
            return;
        }

        // Username validation: lowercase alphanumeric + dots/hyphens/underscores, starts with letter
        const usernameRegex = /^[a-z][a-z0-9.\-_]*$/;
        if (!usernameRegex.test(draft.username)) {
            setError(intl.formatMessage({defaultMessage: 'Username must start with a letter and contain only lowercase letters, numbers, dots, hyphens, or underscores.'}));
            setActiveTab('config');
            return;
        }

        setSaving(true);
        setError(null);

        try {
            let savedAgent: UserAgent;

            if (mode === 'create') {
                const req: CreateAgentRequest = {
                    display_name: draft.displayName,
                    username: draft.username,
                    service_id: draft.serviceId,
                    custom_instructions: draft.customInstructions,
                    channel_access_level: draft.channelAccessLevel,
                    channel_ids: draft.channelIds,
                    user_access_level: draft.userAccessLevel,
                    user_ids: draft.userIds,
                    team_ids: draft.teamIds,
                    admin_user_ids: draft.adminUserIds,
                    enabled_tools: draft.enabledTools,
                };
                savedAgent = await createAgent(req);
            } else {
                const req: UpdateAgentRequest = {
                    display_name: draft.displayName,
                    username: draft.username,
                    service_id: draft.serviceId,
                    custom_instructions: draft.customInstructions,
                    channel_access_level: draft.channelAccessLevel,
                    channel_ids: draft.channelIds,
                    user_access_level: draft.userAccessLevel,
                    user_ids: draft.userIds,
                    team_ids: draft.teamIds,
                    admin_user_ids: draft.adminUserIds,
                    enabled_tools: draft.enabledTools,
                };
                savedAgent = await updateAgent(agent!.id, req);
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
            const status = e?.status_code;
            if (status === 403) {
                setError(intl.formatMessage({defaultMessage: 'You do not have permission to perform this action.'}));
            } else if (status === 400) {
                setError(intl.formatMessage({defaultMessage: 'Invalid input. Please check the form fields.'}));
            } else {
                setError(intl.formatMessage({defaultMessage: 'Failed to save agent. Please try again.'}));
            }
        } finally {
            setSaving(false);
        }
    }, [mode, agent, draft, avatarFile, intl, onSaved]);

    if (!show) {
        return null;
    }

    const title = mode === 'create'
        ? intl.formatMessage({defaultMessage: 'New Agent'})
        : draft.displayName || intl.formatMessage({defaultMessage: 'Edit Agent'});

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
                        onClick={() => setActiveTab('mcps')}
                    >
                        <FormattedMessage defaultMessage='MCPs'/>
                    </TabButton>
                </TabsContainer>

                <ModalBody>
                    {error && <ErrorBanner>{error}</ErrorBanner>}

                    {activeTab === 'config' && (
                        <ConfigTab
                            draft={draft}
                            onChange={updateDraft}
                            onAvatarChange={setAvatarFile}
                            botUserId={agent?.botUserId}
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
                            onChange={(enabledTools) => updateDraft({enabledTools})}
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
                        {saving
                            ? <FormattedMessage defaultMessage='Saving...'/>
                            : <FormattedMessage defaultMessage='Save'/>
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

const ModalContainer = styled.div`
    background-color: var(--center-channel-bg);
    border-radius: 12px;
    width: 700px;
    max-height: 85vh;
    display: flex;
    flex-direction: column;
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

    &:hover {
        color: ${(p) => (p.$active ? 'var(--button-bg)' : 'var(--center-channel-color)')};
    }

    &:first-child {
        padding-left: 0;
    }
`;

const ModalBody = styled.div`
    padding: 24px 32px;
    overflow-y: auto;
    flex: 1;
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
