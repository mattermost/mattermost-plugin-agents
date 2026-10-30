// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useId, useRef, useState} from 'react';
import styled from 'styled-components';
import {PlusIcon} from '@mattermost/compass-icons/components';
import {FormattedMessage, useIntl} from 'react-intl';
import {useSelector} from 'react-redux';
import {GlobalState} from '@mattermost/types/store';

import {AdminPanelHeader} from '@mattermost/compass-ui/components/admin-panel-header';
import {Button} from '@mattermost/compass-ui/components/button';
import {Divider} from '@mattermost/compass-ui/components/divider';
import {EmptyState} from '@mattermost/compass-ui/components/empty-state';
import {Icon} from '@mattermost/compass-ui/components/icon';

import {getMCPTools} from '../../client';

import manifest from '@/manifest';

import {useIsLicensedFor} from '@/license';

import {UnderlineTab, UnderlineTabs} from '../underline_tabs';

import {CopyableTextItem} from './copyable_text_item';
import {BuiltInPluginServersSection} from './mcp_builtin_servers_section';
import MCPRemoteServerList from './mcp_remote_server_list';
import MCPServerWizard, {MCPServerWizardMode, PersistMCPServer} from './mcp_server_wizard';
import MCPToolsViewer from './mcp_tools_viewer';
import type {
    MCPConfig as BaseMCPConfig,
    MCPEmbeddedServerConfig as BaseMCPEmbeddedServerConfig,
    MCPServerConfig as BaseMCPServerConfig,
    MCPToolConfig as BaseMCPToolConfig,
    MCPToolsResponse,
} from './mcp_types';

import {LicenseChip} from './enterprise_chip';

import {BooleanItem, ItemList, TextItem} from './item';

export type MCPToolConfig = BaseMCPToolConfig;
export type MCPEmbeddedServerConfig = BaseMCPEmbeddedServerConfig;

export type MCPServerConfig = BaseMCPServerConfig & {

    // Optional: the backend tag has omitempty, so pre-feature servers omit the key entirely.
    serviceAccountHeaders?: {[key: string]: string};
};

export type MCPConfig = Omit<BaseMCPConfig, 'servers'> & {
    servers: MCPServerConfig[] | null;
};

// Like a React state setter: work that finishes asynchronously passes a
// function so it applies to the configuration as it is then, not as it was
// when the work started.
export type MCPConfigUpdate = MCPConfig | ((current: MCPConfig) => MCPConfig);

type Props = {
    mcpConfig: MCPConfig;

    // The servers as last saved, matched by id, for actions that run against
    // the saved configuration.
    savedServers?: MCPServerConfig[];
    onChange: (update: MCPConfigUpdate) => void;

    // onPersistServer saves one server straight away: remote servers are
    // added and edited in the server dialog, which saves as it goes.
    onPersistServer: PersistMCPServer;

    // onDeleteServer deletes one saved server straight away.
    onDeleteServer: (serverID: string) => Promise<void>;
};

const getIdleTimeoutInputValue = (idleTimeoutMinutes?: number): string => {
    if (typeof idleTimeoutMinutes !== 'number' || idleTimeoutMinutes <= 0) {
        return '';
    }

    return idleTimeoutMinutes.toString();
};

// MCP client and embedded server are always enabled; users can still
// disable individual tools but cannot turn off MCP entirely.
// Spread mcpConfig so fields like plugin_servers and embeddedServer.id
// survive rebuilds that only override a subset of keys.
const normalizeMCPConfig = (mcpConfig: MCPConfig): MCPConfig => ({
    ...mcpConfig,
    enabled: true,
    enablePluginServer: mcpConfig?.enablePluginServer ?? false,
    servers: Array.isArray(mcpConfig?.servers) ? mcpConfig.servers : [],
    embeddedServer: {
        ...(mcpConfig?.embeddedServer || {}),
        enabled: true,
    },
    idleTimeoutMinutes: mcpConfig?.idleTimeoutMinutes,
});

// Main component for MCP servers configuration
const MCPServers = ({mcpConfig, savedServers = [], onChange, onPersistServer, onDeleteServer}: Props) => {
    const intl = useIntl();
    const titleId = useId();
    const remoteMcpLicensed = useIsLicensedFor('remote_mcp');
    const [activeTab, setActiveTab] = useState<'config' | 'tools'>('config');
    const [preloadedToolsData, setPreloadedToolsData] = useState<MCPToolsResponse | null>(null);
    const [wizardMode, setWizardMode] = useState<MCPServerWizardMode | null>(null);
    const [idleTimeoutInputValue, setIdleTimeoutInputValue] = useState<string>(() => getIdleTimeoutInputValue(mcpConfig?.idleTimeoutMinutes));
    const normalizedServers = Array.isArray(mcpConfig?.servers) ? mcpConfig.servers : [];

    const configuredSiteURL = useSelector<GlobalState, string | undefined>(
        (state) => state.entities.general.config.SiteURL,
    );
    const normalizedConfiguredSiteURL = configuredSiteURL?.trim();
    const siteURL = normalizedConfiguredSiteURL ? normalizedConfiguredSiteURL.replace(/\/+$/, '') : window.location.origin;
    const oauthCallbackURL = `${siteURL}/plugins/${manifest.id}/oauth/callback`;

    // Tool-affecting config fingerprint (must be declared before prefetch effect)
    const configFingerprint = JSON.stringify({
        servers: normalizedServers.map((s) => ({
            url: s.baseURL,
            enabled: s.enabled,
            clientID: s.clientID || '',
            hasClientSecret: Boolean(s.clientSecret),
        })),
        embeddedEnabled: mcpConfig?.embeddedServer?.enabled,
        enablePluginServer: mcpConfig?.enablePluginServer,
    });
    const prevFingerprintRef = useRef(configFingerprint);

    // Invalidate prefetched tools when tool-affecting config fields change
    useEffect(() => {
        if (prevFingerprintRef.current !== configFingerprint) {
            prevFingerprintRef.current = configFingerprint;
            setPreloadedToolsData(null);
        }
    }, [configFingerprint]);

    // Pre-fetch tools data so they're ready when the Tools tab is clicked.
    // Ignore responses from outdated requests (cleanup + fingerprint match) so config changes cannot apply stale data.
    useEffect(() => {
        const fingerprintAtFetchStart = configFingerprint;
        let cancelled = false;

        getMCPTools().
            then((response) => {
                if (cancelled) {
                    return;
                }
                if (fingerprintAtFetchStart !== prevFingerprintRef.current) {
                    return;
                }
                setPreloadedToolsData(response);
            }).
            catch((error) => {
                if (cancelled) {
                    return;
                }
                // eslint-disable-next-line no-console
                console.error('Failed to preload MCP tools:', error);
            });

        return () => {
            cancelled = true;
        };
    }, [configFingerprint]);

    useEffect(() => {
        setIdleTimeoutInputValue(getIdleTimeoutInputValue(mcpConfig?.idleTimeoutMinutes));
    }, [mcpConfig?.idleTimeoutMinutes]);

    const config = normalizeMCPConfig(mcpConfig);

    const updateConfigLater = (update: (current: MCPConfig) => MCPConfig) => {
        onChange((current) => update(normalizeMCPConfig(current)));
    };

    const pluginServers = (preloadedToolsData?.servers ?? []).filter(
        (server) => server.serverType === 'plugin',
    );

    // New servers are added through the wizard, which saves the server,
    // connects to it and has the admin review its tools.
    const addServer = () => setWizardMode({kind: 'add'});

    // A server deleted before it was ever saved only leaves the page.
    const deleteServer = async (server: MCPServerConfig) => {
        if (server.id) {
            await onDeleteServer(server.id);
            return;
        }
        onChange({...config, servers: normalizedServers.filter((s) => s !== server)});
    };

    return (
        <MCPPanel aria-labelledby={titleId}>
            <AdminPanelHeader
                titleId={titleId}
                title={<FormattedMessage defaultMessage='Model Context Protocol (MCP)'/>}
                subtitle={intl.formatMessage({defaultMessage: 'Configure MCP servers to enable AI tools.'})}
                showDivider={false}
            />
            <TabsContainer role='tablist'>
                <UnderlineTab
                    type='button'
                    role='tab'
                    id='mcp-config-tab'
                    aria-controls='mcp-config-panel'
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
                    id='mcp-tools-tab'
                    aria-controls='mcp-tools-panel'
                    aria-selected={activeTab === 'tools'}
                    tabIndex={activeTab === 'tools' ? 0 : -1}
                    $active={activeTab === 'tools'}
                    onClick={() => setActiveTab('tools')}
                >
                    <FormattedMessage defaultMessage='Tools'/>
                </UnderlineTab>
            </TabsContainer>

            <MCPPanelBody>
                {activeTab === 'config' && (
                    <ConfigTabPanel
                        role='tabpanel'
                        id='mcp-config-panel'
                        aria-labelledby='mcp-config-tab'
                    >
                        <ItemList title={intl.formatMessage({defaultMessage: 'MCP Configuration'})}>
                            <BooleanItem
                                label={intl.formatMessage({defaultMessage: 'Enable Mattermost MCP Server (HTTP)'})}
                                value={config.enablePluginServer}
                                disableTrue={!remoteMcpLicensed}
                                extra={!remoteMcpLicensed && (
                                    <LicenseChip capability='remote_mcp'/>
                                )}
                                onChange={(enablePluginServer) => onChange({...config, enablePluginServer})}
                                helpText={intl.formatMessage({defaultMessage: 'Enable the Mattermost MCP server over HTTP to allow external MCP clients to access Mattermost channels, users, and posts through the MCP protocol. Note: Streaming support requires Mattermost v11.2+.'})}
                            />
                            <TextItem
                                label={intl.formatMessage({defaultMessage: 'Connection Idle Timeout (minutes)'})}
                                value={idleTimeoutInputValue}
                                type='number'
                                onChange={(e) => {
                                    const nextValue = e.target.value;
                                    setIdleTimeoutInputValue(nextValue);

                                    if (nextValue.trim() === '') {
                                        const configWithoutIdleTimeout = {...config};
                                        delete configWithoutIdleTimeout.idleTimeoutMinutes;
                                        onChange({
                                            ...configWithoutIdleTimeout,
                                        });
                                        return;
                                    }

                                    const idleTimeoutMinutes = Number.parseInt(nextValue, 10);
                                    if (Number.isNaN(idleTimeoutMinutes)) {
                                        return;
                                    }

                                    if (idleTimeoutMinutes <= 0) {
                                        const configWithoutIdleTimeout = {...config};
                                        delete configWithoutIdleTimeout.idleTimeoutMinutes;
                                        onChange({
                                            ...configWithoutIdleTimeout,
                                        });
                                        return;
                                    }

                                    onChange({
                                        ...config,
                                        idleTimeoutMinutes,
                                    });
                                }}
                                helptext={intl.formatMessage({defaultMessage: 'How long to keep an inactive user connection open before closing it automatically. Lower values save resources, higher values improve response times. Default: 30 minutes'})}
                            />
                            {(remoteMcpLicensed || normalizedServers.length > 0) && (
                                <CopyableTextItem
                                    label={intl.formatMessage({defaultMessage: 'MCP OAuth Callback URL'})}
                                    value={oauthCallbackURL}
                                    helptext={intl.formatMessage({defaultMessage: 'Register this redirect URI in the remote MCP server\u2019s OAuth application so authorization callbacks return to this Mattermost instance.'})}
                                />
                            )}
                        </ItemList>
                        <SectionDivider/>
                        <BuiltInPluginServersSection
                            embeddedServerId={config.embeddedServer.id}
                            pluginServers={pluginServers}
                        />
                        <SectionDivider/>
                        <RemoteServersSection>
                            {!Array.isArray(normalizedServers) || normalizedServers.length < 1 ? (
                                <>
                                    <RemoteServersEmptyState
                                        title={<FormattedMessage defaultMessage='No remote MCP servers configured'/>}
                                        description={<FormattedMessage defaultMessage='Add a server to connect to external MCP tools.'/>}
                                        action={{
                                            emphasis: 'primary',
                                            onClick: addServer,
                                            disabled: !remoteMcpLicensed,
                                            leadingIcon: <Icon glyph={<PlusIcon/>}/>,
                                            children: <FormattedMessage defaultMessage='Add Remote MCP Server'/>,
                                        }}
                                    />
                                    {!remoteMcpLicensed && (
                                        <AddServerContainer>
                                            <LicenseChip capability='remote_mcp'/>
                                        </AddServerContainer>
                                    )}
                                </>
                            ) : (
                                <>
                                    <MCPRemoteServerList
                                        servers={normalizedServers}
                                        toolsData={preloadedToolsData}
                                        onEdit={(server) => setWizardMode({kind: 'edit', server})}
                                        onDelete={deleteServer}
                                    />
                                    <AddServerContainer>
                                        <Button
                                            emphasis='primary'
                                            onClick={addServer}
                                            disabled={!remoteMcpLicensed}
                                            leadingIcon={<Icon glyph={<PlusIcon/>}/>}
                                        >
                                            <FormattedMessage defaultMessage='Add Remote MCP Server'/>
                                        </Button>
                                        {!remoteMcpLicensed && (
                                            <LicenseChip capability='remote_mcp'/>
                                        )}
                                    </AddServerContainer>
                                </>
                            )}
                        </RemoteServersSection>
                    </ConfigTabPanel>
                )}

                {activeTab === 'tools' && (
                    <div
                        role='tabpanel'
                        id='mcp-tools-panel'
                        aria-labelledby='mcp-tools-tab'
                    >
                        <MCPToolsViewer
                            mcpConfig={config}
                            onConfigChange={(update) => (typeof update === 'function' ? updateConfigLater(update) : onChange(update))}
                            initialToolsData={preloadedToolsData}
                            onReviewServer={(server) => setWizardMode({kind: 'review', server})}
                        />
                    </div>
                )}
                {wizardMode && (
                    <MCPServerWizard
                        mode={wizardMode}
                        existingNames={[...new Set([...normalizedServers, ...savedServers].map((server) => server.name))]}
                        onPersist={onPersistServer}
                        onClose={() => setWizardMode(null)}
                    />
                )}
            </MCPPanelBody>
        </MCPPanel>
    );
};

// Styled components

const RemoteServersSection = styled.div`
	display: flex;
	flex-direction: column;
	gap: var(--spacing-l);
`;

const RemoteServersEmptyState = styled(EmptyState)`
	/* EmptyState title defaults to Heading 400; scale down for this compact section. */
	h2 {
		font-size: var(--font-size-200);
		line-height: var(--line-height-200);
	}

	[class*='empty-state__titles'] {
		gap: var(--spacing-xxxs, 4px);
	}
`;

const SectionDivider = styled(Divider)`
	padding: 0;
`;

const AddServerContainer = styled.div`
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: var(--spacing-m);
`;

/* Mirrors Compass AdminPanel shell so tabs can sit between header and body. */
const MCPPanel = styled.section`
	display: flex;
	flex-direction: column;
	width: 100%;
	max-width: 920px;
	background: var(--center-channel-bg);
	border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
	border-radius: var(--radius-m);
	box-shadow: var(--elevation-1);
	overflow: hidden;

	[class*='admin-panel-header__main'] {
		padding-bottom: calc(var(--spacing-xxl) - var(--spacing-l));
	}
`;

const TabsContainer = styled(UnderlineTabs)`
	padding: 0 var(--spacing-xxl);
`;

const MCPPanelBody = styled.div`
	flex: 1 1 auto;
	width: 100%;
	padding: var(--spacing-xxl);
	box-sizing: border-box;
`;

const ConfigTabPanel = styled.div`
	display: flex;
	flex-direction: column;
	gap: var(--spacing-m);
`;

export default MCPServers;
