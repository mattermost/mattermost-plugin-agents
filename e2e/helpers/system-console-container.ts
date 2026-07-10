import fs from 'fs';
import path from 'path';
import MattermostContainer from './mmcontainer';

/**
 * Container setup for System Console tests
 * Uses mock configurations (no real API keys)
 */

export type SystemConsoleTelemetryOutput = 'off' | 'logs' | 'otlp' | '';

export interface SystemConsoleWebSearchConfig {
    enabled: boolean;
    provider: 'google' | 'brave';
    google: {
        apiKey: string;
        searchEngineId: string;
        resultLimit: number;
        apiURL: string;
    };
    brave: {
        apiKey: string;
        resultLimit: number;
        apiURL: string;
        pollTimeout?: number;
        pollInterval?: number;
    };
    domainDenylist: string[] | null;
}

export interface SystemConsoleEmbeddingSearchConfig {
    type: string;
    vectorStore: {
        type: string;
        parameters: Record<string, unknown> | null;
    };
    embeddingProvider: {
        type: string;
        parameters: Record<string, unknown> | null;
    };
    parameters: Record<string, unknown> | null;
    dimensions: number;
    chunkingOptions: {
        chunkSize: number;
        chunkOverlap: number;
        chunkingStrategy: 'sentences' | 'paragraphs' | 'fixed';
    };
}

export interface SystemConsolePluginConfig {
    allowPrivateChannels?: boolean;
    disableFunctionCalls?: boolean;
    enableUserRestrictions?: boolean;
    enableVectorIndex?: boolean;
    enableTokenUsageLogging?: boolean;
    enableChannelMentionToolCalling?: boolean;
    allowNativeWebSearchInChannels?: boolean;
    telemetryOutput?: SystemConsoleTelemetryOutput;
    openTelemetryEndpoint?: string;
    defaultBotName?: string;
    allowedUpstreamHostnames?: string;
    allowUnsafeLinks?: boolean;
    embeddingSearchConfig?: SystemConsoleEmbeddingSearchConfig;
    webSearch?: SystemConsoleWebSearchConfig;
    services?: any[];
    bots?: any[];
    mcp?: {
        enabled?: boolean;
        enablePluginServer?: boolean;
        idleTimeoutMinutes?: number;
        servers?: MCPServerConfig[] | null;
        embeddedServer?: {
            enabled?: boolean;
            tool_configs?: Array<{ name?: string; policy?: string; enabled?: boolean }>;
        };
    };
}

export interface MCPServerConfig {
    name?: string;
    enabled?: boolean;
    baseURL?: string;
    headers?: Record<string, string>;
    clientID?: string;
    clientSecret?: string;
    tool_configs?: Array<{ name?: string; policy?: string; enabled?: boolean }>;
}

const adminUsername = 'sysadmin';
const adminPassword = 'Sys@dmin-sample1';

/**
 * Find and return the plugin tar.gz file path
 */
function findPluginFile(): string {
    const distPath = path.join(__dirname, '..', '..', 'dist');
    let filename = "";
    fs.readdirSync(distPath).forEach(file => {
        if (file.endsWith(".tar.gz")) {
            filename = path.join(distPath, file);
        }
    });
    if (filename === "") {
        throw new Error("No tar.gz file found in dist folder");
    }
    return filename;
}

/**
 * Setup admin user with standard preferences
 */
async function setupAdminUser(mattermost: MattermostContainer): Promise<void> {
    // Create sysadmin user
    await mattermost.createAdmin('sysadmin@example.com', adminUsername, adminPassword);
    await mattermost.addUserToTeam(adminUsername, 'test');

    // Set up preferences for sysadmin
    const adminClient = await mattermost.getClient(adminUsername, adminPassword);
    const admin = await adminClient.getMe();
    await adminClient.savePreferences(admin.id, [
        { user_id: admin.id, category: 'tutorial_step', name: admin.id, value: '999' },
        { user_id: admin.id, category: 'onboarding_task_list', name: 'onboarding_task_list_show', value: 'false' },
        { user_id: admin.id, category: 'onboarding_task_list', name: 'onboarding_task_list_open', value: 'false' },
        {
            user_id: admin.id,
            category: 'drafts',
            name: 'drafts_tour_tip_showed',
            value: JSON.stringify({ drafts_tour_tip_showed: true }),
        },
        { user_id: admin.id, category: 'crt_thread_pane_step', name: admin.id, value: '999' },
    ]);
}

/**
 * Run a Mattermost container configured for System Console tests
 * @param config Plugin configuration (services, bots, etc.)
 * @returns Configured MattermostContainer
 */
export async function RunSystemConsoleContainer(config: SystemConsolePluginConfig): Promise<MattermostContainer> {
    const filename = findPluginFile();
    const mcpServers = config.mcp?.servers === undefined ? [] : config.mcp.servers;

    const pluginConfig: Record<string, any> = {
        config: {
            allowPrivateChannels: config.allowPrivateChannels ?? true,
            disableFunctionCalls: config.disableFunctionCalls ?? false,
            enableUserRestrictions: config.enableUserRestrictions ?? false,
            enableVectorIndex: config.enableVectorIndex ?? false,
            enableTokenUsageLogging: config.enableTokenUsageLogging,
            enableChannelMentionToolCalling: config.enableChannelMentionToolCalling ?? false,
            defaultBotName: config.defaultBotName,
            allowedUpstreamHostnames: config.allowedUpstreamHostnames,
            allowUnsafeLinks: config.allowUnsafeLinks,
            services: config.services ?? [],
            bots: config.bots ?? [],
            mcp: {
                enabled: config.mcp?.enabled ?? false,
                enablePluginServer: config.mcp?.enablePluginServer ?? false,
                idleTimeoutMinutes: config.mcp?.idleTimeoutMinutes ?? 30,
                servers: mcpServers,
                embeddedServer: {
                    enabled: config.mcp?.embeddedServer?.enabled ?? true,
                },
            },
        }
    };

    if (config.mcp) {
        pluginConfig.config.mcp = config.mcp;
    }

    if (config.embeddingSearchConfig !== undefined) {
        pluginConfig.config.embeddingSearchConfig = config.embeddingSearchConfig;
    }

    if (config.webSearch !== undefined) {
        pluginConfig.config.webSearch = config.webSearch;
    }

    if (config.allowNativeWebSearchInChannels !== undefined) {
        pluginConfig.config.allowNativeWebSearchInChannels = config.allowNativeWebSearchInChannels;
    }

    if (config.telemetryOutput !== undefined) {
        pluginConfig.config.telemetryOutput = config.telemetryOutput;
    }

    if (config.openTelemetryEndpoint !== undefined) {
        pluginConfig.config.openTelemetryEndpoint = config.openTelemetryEndpoint;
    }

    const mattermost = await new MattermostContainer()
        .withEnv('MM_SERVICESETTINGS_ALLOWEDUNTRUSTEDINTERNALCONNECTIONS', 'openai')
        .withPlugin(filename, 'mattermost-ai', pluginConfig)
        .start();

    await setupAdminUser(mattermost);
    await mattermost.grantSelfServiceAgentPermissions();

    return mattermost;
}

export { adminUsername, adminPassword };
export default RunSystemConsoleContainer;
