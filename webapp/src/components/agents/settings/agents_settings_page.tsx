// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {Button} from '@mattermost/compass-ui/components/button';
import {EmptyState} from '@mattermost/compass-ui/components/empty-state';
import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';
import {Spinner} from '@mattermost/compass-ui/components/spinner';
import {Tabs} from '@mattermost/compass-ui/components/tabs';

import {getAIBots, getPluginConfig, savePluginConfig} from '@/client';

import {LLMService} from '@/components/system_console/service';
import {firstNewService} from '@/components/system_console/services';
import NoServicesPage from '@/components/system_console/no_services_page';
import {SettingsReadOnlyContext} from '@/components/system_console/settings_read_only';
import {MCPServerConfig} from '@/components/system_console/mcp_servers';
import {PersistMCPServerScope} from '@/components/system_console/mcp_types';
import {PluginConfig} from '@/components/system_console/plugin_config_types';

import {SETTINGS_PATH, useNavigationGuard} from '../use_agents_route';

import {DEFAULT_SETTINGS_SECTION, SETTINGS_SECTIONS, SettingsSection} from './sections';
import {withSavedMCPServer, withoutMCPServer} from './mcp_server_persistence';
import SettingsSectionContent, {RuntimeBotOption, defaultConfig} from './settings_sections';
import {useSettingsAccess} from './use_settings_access';

const isSettingsPath = (pathname: string) => pathname === SETTINGS_PATH || pathname.startsWith(`${SETTINGS_PATH}/`);

type Props = {
    section?: SettingsSection;
    onSelectSection: (section: SettingsSection) => void;
};

/**
 * The plugin configuration as a page of the Agents product (the `settings`
 * view). System admins and users they delegate to on the System Console reach
 * it; read-only users see everything but credentials and cannot save.
 */
const AgentsSettingsPage = ({section = DEFAULT_SETTINGS_SECTION, onSelectSection}: Props) => {
    const intl = useIntl();
    const access = useSettingsAccess();
    const canView = access.level === 'read' || access.level === 'write';
    const readOnly = access.level !== 'write';
    const canEdit = !readOnly;

    // The blank service offered to an editor on a workspace with none. Until
    // they change it, it is not part of what Save sends.
    const draftServiceRef = useRef<LLMService | null>(null);

    const [localConfig, setLocalConfig] = useState<PluginConfig>(defaultConfig);
    const [savedConfig, setSavedConfig] = useState<PluginConfig>(defaultConfig);
    const [loading, setLoading] = useState(true);
    const [loadError, setLoadError] = useState<string | null>(null);
    const [dirty, setDirty] = useState(false);
    const [saving, setSaving] = useState(false);
    const [saveError, setSaveError] = useState<string | null>(null);
    const [linkDomainsError, setLinkDomainsError] = useState('');
    const [runtimeBots, setRuntimeBots] = useState<RuntimeBotOption[]>([]);
    const [runtimeBotsError, setRuntimeBotsError] = useState<string | null>(null);

    useEffect(() => {
        if (!canView) {
            return () => {/* nothing to cancel */};
        }
        let current = true;
        const load = async () => {
            try {
                const cfg = await getPluginConfig();
                if (current) {
                    const loaded = {...defaultConfig, ...cfg};
                    setSavedConfig(loaded);
                    if (canEdit && !loaded.services?.length) {
                        draftServiceRef.current = {...firstNewService};
                        setLocalConfig({...loaded, services: [draftServiceRef.current]});
                    } else {
                        setLocalConfig(loaded);
                    }
                    setLoadError(null);
                }
            } catch {
                if (current) {
                    setLoadError(intl.formatMessage({defaultMessage: 'Failed to load configuration.'}));
                }
            } finally {
                if (current) {
                    setLoading(false);
                }
            }
        };
        load();
        return () => {
            current = false;
        };
    }, [canView, canEdit, intl]);

    useEffect(() => {
        if (!canView || loading || loadError) {
            return () => {/* nothing to cancel */};
        }
        let current = true;
        getAIBots().
            then((res) => {
                if (current) {
                    setRuntimeBots(res.bots ?? []);
                    setRuntimeBotsError(null);
                }
            }).
            catch(() => {
                if (current) {
                    setRuntimeBotsError(intl.formatMessage({defaultMessage: 'Failed to load the runtime bot list. The previous list is kept.'}));
                }
            });
        return () => {
            current = false;
        };
    }, [canView, loading, loadError, intl]);

    // Editors also adjust the configuration on their own (seeding vetted tool
    // policies, normalizing services); for a read-only viewer those changes can
    // be neither saved nor discarded, so nothing counts as unsaved.
    const unsaved = dirty && !readOnly;

    // Leaving the settings within the webapp drops unsaved changes; switching
    // sections keeps them.
    useNavigationGuard(unsaved, isSettingsPath, intl.formatMessage({defaultMessage: 'You have unsaved changes to the Agents settings. Leave without saving?'}));

    // Leaving the page for another site or a reload drops unsaved changes.
    useEffect(() => {
        if (!unsaved) {
            return () => {/* nothing to remove */};
        }
        const warn = (e: BeforeUnloadEvent) => {
            e.preventDefault();
        };
        window.addEventListener('beforeunload', warn);
        return () => window.removeEventListener('beforeunload', warn);
    }, [unsaved]);

    const updateConfig = useCallback((updates: Partial<PluginConfig>) => {
        if ('allowedLinkDomains' in updates) {
            setLinkDomainsError('');
        }
        setLocalConfig((prev) => ({...prev, ...updates}));
        setDirty(true);
    }, []);

    const updateMCP = useCallback((update: PluginConfig['mcp'] | ((current: PluginConfig['mcp']) => PluginConfig['mcp'])) => {
        setLocalConfig((prev) => {
            const updated = typeof update === 'function' ? update(prev.mcp || defaultConfig.mcp) : update;
            return {...prev, mcp: {...updated, servers: updated.servers || []}};
        });
        setDirty(true);
    }, []);

    // persistMCPServer saves one MCP server on its own, merged into the
    // configuration as the server has it, so other unsaved edits on this page
    // are not saved with it. A server without an id is added. Scope
    // tool_configs writes only its tool policies; settings writes everything
    // else and keeps the saved tool policies unless server carries
    // tool_configs. The saved server is then reflected in the page's local
    // configuration.
    const persistMCPServer = useCallback(async (server: MCPServerConfig, scope: PersistMCPServerScope = 'server'): Promise<MCPServerConfig> => {
        const current = {...defaultConfig, ...(await getPluginConfig())};
        const currentMCP = current.mcp || defaultConfig.mcp;
        const currentServers: MCPServerConfig[] = currentMCP.servers ?? [];
        const stored = server.id ? currentServers.find((s) => s.id === server.id) : undefined; // eslint-disable-line no-undefined
        if (server.id && !stored) {
            throw new Error(intl.formatMessage({defaultMessage: 'The server is no longer configured. Reload the page.'}));
        }
        let next = server;
        if (stored && scope === 'tool_configs') {
            next = {...stored, tool_configs: server.tool_configs};
        } else if (stored && scope === 'settings') {
            next = {...stored, ...server, tool_configs: server.tool_configs ?? stored.tool_configs};
        }
        const servers = stored ? currentServers.map((s) => (s.id === server.id ? next : s)) : [...currentServers, next];

        let saved: PluginConfig;
        try {
            saved = {...defaultConfig, ...(await savePluginConfig({...current, mcp: {...currentMCP, servers}}))};
        } catch (e) {
            throw new Error((e as Error)?.message || intl.formatMessage({defaultMessage: 'Failed to save configuration.'}));
        }
        const savedServer = (saved.mcp?.servers ?? []).find((s: MCPServerConfig) => (
            server.id ? s.id === server.id : s.name === next.name && s.baseURL === next.baseURL
        ));
        if (!savedServer) {
            throw new Error(intl.formatMessage({defaultMessage: 'The saved server could not be found. Reload the page.'}));
        }

        setSavedConfig(saved);
        setLocalConfig((prev) => withSavedMCPServer(prev, savedServer, scope, defaultConfig.mcp));
        return savedServer;
    }, [intl]);

    // deleteMCPServer removes one saved MCP server on its own, like
    // persistMCPServer, leaving other unsaved edits on this page unsaved.
    const deleteMCPServer = useCallback(async (serverID: string): Promise<void> => {
        const current = {...defaultConfig, ...(await getPluginConfig())};
        const currentMCP = current.mcp || defaultConfig.mcp;
        const currentServers: MCPServerConfig[] = currentMCP.servers ?? [];
        let saved: PluginConfig;
        try {
            saved = {...defaultConfig, ...(await savePluginConfig({...current, mcp: {...currentMCP, servers: currentServers.filter((s) => s.id !== serverID)}}))};
        } catch (e) {
            throw new Error((e as Error)?.message || intl.formatMessage({defaultMessage: 'Failed to save configuration.'}));
        }
        setSavedConfig(saved);
        setLocalConfig((prev) => withoutMCPServer(prev, serverID, defaultConfig.mcp));
    }, [intl]);

    const save = async () => {
        setSaving(true);
        setSaveError(null);
        try {
            const draft = draftServiceRef.current;
            const saved = await savePluginConfig({...localConfig, services: (localConfig.services ?? []).filter((svc) => svc !== draft)});

            // Adopt the normalized saved config so server-minted service/MCP
            // IDs (and the UI gated on them) appear without a reload.
            setLocalConfig({...defaultConfig, ...saved});
            setSavedConfig({...defaultConfig, ...saved});
            setDirty(false);
            setLinkDomainsError('');
        } catch (e) {
            if ((e as {server_error_id?: string})?.server_error_id === 'invalid_allowed_link_domains') {
                setLinkDomainsError(intl.formatMessage({defaultMessage: 'One or more entries are not valid domains.'}));
            }
            setSaveError(intl.formatMessage({defaultMessage: 'Failed to save configuration.'}));
        } finally {
            setSaving(false);
        }
    };

    const discard = () => {
        setLocalConfig(savedConfig);
        setDirty(false);
        setSaveError(null);
        setLinkDomainsError('');
    };

    const addFirstService = () => {
        updateConfig({services: [{...firstNewService}]});
        onSelectSection('services');
    };

    const tabLabels: Record<SettingsSection, React.ReactNode> = {
        general: <FormattedMessage defaultMessage='General'/>,
        services: <FormattedMessage defaultMessage='AI services'/>,
        mcp: <FormattedMessage defaultMessage='MCP servers'/>,
        search: <FormattedMessage defaultMessage='Embedding search'/>,
        'web-search': <FormattedMessage defaultMessage='Web search'/>,
        debug: <FormattedMessage defaultMessage='Debug'/>,
    };

    let body: React.ReactNode;
    if (access.loading || (canView && loading)) {
        body = (
            <LoadingContainer>
                <Spinner
                    size='32'
                    aria-label={intl.formatMessage({defaultMessage: 'Loading configuration...'})}
                />
            </LoadingContainer>
        );
    } else if (access.error) {
        body = (
            <SectionNotice
                type='danger'
                title={intl.formatMessage({defaultMessage: 'Failed to check your access to the Agents settings.'})}
            />
        );
    } else if (!canView) {
        body = (
            <EmptyState
                title={<FormattedMessage defaultMessage="You don't have access to the Agents settings"/>}
                description={<FormattedMessage defaultMessage='A system admin can give you access from the System Console.'/>}
            />
        );
    } else if (loadError) {
        body = (
            <SectionNotice
                type='danger'
                title={loadError}
            />
        );
    } else if (localConfig.services?.length) {
        body = (
            <>
                <TabBar
                    tabs={SETTINGS_SECTIONS.map((key) => ({key, label: tabLabels[key]}))}
                    activeKey={section}
                    onChange={(key) => onSelectSection(key as SettingsSection)}
                />
                <SettingsSectionContent
                    section={section}
                    value={localConfig}
                    savedConfig={savedConfig}
                    runtimeBots={runtimeBots}
                    runtimeBotsError={runtimeBotsError}
                    linkDomainsError={linkDomainsError}
                    updateConfig={updateConfig}
                    updateMCP={updateMCP}
                    persistMCPServer={persistMCPServer}
                    deleteMCPServer={deleteMCPServer}
                    draftService={draftServiceRef.current}
                />
            </>
        );
    } else {
        body = (
            <NoServicesPage onAddServicePressed={readOnly ? undefined : addFirstService}/> // eslint-disable-line no-undefined
        );
    }

    // Editors are read-only while a save is pending: its response replaces the
    // local configuration, so an edit made meanwhile would be lost.
    return (
        <SettingsReadOnlyContext.Provider value={readOnly || saving}>
            <PageContainer>
                <PageHeader>
                    <PageTitle>
                        <FormattedMessage defaultMessage='Settings'/>
                    </PageTitle>
                    <Subtitle>
                        <FormattedMessage defaultMessage='AI services, tools, search and other settings that apply to every agent in the workspace.'/>
                    </Subtitle>
                </PageHeader>
                {canView && readOnly && (
                    <Notices>
                        <SectionNotice
                            type='info'
                            title={<FormattedMessage defaultMessage='You have read-only access'/>}
                            description={<FormattedMessage defaultMessage='You can view these settings but not change them. Credentials such as API keys are hidden.'/>}
                        />
                    </Notices>
                )}
                <PageBody>{body}</PageBody>
            </PageContainer>
            {canView && !readOnly && (dirty || saveError) && (
                <SaveBar aria-label={intl.formatMessage({defaultMessage: 'Unsaved changes'})}>
                    <SaveBarContent>
                        <SaveBarMessage>
                            {saveError ?? <FormattedMessage defaultMessage='You have unsaved changes'/>}
                        </SaveBarMessage>
                        <Button
                            emphasis='tertiary'
                            onClick={discard}
                            disabled={saving || !dirty}
                        >
                            <FormattedMessage defaultMessage='Discard'/>
                        </Button>
                        <Button
                            emphasis='primary'
                            onClick={save}
                            loading={saving}
                            disabled={!dirty}
                        >
                            <FormattedMessage defaultMessage='Save'/>
                        </Button>
                    </SaveBarContent>
                </SaveBar>
            )}
        </SettingsReadOnlyContext.Provider>
    );
};

// Same column as the All agents and Custom Prompts views.
const CONTENT_MAX_WIDTH = '960px';

const PageContainer = styled.div`
    display: flex;
    flex: 1 0 auto;
    flex-direction: column;
    width: 100%;
    max-width: ${CONTENT_MAX_WIDTH};
    margin: 0 auto;
    padding: 0 var(--spacing-xxxl) var(--spacing-xxxl);
`;

const PageHeader = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xxxs);
    padding: var(--spacing-xxxxxl) 0 var(--spacing-xl);
`;

const PageTitle = styled.h1`
    margin: 0;
    font-family: var(--font-family-heading, 'Metropolis', sans-serif);
    font-size: var(--font-size-500);
    font-weight: var(--font-weight-semibold);
    line-height: var(--line-height-500);
    color: var(--center-channel-color);
`;

const Subtitle = styled.p`
    margin: 0;
    font-size: var(--font-size-75);
    line-height: var(--line-height-75);
    color: rgba(var(--center-channel-color-rgb), 0.75);
`;

const Notices = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-m);
    margin-bottom: var(--spacing-xl);
`;

const PageBody = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xl);
`;

const TabBar = styled(Tabs)`
    flex-shrink: 0;
`;

const LoadingContainer = styled.div`
    display: flex;
    justify-content: center;
    align-items: center;
    padding: var(--spacing-xxxxl);
`;

// Sticks to the bottom of the scrolling content outlet while there is something to save.
const SaveBar = styled.section`
    position: sticky;
    bottom: 0;
    z-index: 1;
    border-top: var(--border-light);
    background: var(--center-channel-bg);
`;

const SaveBarContent = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-s);
    max-width: ${CONTENT_MAX_WIDTH};
    margin: 0 auto;
    padding: var(--spacing-m) var(--spacing-xxxl);
`;

const SaveBarMessage = styled.div`
    flex: 1;
    min-width: 0;
    color: rgba(var(--center-channel-color-rgb), 0.75);
`;

export default AgentsSettingsPage;
