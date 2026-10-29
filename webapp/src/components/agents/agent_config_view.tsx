// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {ArrowLeftIcon, ExportVariantIcon, ImportIcon, UpdateIcon} from '@mattermost/compass-icons/components';

import {Button} from '@mattermost/compass-ui/components/button';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';

import {createAgent, getUserMCPTools, updateAgent, uploadAgentAvatar} from '@/client';
import {TooltipAboveModals} from '@/components/dropdown';
import {
    AgentExportDocument,
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
import {defaultReasoningEffort} from '@/components/system_console/reasoning_config';
import ConfirmationDialog from '@/components/confirmation_dialog';
import {UnderlineTab, UnderlineTabs} from '@/components/underline_tabs';
import {useIsLicensedFor} from '@/license';
import {useABACSupport} from '@/utils/access_control';
import {useCurrentUserHasSystemPermission} from '@/utils/permissions';
import {downloadAgentExport, matchImportedMCPTools} from '@/utils/agent_export';

import {useAgentFilePicker} from './use_agent_file_picker';
import ConfigTab from './tabs/config_tab';
import AccessTab from './tabs/access_tab';
import McpsTab from './tabs/mcps_tab';
import AgentVersionHistoryModal from './agent_version_history_modal';
import {formatRelativeTime} from './agent_version_labels';
import {hasVersions, useAgentVersionHistory} from './use_agent_version_history';

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

function agentToDraft(agent: UserAgent, services: ServiceInfo[]): AgentDraft {
    const serviceType = services.find((s) => s.id === agent.serviceID)?.type;
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
        reasoningEffort: agent.reasoningEffort || defaultReasoningEffort(serviceType),
        thinkingBudget: agent.thinkingBudget ?? 0,
        maxToolTurns: agent.maxToolTurns && agent.maxToolTurns > 0 ? agent.maxToolTurns : DefaultMaxToolTurns,
    };
}

export type AgentConfigFrameParts = {
    title: React.ReactNode;

    /** The "Version N" line (or its error), or null. */
    subtitle: React.ReactNode;

    /** Version history, Import and Export. */
    headerAction: React.ReactNode;

    /** The Configuration, Access and MCPs tabs. */
    tabs: React.ReactNode;

    /** The active tab's panel; scrolls on its own. */
    body: React.ReactNode;

    /** Cancel and Save. */
    footer: React.ReactNode;
}

type Props = {
    mode: Mode;
    agent?: UserAgent; // provided when mode === 'edit'
    services: ServiceInfo[]; // pre-fetched from parent
    onBack: () => void;
    onSaved: (agent: UserAgent) => void; // called after successful create or update
    onRestored?: (agent: UserAgent) => void; // called after a version was restored; the editor stays open

    /** An agent export file to load into the form when the view opens. */
    importedDocument?: AgentExportDocument;

    /**
     * 'page' (default) is the full page editor. 'modal' is the same view inside a modal sheet: no Back
     * button (the modal has its close button), less room above the title, and the owner handles Escape
     * (see closeRequestRef).
     */
    variant?: 'page' | 'modal';

    /**
     * Set to the view's close request while it is mounted: it closes at once, or asks to discard
     * unsaved changes first. A modal's close button, backdrop and Escape all go through it.
     */
    closeRequestRef?: React.MutableRefObject<(() => void) | null>;

    /**
     * Lays the editor out in the owner's own frame instead of the page's header and footer (a modal passes
     * the compass Modal's title row and footer slot). Receives the pieces to place; the discard, rename and
     * history dialogs stay with the view.
     */
    renderFrame?: (parts: AgentConfigFrameParts) => React.ReactNode;
}

const DISCARD_CHANGES_TITLE_ID = 'discard-agent-changes-title';
const RENAME_AGENT_TITLE_ID = 'rename-agent-title';

const AgentConfigView = (props: Props) => {
    const {mode, agent, services, onBack, onSaved, onRestored, importedDocument, variant = 'page', closeRequestRef, renderFrame} = props;
    const inModal = variant === 'modal';
    const intl = useIntl();

    // Parent owns the manage_system check via useCurrentUserHasSystemPermission.
    const canEditServiceAccountAuth = useCurrentUserHasSystemPermission('manage_system');
    const {supported: abacSupported} = useABACSupport();
    const providerWebSearchLicensed = useIsLicensedFor('provider_web_search');

    const [activeTab, setActiveTab] = useState<Tab>('config');
    const initialDraft = useMemo(() => {
        if (agent) {
            return agentToDraft(agent, services);
        }
        const draft = cloneDraft(emptyDraft);
        if (services.length > 0) {
            draft.serviceId = services[0].id;
            draft.reasoningEffort = defaultReasoningEffort(services[0].type);
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
    const [exporting, setExporting] = useState(false);
    const [errors, setErrors] = useState<Record<string, string>>({});
    const [showDiscardDialog, setShowDiscardDialog] = useState(false);
    const [showRenameDialog, setShowRenameDialog] = useState(false);
    const [importing, setImporting] = useState(false);
    const [showHistory, setShowHistory] = useState(false);
    const history = useAgentVersionHistory(mode === 'edit' && agent ? agent.id : '');
    const refreshHistory = history.refresh;

    // MCP server origins whose tools the last imported file referenced but
    // this server does not list; null until a file is imported.
    const [importMissingServers, setImportMissingServers] = useState<string[] | null>(null);
    const importRequestRef = useRef(0);
    const dialogOpenRef = useRef(false);
    dialogOpenRef.current = showDiscardDialog || showRenameDialog || showHistory;

    const savedUsername = (mode === 'edit' && agent?.name) || '';
    const isRenaming = savedUsername !== '' && draft.username !== savedUsername;

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
        if (dialogOpenRef.current) {
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

    useEffect(() => {
        if (closeRequestRef) {
            closeRequestRef.current = requestBack;
        }
        return () => {
            if (closeRequestRef) {
                closeRequestRef.current = null;
            }
        };
    }, [closeRequestRef, requestBack]);

    // Escape key: same as back — confirm when there are unsaved changes.
    // Skip Escapes a child already handled (e.g. closing a picker menu or a nested dialog).
    // A modal's shell owns Escape (it also defers to open menus and dialogs) and calls closeRequestRef.
    useEffect(() => {
        const handler = (e: KeyboardEvent) => {
            if (inModal || e.key !== 'Escape' || e.defaultPrevented) {
                return;
            }
            if (dialogOpenRef.current) {
                return;
            }
            e.preventDefault();
            requestBack();
        };
        document.addEventListener('keydown', handler);
        return () => document.removeEventListener('keydown', handler);
    }, [requestBack, inModal]);

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

    const handleVersionRestored = useCallback((restored: UserAgent) => {
        const restoredDraft = agentToDraft(restored, services);

        // Switching access away from attribute-based deletes the agent's
        // policy, so a restored attribute-based version has none until one
        // is created on the Access tab.
        const needsAccessPolicy = restoredDraft.userAccessLevel === UserAccessLevel.AttributeBased &&
            baselineDraft.userAccessLevel !== UserAccessLevel.AttributeBased;
        setDraft(restoredDraft);
        setBaselineDraft(cloneDraft(restoredDraft));
        setAvatarFile(null);
        setErrors({});
        setImportMissingServers(null);
        if (needsAccessPolicy) {
            setShowHistory(false);
            setActiveTab('access');
        }
        onRestored?.(restored);
    }, [baselineDraft.userAccessLevel, onRestored, services]);

    useEffect(() => {
        return () => {
            importRequestRef.current += 1;
        };
    }, []);

    // Loads the file's transferable settings into the form; saving applies
    // them through the normal create or update. The name and display name are
    // only suggestions for a new agent.
    const applyImport = useCallback(async (doc: AgentExportDocument) => {
        importRequestRef.current += 1;
        const request = importRequestRef.current;
        const imported = doc.agent;
        setImporting(true);
        try {
            let enabledTools: EnabledTool[] = [];
            let missingServerOrigins: string[] = [];
            if (!imported.autoEnableNewMCPTools && imported.mcpTools.length > 0) {
                const {servers} = await getUserMCPTools({agentId: agent?.id, serviceAccount: draft.useServiceAccountAuth});
                ({enabledTools, missingServerOrigins} = matchImportedMCPTools(imported.mcpTools, servers ?? []));
            }
            if (request !== importRequestRef.current) {
                return;
            }
            updateDraft({
                ...(mode === 'create' && imported.name ? {username: imported.name} : {}),
                ...(mode === 'create' && imported.displayName ? {displayName: imported.displayName} : {}),
                customInstructions: imported.customInstructions,
                disableTools: imported.disableTools,
                maxToolTurns: imported.maxToolTurns > 0 ? imported.maxToolTurns : DefaultMaxToolTurns,
                mcpDynamicToolLoading: imported.mcpDynamicToolLoading,
                autoEnableNewMCPTools: imported.autoEnableNewMCPTools,
                enabledTools,
            });
            setImportMissingServers(missingServerOrigins);
            setActiveTab('config');
        } catch {
            if (request === importRequestRef.current) {
                setErrors({general: intl.formatMessage({defaultMessage: 'Failed to load your MCP servers, so the agent file was not imported. Please try again.'})});
            }
        } finally {
            if (request === importRequestRef.current) {
                setImporting(false);
            }
        }
    }, [agent?.id, draft.useServiceAccountAuth, intl, mode, updateDraft]);

    const filePicker = useAgentFilePicker(applyImport, (message) => setErrors({general: message}));

    const initialImportRef = useRef(importedDocument ?? null);
    useEffect(() => {
        const doc = initialImportRef.current;
        initialImportRef.current = null;
        if (doc) {
            applyImport(doc);
        }
    }, [applyImport]);

    const handleExport = useCallback(async () => {
        if (!agent || exporting) {
            return;
        }
        setExporting(true);
        try {
            await downloadAgentExport(agent.id);
        } catch (e: any) {
            const message = (typeof e?.message === 'string' ? e.message : '').trim();
            setErrors({general: message || intl.formatMessage({defaultMessage: 'Failed to export agent. Please try again.'})});
        } finally {
            setExporting(false);
        }
    }, [agent, exporting, intl]);

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

    const save = useCallback(async () => {
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
            if (mode === 'edit') {
                refreshHistory();
            }

            onSaved(savedAgent);
        } catch (e: any) {
            const message = (typeof e?.message === 'string' ? e.message : '').trim();
            if (e?.status_code === 409 || (message.includes('username') && (message.includes('taken') || message.includes('conflict')))) {
                if (e?.server_error_id === 'username_held_by_deactivated_bot') {
                    setErrors({username: intl.formatMessage({defaultMessage: 'This username belongs to a deactivated bot account. Permanently delete that bot account to make the username available.'})});
                } else {
                    setErrors({username: intl.formatMessage({defaultMessage: 'This username is already taken'})});
                }
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
    }, [mode, agent, draft, avatarFile, intl, onSaved, refreshHistory]);

    const handleSave = useCallback(() => {
        const validationErrors = validate();
        if (Object.keys(validationErrors).length > 0) {
            setErrors(validationErrors);
            setActiveTab('config');
            return;
        }
        setErrors({});
        if (isRenaming) {
            setShowRenameDialog(true);
            return;
        }
        save();
    }, [validate, isRenaming, save]);

    const handleRenameConfirm = useCallback(() => {
        setShowRenameDialog(false);
        save();
    }, [save]);

    const handleRenameCancel = useCallback(() => {
        setShowRenameDialog(false);
    }, []);

    const title = mode === 'create' ? intl.formatMessage({defaultMessage: 'New Agent'}) : draft.displayName || intl.formatMessage({defaultMessage: 'Edit Agent'});

    const versionHistory = mode === 'edit' && agent && hasVersions(history.list) ? history.list : null;
    const canViewHistory = versionHistory !== null;
    let versionLine: React.ReactNode = null;
    if (versionHistory) {
        const current = versionHistory.versions.find((v) => v.version === versionHistory.currentVersion) ?? versionHistory.versions[0];
        const summary = intl.formatMessage(
            {defaultMessage: 'v{version} · by {author}, {time}'},
            {
                version: current.version,
                author: history.authorLabel(current.createdBy),
                time: formatRelativeTime(intl, current.createAt),
            },
        );
        const unsaved = intl.formatMessage({defaultMessage: 'Unsaved changes'});
        versionLine = (
            <VersionLineButton
                type='button'
                aria-haspopup='dialog'
                aria-label={intl.formatMessage(
                    {defaultMessage: 'Version history, {summary}'},
                    {summary: isDirty ? `${summary} · ${unsaved}` : summary},
                )}
                onClick={() => setShowHistory(true)}
            >
                {isDirty ? `${summary} · ${unsaved}` : summary}
            </VersionLineButton>
        );
    } else if (mode === 'edit' && agent && history.listError) {
        versionLine = (
            <VersionLineError role='alert'>
                {history.listError}
                {' '}
                <Button
                    emphasis='link'
                    size='small'
                    onClick={() => history.refresh()}
                >
                    <FormattedMessage defaultMessage='Retry'/>
                </Button>
            </VersionLineError>
        );
    }

    const headerActions = (
        <HeaderActions>
            {canViewHistory && (
                <TooltipAboveModals title={intl.formatMessage({defaultMessage: 'Version history'})}>
                    <IconButton
                        icon={<Icon glyph={<UpdateIcon/>}/>}
                        size='medium'
                        onClick={() => setShowHistory(true)}
                        aria-haspopup='dialog'
                        aria-label={intl.formatMessage({defaultMessage: 'Version history'})}
                    />
                </TooltipAboveModals>
            )}
            <Button
                emphasis='tertiary'
                leadingIcon={<Icon glyph={<ImportIcon/>}/>}
                onClick={filePicker.open}
                loading={importing}
                disabled={saving}
            >
                <FormattedMessage defaultMessage='Import'/>
            </Button>
            {filePicker.input}
            {mode === 'edit' && agent && (
                <Button
                    emphasis='tertiary'
                    leadingIcon={<Icon glyph={<ExportVariantIcon/>}/>}
                    onClick={handleExport}
                    loading={exporting}
                    disabled={saving}
                >
                    {exporting ? <FormattedMessage defaultMessage='Exporting...'/> : <FormattedMessage defaultMessage='Export'/>}
                </Button>
            )}
        </HeaderActions>
    );

    const tabs = (
        <ConfigTabs
            role='tablist'
            $modal={inModal}
        >
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
                {...(mcpsTabDisabled ? {title: intl.formatMessage({defaultMessage: 'Enable Tools to configure MCP integrations'})} : {})}
                onClick={() => setActiveTab('mcps')}
            >
                <FormattedMessage defaultMessage='MCPs'/>
            </UnderlineTab>
        </ConfigTabs>
    );

    const viewBody = (
        <ViewBody $modal={inModal}>
            {errors.general && (
                <Notice
                    type='danger'
                    title={errors.general}
                />
            )}
            {importMissingServers !== null && (
                <Notice
                    type={importMissingServers.length > 0 ? 'warning' : 'info'}
                    title={intl.formatMessage({defaultMessage: 'Settings loaded from the agent file'})}
                    description={importMissingServers.length > 0 ? intl.formatMessage(
                        {defaultMessage: 'Tools from MCP servers that are not available here were left out: {servers}. Review the settings, then save to apply them.'},
                        {servers: importMissingServers.join(', ')},
                    ) : intl.formatMessage({defaultMessage: 'Review the settings, then save to apply them.'})}
                    role='status'
                    onDismiss={() => setImportMissingServers(null)}
                    dismissLabel={intl.formatMessage({defaultMessage: 'Dismiss'})}
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
                        avatarFile={avatarFile}
                        botUserId={agent?.botUserID}
                        services={services}
                        errors={errors}
                        savedUsername={savedUsername}
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
    );

    const footerButtons = (
        <>
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
        </>
    );

    // In a modal the compass Modal supplies the header and footer (its title row, close button and footer
    // slot), so the owner receives the pieces instead of the page's header and footer.
    const frame = renderFrame ? renderFrame({title, subtitle: versionLine, headerAction: headerActions, tabs, body: viewBody, footer: footerButtons}) : (
        <ViewContainer>
            <ViewHeader $modal={inModal}>
                <HeaderLeading>
                    {!inModal && (
                        <BackButton
                            icon={<Icon glyph={<ArrowLeftIcon/>}/>}
                            size='medium'
                            onClick={requestBack}
                            disabled={saving}
                            aria-label={intl.formatMessage({defaultMessage: 'Back to agents'})}
                        />
                    )}
                    <HeaderTitleBlock>
                        <ViewTitle>{title}</ViewTitle>
                        {versionLine}
                    </HeaderTitleBlock>
                </HeaderLeading>
                {headerActions}
            </ViewHeader>

            {tabs}

            {viewBody}

            <ViewFooter>
                {footerButtons}
            </ViewFooter>
        </ViewContainer>
    );

    return (
        <>
            {frame}
            {mode === 'edit' && agent && (
                <AgentVersionHistoryModal
                    show={showHistory}
                    onClose={() => setShowHistory(false)}
                    agentId={agent.id}
                    agentName={agent.displayName}
                    services={services}
                    history={history}
                    isDirty={isDirty}
                    currentUserAccessLevel={baselineDraft.userAccessLevel}
                    onRestored={handleVersionRestored}
                />
            )}
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
            <ConfirmationDialog
                show={showRenameDialog}
                titleId={RENAME_AGENT_TITLE_ID}
                title={<FormattedMessage defaultMessage='Change agent username?'/>}
                message={(
                    <>
                        <FormattedMessage
                            defaultMessage='The agent will be renamed from <b>@{oldName}</b> to <b>@{newName}</b>. Its direct messages, channel memberships, and conversation history are kept. After the change:'
                            values={{
                                oldName: savedUsername,
                                newName: draft.username,
                                b: (chunks: React.ReactNode) => <strong>{chunks}</strong>,
                            }}
                        />
                        <RenameConsequences>
                            <li>
                                <FormattedMessage
                                    defaultMessage='Existing messages that mention @{oldName} will no longer link to the agent.'
                                    values={{oldName: savedUsername}}
                                />
                            </li>
                            <li>
                                <FormattedMessage
                                    defaultMessage='People must mention @{newName} to reach the agent, and slash commands, scripts, or integrations that use @{oldName} must be updated.'
                                    values={{oldName: savedUsername, newName: draft.username}}
                                />
                            </li>
                            <li>
                                <FormattedMessage
                                    defaultMessage='@{oldName} becomes available, so another user or bot could claim it.'
                                    values={{oldName: savedUsername}}
                                />
                            </li>
                        </RenameConsequences>
                    </>
                )}
                confirmButtonText={<FormattedMessage defaultMessage='Change username'/>}
                cancelButtonText={<FormattedMessage defaultMessage='Keep current username'/>}
                onConfirm={handleRenameConfirm}
                onCancel={handleRenameCancel}
                confirmPending={saving}
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

const ViewHeader = styled.div<{$modal: boolean}>`
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: ${({$modal}) => ($modal ? 'var(--spacing-xs)' : 'var(--spacing-xxxxxl)')} 0 var(--spacing-l) 0;
    flex-shrink: 0;
`;

const HeaderActions = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-xs);
    flex-shrink: 0;
`;

const ConfigTabs = styled(UnderlineTabs)<{$modal: boolean}>`
    padding: ${({$modal}) => ($modal ? '0 0 0 var(--spacing-xxxl)' : '0 var(--spacing-l)')};
`;

const HeaderLeading = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-xs);
    min-width: 0;
`;

const HeaderTitleBlock = styled.div`
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    min-width: 0;
`;

// Reads as quiet metadata under the title rather than a button or link.
const VersionLineButton = styled.button`
    max-width: 100%;
    margin: 0;
    padding: 0;
    border: none;
    background: transparent;
    cursor: pointer;
    text-align: left;
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;

    &:hover {
        text-decoration: underline;
    }
`;

const VersionLineError = styled.div`
    font-size: 12px;
    line-height: 16px;
    color: var(--error-text);
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

const ViewBody = styled.div<{$modal: boolean}>`
    padding: ${({$modal}) => ($modal ? 'var(--spacing-xxl) var(--spacing-xxxl)' : 'var(--spacing-xxxl) var(--spacing-l)')};
    flex: 1;
    min-height: 0;
    overflow-y: auto;
`;

const Notice = styled(SectionNotice)`
    margin-bottom: var(--spacing-l);
`;

const RenameConsequences = styled.ul`
    margin: 12px 0 0;
    padding-left: 20px;

    li + li {
        margin-top: 4px;
    }
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
