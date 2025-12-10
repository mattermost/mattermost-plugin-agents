// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {setUserProfilePictureByUsername} from '@/client';
import {Pill} from '../../components/pill';

import Panel, {PanelFooterText} from './panel';
import Bots, {firstNewBot} from './bots';
import {LLMBotConfig} from './bot';
import Services, {firstNewService} from './services';
import {LLMService} from './service';
import {BooleanItem, ItemList, SelectionItem, SelectionItemOption, TextItem} from './item';
import NoBotsPage from './no_bots_page';
import NoServicesPage from './no_services_page';
import EmbeddingSearchPanel from './embedding_search/embedding_search_panel';
import {EmbeddingSearchConfig} from './embedding_search/types';
import MCPServers, {MCPConfig} from './mcp_servers';

type Config = {
    services: LLMService[],
    bots: LLMBotConfig[],
    defaultBotName: string,
    transcriptBackend: string,
    enableLLMTrace: boolean,
    enableTokenUsageLogging: boolean,
    enableCallSummary: boolean,
    allowedUpstreamHostnames: string,
    allowUnsafeLinks: boolean,
    embeddingSearchConfig: EmbeddingSearchConfig,
    mcp: MCPConfig,
    commonDataSources?: any
}

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

const defaultConfig = {
    services: [],
    llmBackend: '',
    transcriptBackend: '',
    enableLLMTrace: false,
    enableTokenUsageLogging: false,
    allowUnsafeLinks: false,
    embeddingSearchConfig: {
        type: 'disabled',
        vectorStore: {
            type: '',
            parameters: {},
        },
        embeddingProvider: {
            type: '',
            parameters: {},
        },
        parameters: {},
        chunkingOptions: {
            chunkSize: 1000,
            chunkOverlap: 200,
            minChunkSize: 0.75,
            chunkingStrategy: 'sentences',
        },
    },
    mcp: {
        enabled: false,
        servers: {},
        idleTimeout: 30,
    },
    commonDataSources: {
        sources: [
            {
                name: 'mattermost_docs',
                enabled: true,
                protocol: 'http',
                endpoints: {
                    base_url: 'https://docs.mattermost.com',
                    admin: '/guides/administration.html',
                    developer: '/deployment-guide/deployment-guide-index.html',
                    api: '/administration/changelog.html',
                    mobile: '/product-overview/mobile.html',
                },
                auth: {type: 'token', key: ''},
                sections: ['admin', 'developer', 'api', 'mobile'],
                max_docs: 5,
                rate_limit: {
                    requests_per_minute: 30,
                    burst_size: 5,
                    enabled: true,
                },
            },
            {
                name: 'github_repos',
                enabled: true,
                protocol: 'github_api',
                endpoints: {
                    owner: 'mattermost',
                    repos: 'mattermost-server,mattermost-webapp',
                },
                auth: {type: 'token', key: ''},
                sections: ['issues', 'releases', 'pulls'],
                max_docs: 10,
                rate_limit: {
                    requests_per_minute: 60,
                    burst_size: 10,
                    enabled: true,
                },
            },
            {
                name: 'community_forum',
                enabled: true,
                protocol: 'mattermost',
                endpoints: {
                    base_url: 'https://community.mattermost.com',
                },
                auth: {type: 'none'},
                sections: ['feature-requests', 'troubleshooting', 'general'],
                max_docs: 5,
                rate_limit: {
                    requests_per_minute: 20,
                    burst_size: 3,
                    enabled: true,
                },
            },
            {
                name: 'mattermost_hub',
                enabled: true,
                protocol: 'mattermost',
                endpoints: {
                    base_url: 'https://hub.mattermost.com',
                },
                auth: {type: 'token', key: ''},
                sections: ['contact-sales', 'customer-feedback'],
                max_docs: 5,
                rate_limit: {
                    requests_per_minute: 20,
                    burst_size: 3,
                    enabled: true,
                },
            },
            {
                name: 'confluence_docs',
                enabled: true,
                protocol: 'confluence',
                endpoints: {
                    base_url: 'https://mattermost.atlassian.net/wiki',
                    spaces: 'CLOUD,DATAENG,DE,DES,FF,ICU,TW,WD,WKFL',
                    email: '',
                },
                auth: {type: 'api_key', key: ''},
                sections: ['product-requirements', 'market-research', 'feature-specs', 'roadmaps', 'competitive-analysis', 'customer-insights'],
                max_docs: 5,
                rate_limit: {
                    requests_per_minute: 15,
                    burst_size: 3,
                    enabled: true,
                },
            },
            {
                name: 'plugin_marketplace',
                enabled: true,
                protocol: 'http',
                endpoints: {
                    base_url: 'https://integrations.mattermost.com',
                },
                auth: {type: 'none'},
                sections: ['plugins', 'integrations'],
                max_docs: 5,
                rate_limit: {
                    requests_per_minute: 30,
                    burst_size: 5,
                    enabled: true,
                },
            },
            {
                name: 'feature_requests',
                enabled: true,
                protocol: 'uservoice',
                endpoints: {
                    base_url: 'https://mattermost.uservoice.com',
                },
                auth: {type: 'none'},
                sections: ['feature-requests'],
                max_docs: 5,
                rate_limit: {
                    requests_per_minute: 20,
                    burst_size: 3,
                    enabled: true,
                },
            },
        ],
        allowed_domains: ['docs.mattermost.com', 'api.github.com', 'community.mattermost.com', 'hub.mattermost.com', 'mattermost.atlassian.net', 'integrations.mattermost.com', 'mattermost.uservoice.com'],
        github_token: '',
        cache_ttl: '24h',
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
    const value = props.value || defaultConfig;
    const [avatarUpdates, setAvatarUpdates] = useState<{ [key: string]: File }>({});
    const intl = useIntl();

    useEffect(() => {
        const save = async () => {
            Object.keys(avatarUpdates).map((username: string) => setUserProfilePictureByUsername(username, avatarUpdates[username]));
            return {};
        };
        props.registerSaveAction(save);
        return () => {
            props.unRegisterSaveAction(save);
        };
    }, [avatarUpdates]);

    const botChangedAvatar = (bot: LLMBotConfig, image: File) => {
        setAvatarUpdates((prev: { [key: string]: File }) => ({...prev, [bot.name]: image}));
        props.setSaveNeeded();
    };

    const addFirstService = () => {
        const id = crypto.randomUUID();
        props.onChange(props.id, {
            ...value,
            services: [{
                ...firstNewService,
                id,
            }],
        });
    };

    const addFirstBot = () => {
        const id = crypto.randomUUID();
        props.onChange(props.id, {
            ...value,
            bots: [{
                ...firstNewBot,
                id,
            }],
        });
    };

    const hasServiceConfigured = props.value?.services && props.value.services.length > 0;
    const hasBotConfigured = props.value?.bots && props.value.bots.length > 0;

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
                        services={props.value.services ?? []}
                        bots={props.value.bots ?? []}
                        onChange={(services: LLMService[]) => {
                            props.onChange(props.id, {...value, services});
                            props.setSaveNeeded();
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
                    services={props.value.services ?? []}
                    bots={props.value.bots ?? []}
                    onChange={(services: LLMService[]) => {
                        props.onChange(props.id, {...value, services});
                        props.setSaveNeeded();
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
                    bots={props.value.bots ?? []}
                    services={props.value.services ?? []}
                    onChange={(bots: LLMBotConfig[]) => {
                        if (value.bots.findIndex((bot) => bot.name === value.defaultBotName) === -1) {
                            const newDefaultBotName = bots.length > 0 ? bots[0].name : '';
                            props.onChange(props.id, {...value, bots, defaultBotName: newDefaultBotName});
                        } else {
                            props.onChange(props.id, {...value, bots});
                        }
                        props.setSaveNeeded();
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
                            props.onChange(props.id, {...value, defaultBotName: e.target.value});
                            props.setSaveNeeded();
                        }}
                    >
                        {props.value.bots.map((bot: LLMBotConfig) => (
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
                        onChange={(e) => props.onChange(props.id, {...value, allowedUpstreamHostnames: e.target.value})}
                        helptext={intl.formatMessage({defaultMessage: 'Comma separated list of hostnames that LLMs are allowed to contact when using tools. Supports wildcards like *.mydomain.com. For instance to allow JIRA tool use to the Mattermost JIRA instance use mattermost.atlassian.net'})}
                    />
                    <BooleanItem
                        label={<FormattedMessage defaultMessage='Render AI-generated links'/>}
                        value={Boolean(value.allowUnsafeLinks)}
                        onChange={(to) => {
                            props.onChange(props.id, {...value, allowUnsafeLinks: to});
                            props.setSaveNeeded();
                        }}
                        helpText={intl.formatMessage({defaultMessage: 'When enabled, AI responses may contain clickable links, including potentially malicious destinations. Enable only if you trust the LLM output and have mitigations for exfiltration risks.'})}
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
                        onChange={(to) => props.onChange(props.id, {...value, enableLLMTrace: to})}
                        helpText={intl.formatMessage({defaultMessage: 'Enable tracing of LLM requests. Outputs full conversation data to the logs.'})}
                    />
                    <BooleanItem
                        label={intl.formatMessage({defaultMessage: 'Enable Token Usage Logging'})}
                        value={value.enableTokenUsageLogging}
                        onChange={(to) => props.onChange(props.id, {...value, enableTokenUsageLogging: to})}
                        helpText={intl.formatMessage({defaultMessage: 'Enable logging of token usage for all LLM interactions.'})}
                    />
                </ItemList>
            </Panel>
            <EmbeddingSearchPanel
                value={value.embeddingSearchConfig || defaultConfig.embeddingSearchConfig}
                onChange={(config) => {
                    props.onChange(props.id, {...value, embeddingSearchConfig: config});
                    props.setSaveNeeded();
                }}
            />
            <Panel
                title={
                    <Horizontal>
                        <FormattedMessage defaultMessage='Model Context Protocol (MCP)'/>
                        <Pill><FormattedMessage defaultMessage='EXPERIMENTAL'/></Pill>
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
                            servers: config.servers || {},
                        };
                        props.onChange(props.id, {...value, mcp: updatedConfig});
                        props.setSaveNeeded();
                    }}
                />
            </Panel>
        </ConfigContainer>
    );
};
export default Config;
