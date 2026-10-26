// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {LLMBotConfig} from './bot';
import {EmbeddingSearchConfig} from './embedding_search/types';
import {MCPConfig} from './mcp_types';
import {LLMService} from './service';
import {WebSearchConfig as WebSearchSettings} from './web_search/web_search_panel';

// services/bots: server sends nil Go slices as JSON null.
export type PluginConfig = {
    services: LLMService[] | null,
    bots: LLMBotConfig[] | null,

    // Agents are referenced by immutable bot user ID. The legacy username
    // fields are only read by the server when the matching ID is empty.
    defaultBotUserID: string,
    transcriptBotUserID: string,
    defaultBotName: string,
    transcriptBackend: string,
    telemetryOutput: 'off' | 'logs' | 'otlp' | '',
    openTelemetryEndpoint: string,
    openTelemetryTLS?: boolean,
    openTelemetryHeaders?: string,
    enableTokenUsageLogging: boolean,
    enableCallSummary: boolean,
    allowedUpstreamHostnames: string,
    allowUnsafeLinks: boolean,
    enableHTMLArtifacts?: boolean,
    enableChannelMentionToolCalling: boolean,
    allowNativeWebSearchInChannels: boolean,
    embeddingSearchConfig: EmbeddingSearchConfig,
    mcp: MCPConfig,
    webSearch: WebSearchSettings,

    // Versions of each agent's history to keep; 0 keeps every version.
    agentVersionsToKeep?: number,
}
