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
import MCPServers from '@/components/system_console/mcp_servers';
import {MCPConfig} from '@/components/system_console/mcp_types';
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

export const BetaMessage = () => (
    <SectionNotice
        type='info'
        title={(
            <FormattedMessage
                defaultMessage='To report a bug or to provide feedback, <link>create a new issue in the plugin repository</link>.'
                values={{
                    link: (chunks: any) => (
                        <a
                            target={'_blank'}
                            rel={'noopener noreferrer'}
                            href='http://github.com/mattermost/mattermost-plugin-agents/issues'
                        >
                            {chunks}
                        </a>
                    ),
                }}
            />
        )}
    />
);

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
};

// The panels of one Settings section.
const SettingsSectionContent = ({section, value, savedConfig, runtimeBots, runtimeBotsError, linkDomainsError, updateConfig, updateMCP}: Props) => {
    const intl = useIntl();
    const tokenAccountingLicensed = useIsLicensedFor('token_accounting');
    const providerWebSearchLicensed = useIsLicensedFor('provider_web_search');
    const channelAutoReplyLicensed = useIsLicensedFor('channel_auto_reply');

    switch (section) {
    case 'general': {
        const defaultBotID = effectiveDefaultBotID(value.defaultBotUserID, value.defaultBotName, runtimeBots);
        return (
            <AdminPanel
                title={intl.formatMessage({defaultMessage: 'AI Functions'})}
                subtitle={intl.formatMessage({defaultMessage: 'Choose a default agent and how agents behave across the workspace.'})}
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
                        value={defaultBotID}
                        error={runtimeBots.length > 0 && !defaultBotID ? intl.formatMessage({defaultMessage: 'The configured default agent is not available. Requests that do not choose an agent will fail until you select another one.'}) : ''}
                        onChange={(defaultBotUserID) => {
                            updateConfig({defaultBotUserID, defaultBotName: ''});
                        }}
                        options={runtimeBots.map((bot) => ({value: bot.id, label: bot.displayName}))}
                    />
                    <TextItem
                        label={intl.formatMessage({defaultMessage: 'Allowed Upstream Hostnames (csv)'})}
                        value={value.allowedUpstreamHostnames}
                        onChange={(e) => updateConfig({allowedUpstreamHostnames: e.target.value})}
                        helptext={intl.formatMessage({defaultMessage: 'Comma separated list of hostnames that LLMs are allowed to contact when using tools. Supports wildcards like *.mydomain.com. For instance to allow JIRA tool use to the Mattermost JIRA instance use mattermost.atlassian.net'})}
                    />
                    <BooleanItem
                        label={<FormattedMessage defaultMessage='Render AI-generated links'/>}
                        value={Boolean(value.allowUnsafeLinks)}
                        onChange={(to) => {
                            updateConfig({allowUnsafeLinks: to});
                        }}
                        helpText={intl.formatMessage({defaultMessage: 'When enabled, AI responses may contain clickable links, including potentially malicious destinations. Enable only if you trust the LLM output and have mitigations for exfiltration risks.'})}
                    />
                    {value.allowUnsafeLinks && (
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Allowed Link Domains (csv)'})}
                            value={value.allowedLinkDomains}
                            onChange={(e) => updateConfig({allowedLinkDomains: e.target.value})}
                            error={linkDomainsError}
                            helptext={intl.formatMessage({defaultMessage: 'Comma separated list of domains that AI-generated links may point to. Links to any other domain are shown as plain text. Supports wildcards like *.mydomain.com, which matches subdomains but not mydomain.com itself. Links to this Mattermost server are always allowed. Leave empty to allow links to any domain.'})}
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
                        helpText={intl.formatMessage({defaultMessage: 'When enabled, HTML files that agents attach to their replies are rendered as sandboxed interactive artifacts that run only when a viewer clicks Run. Artifacts have no access to the user\'s Mattermost session, and they read the viewer\'s basic profile only after the viewer explicitly allows it. Network access is blocked, fully only in Chromium 152 or later; in other browsers an artifact can still send its content, any profile data the viewer allowed, and anything the viewer types into it to external sites via WebRTC. An artifact can also place content on the clipboard that differs from what it displays, so treat anything copied from an artifact as untrusted. The Site URL must match the address users open Mattermost at, or artifacts fail to load. Enable only where these risks are acceptable.'})}
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
                        helpText={intl.formatMessage({defaultMessage: 'When enabled, @mentioning a bot in public channels allows tool calling (e.g., web search, integrations). When disabled, channel mentions still work but tools are disabled—only DMs allow tool usage. This is an experimental feature for multi-player tool calling in channels.'})}
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
                        helpText={intl.formatMessage({defaultMessage: 'Lets channel managers choose the ambient auto-reply modes, where the agent’s model reads each new message and decides whether the agent should reply. Every analyzed message costs an LLM call. When disabled, ambient modes cannot be saved and channels using them stop replying automatically; @mentions keep working.'})}
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
                        helpText={intl.formatMessage({defaultMessage: 'When enabled, bots with native web search (Anthropic Claude, OpenAI with Responses API) can use their built-in web search capability in public and private channels, not just direct messages. This only affects native provider web search, not custom tools or MCP integrations.'})}
                    />
                    <BooleanItem
                        label={<FormattedMessage defaultMessage='Limit agent version history'/>}
                        value={(value.agentVersionsToKeep ?? 0) > 0}
                        onChange={(to) => updateConfig({agentVersionsToKeep: to ? DEFAULT_AGENT_VERSIONS_TO_KEEP : 0})}
                        helpText={intl.formatMessage({defaultMessage: 'When false, every saved version of each agent is kept. When true, each agent keeps only its most recent versions and older ones are permanently deleted.'})}
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
                            helptext={intl.formatMessage({defaultMessage: 'Includes the current version. Older versions are deleted when this setting is saved and each time an agent is saved.'})}
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
                        helptext={intl.formatMessage({defaultMessage: 'Where to send distributed traces of LLM requests, tool execution, and search operations. "Server Logs" writes spans to the Mattermost server log and requires no extra infrastructure. "OTLP Endpoint" exports spans to a collector such as Grafana Tempo or Jaeger.'})}
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
                            helptext={intl.formatMessage({defaultMessage: 'OTLP gRPC endpoint for trace export (e.g. localhost:4317).'})}
                            placeholder={'localhost:4317'}
                        />
                    )}
                    {value.telemetryOutput === 'otlp' && (
                        <BooleanItem
                            label={intl.formatMessage({defaultMessage: 'Use TLS for OTLP Export'})}
                            value={Boolean(value.openTelemetryTLS)}
                            onChange={(to) => updateConfig({openTelemetryTLS: to})}
                            helpText={intl.formatMessage({defaultMessage: 'Connect to the OTLP endpoint over TLS, verified against the system root certificates. Required by hosted collectors such as Grafana Cloud Tempo.'})}
                        />
                    )}
                    {value.telemetryOutput === 'otlp' && (
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'OTLP Export Headers'})}
                            type='password'
                            value={value.openTelemetryHeaders || ''}
                            onChange={(e) => updateConfig({openTelemetryHeaders: e.target.value})}
                            helptext={intl.formatMessage({defaultMessage: 'Headers sent with every trace export, as comma-separated key=value pairs (e.g. Authorization=Basic YOUR_CREDENTIALS). Use with TLS so credentials are not sent in plaintext.'})}
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
                        helpText={intl.formatMessage({defaultMessage: 'Enable logging of token usage for all LLM interactions.'})}
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
