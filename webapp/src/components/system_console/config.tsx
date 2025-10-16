// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {setUserProfilePictureByUsername} from '@/client';
import {Pill} from '../../components/pill';

import {ServiceData} from './service';
import Panel, {PanelFooterText} from './panel';
import Bots, {firstNewBot} from './bots';
import {LLMBotConfig} from './bot';
import {BooleanItem, ItemList, SelectionItem, SelectionItemOption, TextItem} from './item';
import NoBotsPage from './no_bots_page';
import EmbeddingSearchPanel from './embedding_search/embedding_search_panel';
import {EmbeddingSearchConfig} from './embedding_search/types';
import MCPServers, {MCPConfig} from './mcp_servers';

type Config = {
    services: ServiceData[],
    bots: LLMBotConfig[],
    defaultBotName: string,
    transcriptBackend: string,
    enableLLMTrace: boolean,
    enableTokenUsageLogging: boolean,
    enableCallSummary: boolean,
    allowedUpstreamHostnames: string,
    allowUnsafeLinks: boolean,
    embeddingSearchConfig: EmbeddingSearchConfig,
    mcp: MCPConfig
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

    const addFirstBot = () => {
        const id = Math.random().toString(36).substring(2, 22);
        props.onChange(props.id, {
            ...value,
            bots: [{
                ...firstNewBot,
                id,
            }],
        });
    };

    if (!props.value?.bots || props.value.bots.length === 0) {
        return (
            <ConfigContainer>
                <BetaMessage/>
                <NoBotsPage onAddBotPressed={addFirstBot}/>
            </ConfigContainer>
        );
    }

    // Initialize with default empty config if not provided
    const mcpConfig = value.mcp || defaultConfig.mcp;

    return (
        <ConfigContainer>
            <BetaMessage/>
            <Panel
                title={intl.formatMessage({defaultMessage: 'AI Bots'})}
                subtitle={intl.formatMessage({defaultMessage: 'Multiple AI services can be configured below.'})}
            >
                <Bots
                    bots={props.value.bots ?? []}
                    onChange={(bots: LLMBotConfig[]) => {
                        if (value.bots.findIndex((bot) => bot.name === value.defaultBotName) === -1) {
                            props.onChange(props.id, {...value, bots, defaultBotName: bots[0].name});
                        } else {
                            props.onChange(props.id, {...value, bots});
                        }
                        props.setSaveNeeded();
                    }}
                    botChangedAvatar={botChangedAvatar}
                />
                <PanelFooterText>
                    <FormattedMessage defaultMessage='AI services are third-party services. Mattermost is not responsible for service output.'/>
                </PanelFooterText>
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
