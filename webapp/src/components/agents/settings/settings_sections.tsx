// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {AdminPanel} from '@mattermost/compass-ui/components/admin-panel';
import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';
import {Tag} from '@mattermost/compass-ui/components/tag';

import {useIsLicensedFor} from '@/license';

import Services from '@/components/system_console/services';
import {LLMService} from '@/components/system_console/service';
import {BooleanItem, ItemList, SelectionItem, TextItem} from '@/components/system_console/item';
import {LicenseChip} from '@/components/system_console/enterprise_chip';
import EmbeddingSearchPanel from '@/components/system_console/embedding_search/embedding_search_panel';
import {HNSW_DEFAULTS, REINDEX_DEFAULTS, REINDEX_INDEX_STRATEGY, VECTOR_ELEMENT_TYPE} from '@/components/system_console/embedding_search/types';
import MCPServers, {MCPServerConfig} from '@/components/system_console/mcp_servers';
import {MCPConfig, PersistMCPServerScope} from '@/components/system_console/mcp_types';
import {PluginConfig} from '@/components/system_console/plugin_config_types';
import WebSearchPanel from '@/components/system_console/web_search/web_search_panel';

import {SettingsSection} from './sections';

/** Minimal fields from GET /ai_bots used for the default-bot dropdown. */
export type RuntimeBotOption = {
    id: string;
    username: string;
    displayName: string;
};

// Resolves an agent reference to a bot ID. The ID wins; the legacy username is
// only consulted when the ID is empty (installs that haven't migrated yet).
const resolveBotID = (id: string | undefined, legacyName: string | undefined, bots: RuntimeBotOption[]) => {
    if (id) {
        return bots.find((bot) => bot.id === id)?.id ?? '';
    }
    if (legacyName) {
        return bots.find((bot) => bot.username === legacyName)?.id ?? '';
    }
    return '';
};

// The server answers with the first bot only when no default is configured; a
// configured default that is unavailable fails requests, so it resolves to ''.
export const effectiveDefaultBotID = (configuredID: string | undefined, legacyName: string | undefined, bots: RuntimeBotOption[]) => {
    if (configuredID || legacyName) {
        return resolveBotID(configuredID, legacyName, bots);
    }
    return bots[0]?.id ?? '';
};

const DEFAULT_AGENT_VERSIONS_TO_KEEP = 50;

export const defaultConfig: PluginConfig = {
    services: [],
    bots: [],
    defaultBotUserID: '',
    transcriptBotUserID: '',
    defaultBotName: '',
    transcriptBackend: '',
    telemetryOutput: 'off',
    openTelemetryEndpoint: '',
    openTelemetryTLS: false,
    openTelemetryHeaders: '',
    enableTokenUsageLogging: false,
    enableCallSummary: false,
    allowedUpstreamHostnames: '',
    allowUnsafeLinks: false,
    allowedLinkDomains: '',
    enableHTMLArtifacts: false,
    enableChannelMentionToolCalling: false,
    enableAmbientAutoReply: false,
    allowNativeWebSearchInChannels: false,
    embeddingSearchConfig: {
        type: '',
        vectorStore: {
            type: '',
            parameters: {},
        },
        embeddingProvider: {
            type: '',
            parameters: {},
        },
        parameters: {},
        dimensions: 0,
        chunkingOptions: {
            chunkSize: 1000,
            chunkOverlap: 200,
            chunkingStrategy: 'sentences',
        },
        reindexWorkers: REINDEX_DEFAULTS.workers,
        reindexBatchSize: REINDEX_DEFAULTS.batchSize,
        reindexIndexStrategy: REINDEX_INDEX_STRATEGY.maintain,
        hnswM: HNSW_DEFAULTS.m,
        vectorElementType: VECTOR_ELEMENT_TYPE.vector,
        indexRetentionDays: 0,
    },
    mcp: {
        enabled: true,
        enablePluginServer: false,
        servers: [],
        embeddedServer: {
            enabled: true,
        },
        idleTimeoutMinutes: 30,
    },
    webSearch: {
        enabled: false,
        provider: 'google',
        domainDenylist: [],
        google: {
            apiKey: '',
            searchEngineId: '',
            resultLimit: 5,
            apiURL: '',
        },
        brave: {
            apiKey: '',
            resultLimit: 5,
            apiURL: '',
        },
        searxng: {
            baseURL: '',
            resultLimit: 5,
        },
    },
};

type Props = {
    section: SettingsSection;
    value: PluginConfig;

    // The configuration as last loaded or saved, for actions that only work
    // against what the server already has.
    savedConfig: PluginConfig;
    runtimeBots: RuntimeBotOption[];
    runtimeBotsError: string | null;

    // The server's rejection of the allowed link domains on the last save.
    linkDomainsError: string;
    updateConfig: (updates: Partial<PluginConfig>) => void;

    // Applies an MCP change to the configuration as it is when the change
    // lands, for work that finishes after other edits.
    updateMCP: (update: MCPConfig | ((current: MCPConfig) => MCPConfig)) => void;

    // Save or delete one MCP server on its own, leaving other unsaved edits unsaved.
    // The blank first service offered on a workspace with none; shown open.
    draftService: LLMService | null;
    persistMCPServer: (server: MCPServerConfig, scope?: PersistMCPServerScope) => Promise<MCPServerConfig>;
    deleteMCPServer: (serverID: string) => Promise<void>;
};

// The panels of one Settings section.
const SettingsSectionContent = ({section, value, savedConfig, runtimeBots, runtimeBotsError, linkDomainsError, updateConfig, updateMCP, persistMCPServer, deleteMCPServer, draftService}: Props) => {
    const intl = useIntl();
    const tokenAccountingLicensed = useIsLicensedFor('token_accounting');
    const providerWebSearchLicensed = useIsLicensedFor('provider_web_search');
    const channelAutoReplyLicensed = useIsLicensedFor('channel_auto_reply');

    switch (section) {
    case 'general': {
        const defaultBotID = effectiveDefaultBotID(value.defaultBotUserID, value.defaultBotName, runtimeBots);

        // The list only holds agents this user can use, so a configured default
        // outside it still has to stay selected: an unrelated save keeps it, and
        // only choosing another agent replaces it.
        const hiddenDefaultID = !defaultBotID && value.defaultBotUserID ? value.defaultBotUserID : '';
        const defaultBotOptions = runtimeBots.map((bot) => ({value: bot.id, label: bot.displayName}));
        if (hiddenDefaultID) {
            defaultBotOptions.push({value: hiddenDefaultID, label: intl.formatMessage({defaultMessage: 'Current default agent (not available to you)'})});
        }
        return (
            <AdminPanel
                title={intl.formatMessage({defaultMessage: 'AI Functions'})}
                subtitle={intl.formatMessage({defaultMessage: 'Default agent and workspace-wide agent behavior.'})}
            >
                <ItemList>
                    {runtimeBotsError && (
                        <SectionNotice
                            type='warning'
                            title={runtimeBotsError}
                        />
                    )}
                    <SelectionItem
                        label={intl.formatMessage({defaultMessage: 'Default bot'})}
                        value={defaultBotID || hiddenDefaultID}
                        error={runtimeBots.length > 0 && !defaultBotID ? intl.formatMessage({defaultMessage: 'Configured default agent is unavailable to you. Requests without a specific agent may fail until you select another.'}) : ''}
                        onChange={(defaultBotUserID) => {
                            updateConfig({defaultBotUserID, defaultBotName: ''});
                        }}
                        options={defaultBotOptions}
                    />
                    <TextItem
                        label={intl.formatMessage({defaultMessage: 'Allowed Upstream Hostnames (csv)'})}
                        value={value.allowedUpstreamHostnames}
                        onChange={(e) => updateConfig({allowedUpstreamHostnames: e.target.value})}
                        helptext={intl.formatMessage({defaultMessage: 'Comma-separated hostnames LLMs can contact via tools (e.g., mattermost.atlassian.net, *.mydomain.com).'})}
                    />
                    <BooleanItem
                        label={<FormattedMessage defaultMessage='Render AI-generated links'/>}
                        value={Boolean(value.allowUnsafeLinks)}
                        onChange={(to) => {
                            updateConfig({allowUnsafeLinks: to});
                        }}
                        helpText={intl.formatMessage({defaultMessage: 'Makes links in AI replies clickable. May expose users to malicious links or data exfiltration.'})}
                    />
                    {value.allowUnsafeLinks && (
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Allowed Link Domains (csv)'})}
                            value={value.allowedLinkDomains}
                            onChange={(e) => updateConfig({allowedLinkDomains: e.target.value})}
                            error={linkDomainsError}
                            helptext={intl.formatMessage({defaultMessage: 'Domains allowed for clickable links; others render as plain text. Supports *.mydomain.com (subdomains only). This server is always allowed; leave empty to allow any.'})}
                            placeholder={'example.com, *.mydomain.com'}
                        />
                    )}
                    <BooleanItem
                        label={
                            <Horizontal>
                                <FormattedMessage defaultMessage='Enable HTML artifacts'/>
                                <Tag
                                    type='info'
                                    label={<FormattedMessage defaultMessage='EXPERIMENTAL'/>}
                                />
                            </Horizontal>
                        }
                        value={Boolean(value.enableHTMLArtifacts)}
                        onChange={(to) => {
                            updateConfig({enableHTMLArtifacts: to});
                        }}
                        helpText={intl.formatMessage({defaultMessage: 'Renders agent-attached HTML files as sandboxed artifacts when a viewer clicks Run. No session access; basic profile requires viewer approval. Full network blocking requires Chromium 152+ (other browsers may leak data via WebRTC), and clipboard contents are untrusted. Site URL must match the address users visit.'})}
                    />
                    <BooleanItem
                        label={
                            <Horizontal>
                                <FormattedMessage defaultMessage='Enable Channel Mention Tool Calling'/>
                                <Tag
                                    type='info'
                                    label={<FormattedMessage defaultMessage='EXPERIMENTAL'/>}
                                />
                            </Horizontal>
                        }
                        value={Boolean(value.enableChannelMentionToolCalling)}
                        onChange={(to) => {
                            updateConfig({enableChannelMentionToolCalling: to});
                        }}
                        helpText={intl.formatMessage({defaultMessage: 'Allows tool calling when @mentioning an agent in channels. When off, tools work only in DMs.'})}
                    />
                    <BooleanItem
                        label={
                            <Horizontal>
                                <FormattedMessage defaultMessage='Enable ambient auto-replies'/>
                                <Tag
                                    type='info'
                                    label={<FormattedMessage defaultMessage='EXPERIMENTAL'/>}
                                />
                            </Horizontal>
                        }
                        value={Boolean(value.enableAmbientAutoReply)}
                        disableTrue={!channelAutoReplyLicensed}
                        extra={!channelAutoReplyLicensed && (
                            <LicenseChip capability='channel_auto_reply'/>
                        )}
                        onChange={(to) => {
                            updateConfig({enableAmbientAutoReply: to});
                        }}
                        helpText={intl.formatMessage({defaultMessage: 'Lets channel managers enable ambient auto-reply; each evaluated message costs an LLM call. Disabling stops ambient replies immediately while @mentions keep working.'})}
                    />
                    <BooleanItem
                        label={<FormattedMessage defaultMessage='Allow native web search in channels'/>}
                        value={Boolean(value.allowNativeWebSearchInChannels)}
                        disableTrue={!providerWebSearchLicensed}
                        extra={!providerWebSearchLicensed && (
                            <LicenseChip capability='provider_web_search'/>
                        )}
                        onChange={(to) => {
                            updateConfig({allowNativeWebSearchInChannels: to});
                        }}
                        helpText={intl.formatMessage({defaultMessage: 'Lets agents use provider-native web search (e.g., Anthropic, OpenAI) in channels, not only DMs. Does not affect custom tools or MCP.'})}
                    />
                    <BooleanItem
                        label={<FormattedMessage defaultMessage='Limit agent version history'/>}
                        value={(value.agentVersionsToKeep ?? 0) > 0}
                        onChange={(to) => updateConfig({agentVersionsToKeep: to ? DEFAULT_AGENT_VERSIONS_TO_KEEP : 0})}
                        helpText={intl.formatMessage({defaultMessage: 'Keep only recent versions per agent and permanently delete older ones. Off keeps all versions.'})}
                    />
                    {(value.agentVersionsToKeep ?? 0) > 0 && (
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Versions to keep per agent'})}
                            type='number'
                            min='1'
                            value={String(value.agentVersionsToKeep)}
                            onChange={(e) => {
                                const parsed = parseInt(e.target.value, 10);
                                updateConfig({agentVersionsToKeep: Number.isNaN(parsed) || parsed < 1 ? 1 : parsed});
                            }}
                            helptext={intl.formatMessage({defaultMessage: 'Includes the current version. Older versions are pruned on save.'})}
                        />
                    )}
                </ItemList>
            </AdminPanel>
        );
    }
    case 'services':
        return (
            <AdminPanel
                title={intl.formatMessage({defaultMessage: 'AI Services'})}
                subtitle={intl.formatMessage({defaultMessage: 'Configure AI services to power your bots.'})}
            >
                <Services
                    services={value.services ?? []}
                    bots={value.bots ?? []}
                    openByDefault={draftService ?? undefined} // eslint-disable-line no-undefined
                    onChange={(services: LLMService[]) => {
                        updateConfig({services});
                    }}
                />
                <PanelFooterText>
                    <FormattedMessage defaultMessage='AI services are third-party services. Mattermost is not responsible for service output.'/>
                </PanelFooterText>
            </AdminPanel>
        );
    case 'mcp':
        return (
            <MCPServers
                mcpConfig={value.mcp || defaultConfig.mcp}
                savedServers={savedConfig.mcp?.servers ?? []}
                onPersistServer={persistMCPServer}
                onDeleteServer={deleteMCPServer}
                onChange={updateMCP}
            />
        );
    case 'search':
        return (
            <EmbeddingSearchPanel
                value={{...defaultConfig.embeddingSearchConfig, ...(value.embeddingSearchConfig || {})}}
                onChange={(config) => updateConfig({embeddingSearchConfig: config})}
            />
        );
    case 'web-search':
        return (
            <WebSearchPanel
                value={value.webSearch || defaultConfig.webSearch}
                onChange={(config) => updateConfig({webSearch: config})}
            />
        );
    case 'debug':
        return (
            <AdminPanel title={intl.formatMessage({defaultMessage: 'Debug'})}>
                <ItemList>
                    <SelectionItem
                        label={intl.formatMessage({defaultMessage: 'Trace Output'})}
                        value={value.telemetryOutput || 'off'}
                        onChange={(telemetryOutput) => updateConfig({telemetryOutput: telemetryOutput as 'off' | 'logs' | 'otlp'})}
                        helptext={intl.formatMessage({defaultMessage: 'Destination for LLM, tool, and search traces. Server Logs writes to the Mattermost log; OTLP Endpoint exports to a collector (e.g., Tempo, Jaeger).'})}
                        options={[
                            {value: 'off', label: intl.formatMessage({defaultMessage: 'Off'})},
                            {value: 'logs', label: intl.formatMessage({defaultMessage: 'Server Logs'})},
                            {value: 'otlp', label: intl.formatMessage({defaultMessage: 'OTLP Endpoint'})},
                        ]}
                    />
                    {value.telemetryOutput === 'otlp' && (
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'OpenTelemetry Endpoint'})}
                            value={value.openTelemetryEndpoint}
                            onChange={(e) => updateConfig({openTelemetryEndpoint: e.target.value})}
                            helptext={intl.formatMessage({defaultMessage: 'OTLP gRPC endpoint (e.g., localhost:4317).'})}
                            placeholder={'localhost:4317'}
                        />
                    )}
                    {value.telemetryOutput === 'otlp' && (
                        <BooleanItem
                            label={intl.formatMessage({defaultMessage: 'Use TLS for OTLP Export'})}
                            value={Boolean(value.openTelemetryTLS)}
                            onChange={(to) => updateConfig({openTelemetryTLS: to})}
                            helpText={intl.formatMessage({defaultMessage: 'Connect over TLS using system root certificates. Required by hosted collectors (e.g., Grafana Cloud Tempo).'})}
                        />
                    )}
                    {value.telemetryOutput === 'otlp' && (
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'OTLP Export Headers'})}
                            type='password'
                            value={value.openTelemetryHeaders || ''}
                            onChange={(e) => updateConfig({openTelemetryHeaders: e.target.value})}
                            helptext={intl.formatMessage({defaultMessage: 'Comma-separated key=value export headers (e.g., Authorization=Basic ...). Use TLS so credentials are not sent in plaintext.'})}
                        />
                    )}
                    <BooleanItem
                        label={intl.formatMessage({defaultMessage: 'Enable Token Usage Logging'})}
                        value={value.enableTokenUsageLogging}
                        disableTrue={!tokenAccountingLicensed}
                        extra={!tokenAccountingLicensed && (
                            <LicenseChip capability='token_accounting'/>
                        )}
                        onChange={(to) => updateConfig({enableTokenUsageLogging: to})}
                        helpText={intl.formatMessage({defaultMessage: 'Log token usage for all LLM requests.'})}
                    />
                </ItemList>
            </AdminPanel>
        );
    default: {
        const exhaustive: never = section;
        throw new Error(`unknown settings section: ${exhaustive}`);
    }
    }
};

const PanelFooterText = styled.div`
    margin-top: var(--spacing-xl);
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: var(--font-size-100);
`;

const Horizontal = styled.div`
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: var(--spacing-xxxs);
`;

export default SettingsSectionContent;
