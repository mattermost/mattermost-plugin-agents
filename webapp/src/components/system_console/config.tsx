// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {getPluginConfig, getAIBots, savePluginConfig} from '@/client';

import {Pill} from '../pill';

import Panel, {PanelFooterText} from './panel';
import Services, {firstNewService} from './services';
import {LLMService} from './service';
import {BooleanItem, ItemList, SelectionItem, SelectionItemOption, TextItem} from './item';
import NoServicesPage from './no_services_page';
import BotsMovedNotice from './bots_moved_notice';
import EmbeddingSearchPanel from './embedding_search/embedding_search_panel';
import MCPServers from './mcp_servers';
import {PluginConfig} from './plugin_config_types';
import WebSearchPanel from './web_search/web_search_panel';

type Config = PluginConfig;

/** Minimal fields from GET /ai_bots used for the default-bot dropdown. */
type RuntimeBotOption = {
    username: string;
    displayName: string;
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

const MessageContainer = styled.div`
	display: flex;
	align-items: center;
	flex-direction: row;
	gap: 5px;
	padding: 10px 12px;
	background: white;
	border-radius: 4px;
	border: 1px solid rgba(63, 67, 80, 0.08);
`;

const ConfigContainer = styled.div`
	display: flex;
	flex-direction: column;
	gap: 20px;
`;

const Horizontal = styled.div`
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: 8px;
`;

const LoadingContainer = styled.div`
    display: flex;
    justify-content: center;
    align-items: center;
    padding: 40px;
`;

const ErrorContainer = styled.div`
    display: flex;
    align-items: center;
    padding: 10px 12px;
    background: #FFF0F0;
    border-radius: 4px;
    border: 1px solid rgba(210, 75, 78, 0.3);
    color: #D24B4E;
`;

const RuntimeBotsErrorBanner = styled.div`
    grid-column: 1 / -1;
    padding: 10px 12px;
    margin-bottom: 4px;
    background: rgba(var(--away-indicator-rgb, 255, 188, 66), 0.12);
    border-radius: 4px;
    border: 1px solid rgba(var(--away-indicator-rgb, 255, 188, 66), 0.35);
    color: rgba(var(--center-channel-color-rgb), 0.88);
    font-size: 14px;
`;

const defaultConfig: Config = {
    services: [],
    bots: [],
    defaultBotName: '',
    transcriptBackend: '',
    enableLLMTrace: false,
    enableTokenUsageLogging: false,
    enableCallSummary: false,
    allowedUpstreamHostnames: '',
    allowUnsafeLinks: false,
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
            minChunkSize: 0.75,
            chunkingStrategy: 'sentences',
        },
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
    },
};

const BetaMessage = () => (
    <MessageContainer>
        <span>
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
        </span>
    </MessageContainer>
);

const Config = (props: Props) => {
    const [localConfig, setLocalConfig] = useState<Config>(defaultConfig);
    const [loading, setLoading] = useState(true);
    const [loadError, setLoadError] = useState<string | null>(null);
    const [runtimeBots, setRuntimeBots] = useState<RuntimeBotOption[]>([]);
    const [runtimeBotsError, setRuntimeBotsError] = useState<string | null>(null);
    const intl = useIntl();

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
                await savePluginConfig(localConfig);
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

    const addFirstService = () => {
        const id = crypto.randomUUID();
        updateConfig({
            services: [{
                ...firstNewService,
                id,
            }],
        });
    };

    if (loading) {
        return (
            <ConfigContainer>
                <LoadingContainer>
                    <FormattedMessage defaultMessage='Loading configuration...'/>
                </LoadingContainer>
            </ConfigContainer>
        );
    }

    if (loadError) {
        return (
            <ConfigContainer>
                <ErrorContainer>{loadError}</ErrorContainer>
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
            <Panel
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
            </Panel>
            <Panel
                title={intl.formatMessage({defaultMessage: 'AI Bots'})}
                subtitle={intl.formatMessage({defaultMessage: 'AI agents are managed from the Agents product page.'})}
            >
                <BotsMovedNotice/>
            </Panel>
            <Panel
                title={intl.formatMessage({defaultMessage: 'AI Functions'})}
                subtitle={intl.formatMessage({defaultMessage: 'Choose a default bot.'})}
            >
                <ItemList>
                    {runtimeBotsError && (
                        <RuntimeBotsErrorBanner>{runtimeBotsError}</RuntimeBotsErrorBanner>
                    )}
                    <SelectionItem
                        label={intl.formatMessage({defaultMessage: 'Default bot'})}
                        value={value.defaultBotName}
                        onChange={(e) => {
                            updateConfig({defaultBotName: e.target.value});
                        }}
                    >
                        {runtimeBots.map((bot) => (
                            <SelectionItemOption
                                key={bot.username}
                                value={bot.username}
                            >
                                {bot.displayName}
                            </SelectionItemOption>
                        ))}
                    </SelectionItem>
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
                        label={
                            <Horizontal>
                                <FormattedMessage defaultMessage='Enable Channel Mention Tool Calling'/>
                                <Pill><FormattedMessage defaultMessage='EXPERIMENTAL'/></Pill>
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
                        onChange={(to) => {
                            updateConfig({allowNativeWebSearchInChannels: to});
                        }}
                        helpText={intl.formatMessage({defaultMessage: 'When enabled, bots with native web search (Anthropic Claude, OpenAI with Responses API) can use their built-in web search capability in public and private channels, not just direct messages. This only affects native provider web search, not custom tools or MCP integrations.'})}
                    />
                </ItemList>
            </Panel>
            <Panel
                title={intl.formatMessage({defaultMessage: 'Debug'})}
                subtitle=''
            >
                <ItemList>
                    <BooleanItem
                        label={intl.formatMessage({defaultMessage: 'Enable LLM Trace'})}
                        value={value.enableLLMTrace}
                        onChange={(to) => updateConfig({enableLLMTrace: to})}
                        helpText={intl.formatMessage({defaultMessage: 'Enable tracing of LLM requests. Outputs full conversation data to the logs.'})}
                    />
                    <BooleanItem
                        label={intl.formatMessage({defaultMessage: 'Enable Token Usage Logging'})}
                        value={value.enableTokenUsageLogging}
                        onChange={(to) => updateConfig({enableTokenUsageLogging: to})}
                        helpText={intl.formatMessage({defaultMessage: 'Enable logging of token usage for all LLM interactions.'})}
                    />
                </ItemList>
            </Panel>
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
            <Panel
                title={
                    <Horizontal>
                        <FormattedMessage defaultMessage='Model Context Protocol (MCP)'/>
                    </Horizontal>
                }
                subtitle={intl.formatMessage({defaultMessage: 'Configure MCP servers to enable AI tools.'})}
            >
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
            </Panel>
        </ConfigContainer>
    );
};
export default Config;
