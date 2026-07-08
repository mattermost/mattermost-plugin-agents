// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {LLMBotConfig} from './bot';
import {ChannelJoinSummaryConfig} from './channel_join_summary/channel_join_summary_panel';
import {EmbeddingSearchConfig} from './embedding_search/types';
import {MCPConfig} from './mcp_servers';
import {LLMService} from './service';
import {WebSearchConfig as WebSearchSettings} from './web_search/web_search_panel';

// services/bots: server sends nil Go slices as JSON null.
export type PluginConfig = {
    services: LLMService[] | null,
    bots: LLMBotConfig[] | null,
    defaultBotName: string,
    transcriptBackend: string,
    telemetryOutput: 'off' | 'logs' | 'otlp' | '',
    openTelemetryEndpoint: string,
    enableTokenUsageLogging: boolean,
    enableCallSummary: boolean,
    allowedUpstreamHostnames: string,
    allowUnsafeLinks: boolean,
    enableChannelMentionToolCalling: boolean,
    allowNativeWebSearchInChannels: boolean,
    embeddingSearchConfig: EmbeddingSearchConfig,
    channelJoinSummary: ChannelJoinSummaryConfig,
    mcp: MCPConfig,
    webSearch: WebSearchSettings,
}
