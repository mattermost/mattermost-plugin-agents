// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {AdminPanel} from '@mattermost/compass-ui/components/admin-panel';
import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';
import {Spinner} from '@mattermost/compass-ui/components/spinner';
import {Tag} from '@mattermost/compass-ui/components/tag';

import {getPluginConfig, getAIBots, savePluginConfig} from '@/client';
import {useIsLicensedFor} from '@/license';

import Services, {firstNewService} from './services';
import {LLMService} from './service';
import {BooleanItem, ItemList, SelectionItem, TextItem} from './item';
import {LicenseChip} from './enterprise_chip';
import NoServicesPage from './no_services_page';
import BotsMovedNotice from './bots_moved_notice';
import EmbeddingSearchPanel from './embedding_search/embedding_search_panel';
import {HNSW_DEFAULTS, REINDEX_DEFAULTS, REINDEX_INDEX_STRATEGY, VECTOR_ELEMENT_TYPE} from './embedding_search/types';
import MCPServers from './mcp_servers';
import {PluginConfig} from './plugin_config_types';
import WebSearchPanel from './web_search/web_search_panel';

type Config = PluginConfig;

/** Minimal fields from GET /ai_bots used for the default-bot dropdown. */
type RuntimeBotOption = {
    username: string;
    displayName: string;
};

// The server answers with the first bot when the configured default is empty or unknown.
export const effectiveDefaultBotName = (configured: string, bots: RuntimeBotOption[]) => {
    if (bots.some((bot) => bot.username === configured)) {
        return configured;
    }
    return bots[0]?.username ?? '';
};

type Props = {
    id: string
    label: string
    helpText: React.ReactNode
    value: Config
    disabled: boolean
    config: any
    currentState: any
    license: any
    setByEnv: boolean
    onChange: (id: string, value: any) => void
    setSaveNeeded: () => void
    registerSaveAction: (action: () => Promise<{ error?: { message?: string } }>) => void
    unRegisterSaveAction: (action: () => Promise<{ error?: { message?: string } }>) => void
}

const ConfigContainer = styled.div`
	display: flex;
	flex-direction: column;
	gap: var(--spacing-xl);
`;

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

const LoadingContainer = styled.div`
    display: flex;
    justify-content: center;
    align-items: center;
    padding: var(--spacing-xxxxl);
`;

const defaultConfig: Config = {
    services: [],
    bots: [],
    defaultBotName: '',
    transcriptBackend: '',
    telemetryOutput: 'off',
    openTelemetryEndpoint: '',
    enableTokenUsageLogging: false,
    enableCallSummary: false,
    allowedUpstreamHostnames: '',
    allowUnsafeLinks: false,
    enableHTMLArtifacts: false,
    enableChannelMentionToolCalling: false,
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

const BetaMessage = () => (
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

const Config = (props: Props) => {
    const [localConfig, setLocalConfig] = useState<Config>(defaultConfig);
    const [loading, setLoading] = useState(true);
    const [loadError, setLoadError] = useState<string | null>(null);
    const [runtimeBots, setRuntimeBots] = useState<RuntimeBotOption[]>([]);
    const [runtimeBotsError, setRuntimeBotsError] = useState<string | null>(null);
    const intl = useIntl();
    const tokenAccountingLicensed = useIsLicensedFor('token_accounting');
    const providerWebSearchLicensed = useIsLicensedFor('provider_web_search');

    // Load config from plugin API on mount
    useEffect(() => {
        const loadConfig = async () => {
            try {
                const cfg = await getPluginConfig();
                setLocalConfig({...defaultConfig, ...cfg});
                setLoadError(null);
            } catch (e: any) {
                setLoadError(intl.formatMessage({defaultMessage: 'Failed to load configuration.'}));
            } finally {
                setLoading(false);
            }
        };
        loadConfig();
    }, [intl]);

    useEffect(() => {
        if (loading || loadError) {
            return;
        }
        const loadRuntimeBots = async () => {
            try {
                const res = await getAIBots();
                setRuntimeBots(res.bots ?? []);
                setRuntimeBotsError(null);
            } catch {
                setRuntimeBotsError(intl.formatMessage({defaultMessage: 'Failed to load the runtime bot list. The previous list is kept.'}));
            }
        };
        loadRuntimeBots();
    }, [loading, loadError]);

    // Register save action that PUTs config to plugin API
    useEffect(() => {
        const save = async () => {
            try {
                const saved = await savePluginConfig(localConfig);

                // Adopt the normalized saved config so server-minted
                // service/MCP IDs (and the UI gated on them) appear
                // immediately instead of after a page reload.
                setLocalConfig({...defaultConfig, ...saved});
                return {};
            } catch (e: any) {
                return {error: {message: intl.formatMessage({defaultMessage: 'Failed to save configuration.'})}};
            }
        };
        props.registerSaveAction(save);
        return () => {
            props.unRegisterSaveAction(save);
        };
    }, [localConfig, intl, props.registerSaveAction, props.unRegisterSaveAction]);

    const updateConfig = useCallback((updates: Partial<Config>) => {
        setLocalConfig((prev) => ({...prev, ...updates}));
        props.setSaveNeeded();
    }, [props.setSaveNeeded]);

    // No id is assigned client-side: the backend mints the stable service ID
    // on save (normalizeAdminConfig).
    const addFirstService = () => {
        updateConfig({
            services: [{...firstNewService}],
        });
    };

    if (loading) {
        return (
            <ConfigContainer>
                <LoadingContainer>
                    <Spinner
                        size='32'
                        aria-label={intl.formatMessage({defaultMessage: 'Loading configuration...'})}
                    />
                </LoadingContainer>
            </ConfigContainer>
        );
    }

    if (loadError) {
        return (
            <ConfigContainer>
                <SectionNotice
                    type='danger'
                    title={loadError}
                />
            </ConfigContainer>
        );
    }

    const value = localConfig;

    const hasServiceConfigured = value.services && value.services.length > 0;

    if (!hasServiceConfigured) {
        return (
            <ConfigContainer>
                <BetaMessage/>
                <NoServicesPage onAddServicePressed={addFirstService}/>
            </ConfigContainer>
        );
    }

    // Initialize with default empty config if not provided
    const mcpConfig = value.mcp || defaultConfig.mcp;

    return (
        <ConfigContainer>
            <BetaMessage/>
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
            <AdminPanel
                title={intl.formatMessage({defaultMessage: 'AI Bots'})}
                subtitle={intl.formatMessage({defaultMessage: 'AI agents are managed from the Agents product page.'})}
            >
                <BotsMovedNotice/>
            </AdminPanel>
            <AdminPanel
                title={intl.formatMessage({defaultMessage: 'AI Functions'})}
                subtitle={intl.formatMessage({defaultMessage: 'Choose a default bot.'})}
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
                        value={effectiveDefaultBotName(value.defaultBotName, runtimeBots)}
                        onChange={(defaultBotName) => {
                            updateConfig({defaultBotName});
                        }}
                        options={runtimeBots.map((bot) => ({value: bot.username, label: bot.displayName}))}
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
                    <BooleanItem
                        label={<FormattedMessage defaultMessage='Enable HTML artifacts'/>}
                        value={Boolean(value.enableHTMLArtifacts)}
                        onChange={(to) => {
                            updateConfig({enableHTMLArtifacts: to});
                        }}
                        helpText={intl.formatMessage({defaultMessage: 'When enabled, HTML files created by agents are rendered as interactive artifacts in the conversation. Artifacts run in an isolated sandbox with no access to the user\'s Mattermost session, and fetch, XHR, WebSocket and external resources are blocked. An artifact may still be able to send its own content (and any profile data the viewer allowed) to external sites via WebRTC or by navigating its own frame. Enable only where that is acceptable. Artifacts can only read the viewer\'s basic profile after the viewer explicitly allows it.'})}
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
                </ItemList>
            </AdminPanel>
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
            <EmbeddingSearchPanel
                value={{...defaultConfig.embeddingSearchConfig, ...(value.embeddingSearchConfig || {})}}
                onChange={(config) => {
                    updateConfig({embeddingSearchConfig: config});
                }}
            />
            <WebSearchPanel
                value={value.webSearch || defaultConfig.webSearch}
                onChange={(config) => {
                    updateConfig({webSearch: config});
                }}
            />
            <MCPServers
                mcpConfig={mcpConfig}
                onChange={(config) => {
                    // Ensure we're creating a valid structure for the server configuration
                    const updatedConfig = {
                        ...config,
                        servers: config.servers || [],
                    };
                    updateConfig({mcp: updatedConfig});
                }}
            />
        </ConfigContainer>
    );
};
export default Config;
