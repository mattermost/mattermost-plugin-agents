// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {getPluginConfig, savePluginConfig, setUserProfilePictureByUsername} from '@/client';

import {Pill} from '../pill';

import Panel, {PanelFooterText} from './panel';
import Bots, {firstNewBot} from './bots';
import {LLMBotConfig} from './bot';
import Services, {firstNewService} from './services';
import {LLMService} from './service';
import {BooleanItem, ItemList, SelectionItem, SelectionItemOption, TextItem} from './item';
import NoBotsPage from './no_bots_page';
import NoServicesPage from './no_services_page';
import EmbeddingSearchPanel from './embedding_search/embedding_search_panel';
import MCPServers from './mcp_servers';
import {PluginConfig} from './plugin_config_types';
import WebSearchPanel from './web_search/web_search_panel';

type Config = PluginConfig;

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
        enabled: false,
        enablePluginServer: false,
        servers: [],
        embeddedServer: {
            enabled: false,
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
                            href='http://github.com/mattermost/mattermost-plugin-ai/issues'
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
    const [avatarUpdates, setAvatarUpdates] = useState<{ [key: string]: File }>({});
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

    // Register save action that PUTs config to plugin API
    useEffect(() => {
        const save = async () => {
            try {
                await savePluginConfig(localConfig);
                Object.keys(avatarUpdates).forEach((username: string) => setUserProfilePictureByUsername(username, avatarUpdates[username]));
                return {};
            } catch (e: any) {
                return {error: {message: intl.formatMessage({defaultMessage: 'Failed to save configuration.'})}};
            }
        };
        props.registerSaveAction(save);
        return () => {
            props.unRegisterSaveAction(save);
        };
    }, [localConfig, avatarUpdates, intl, props.registerSaveAction, props.unRegisterSaveAction]);

    const updateConfig = useCallback((updates: Partial<Config>) => {
        setLocalConfig((prev) => ({...prev, ...updates}));
        props.setSaveNeeded();
    }, [props.setSaveNeeded]);

    const botChangedAvatar = (bot: LLMBotConfig, image: File) => {
        setAvatarUpdates((prev: { [key: string]: File }) => ({...prev, [bot.name]: image}));
        props.setSaveNeeded();
    };

    const addFirstService = () => {
        const id = crypto.randomUUID();
        updateConfig({
            services: [{
                ...firstNewService,
                id,
            }],
        });
    };

    const addFirstBot = () => {
        const id = crypto.randomUUID();
        updateConfig({
            bots: [{
                ...firstNewBot,
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
    const hasBotConfigured = value.bots && value.bots.length > 0;

    if (!hasServiceConfigured) {
        return (
            <ConfigContainer>
                <BetaMessage/>
                <NoServicesPage onAddServicePressed={addFirstService}/>
            </ConfigContainer>
        );
    }

    if (!hasBotConfigured) {
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
                </Panel>
                <Panel
                    title={intl.formatMessage({defaultMessage: 'AI Bots'})}
                    subtitle={intl.formatMessage({defaultMessage: 'Add your first AI bot to get started.'})}
                >
                    <NoBotsPage onAddBotPressed={addFirstBot}/>
                </Panel>
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
                subtitle={intl.formatMessage({defaultMessage: 'Configure multiple AI bots with different personalities and capabilities.'})}
            >
                <Bots
                    bots={value.bots ?? []}
                    services={value.services ?? []}
                    onChange={(bots: LLMBotConfig[]) => {
                        if (value.bots.findIndex((bot) => bot.name === value.defaultBotName) === -1) {
                            const newDefaultBotName = bots.length > 0 ? bots[0].name : '';
                            updateConfig({bots, defaultBotName: newDefaultBotName});
                        } else {
                            updateConfig({bots});
                        }
                    }}
                    botChangedAvatar={botChangedAvatar}
                />
            </Panel>
            <Panel
                title={intl.formatMessage({defaultMessage: 'AI Functions'})}
                subtitle={intl.formatMessage({defaultMessage: 'Choose a default bot.'})}
            >
                <ItemList>
                    <SelectionItem
                        label={intl.formatMessage({defaultMessage: 'Default bot'})}
                        value={value.defaultBotName}
                        onChange={(e) => {
                            updateConfig({defaultBotName: e.target.value});
                        }}
                    >
                        {value.bots.map((bot: LLMBotConfig) => (
                            <SelectionItemOption
                                key={bot.name}
                                value={bot.name}
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
