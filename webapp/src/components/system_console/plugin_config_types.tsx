// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {LLMBotConfig} from './bot';
import {EmbeddingSearchConfig} from './embedding_search/types';
import {MCPConfig} from './mcp_servers';
import {LLMService} from './service';
import {WebSearchConfig as WebSearchSettings} from './web_search/web_search_panel';

// Mirror the server's credential mask (config.SecretPlaceholder in config/secrets.go).
// A credential that is already stored arrives as this value and is sent back
// unchanged unless the admin edits it.
export const SECRET_PLACEHOLDER = '********************************';

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
    mcp: MCPConfig,
    webSearch: WebSearchSettings,
}
