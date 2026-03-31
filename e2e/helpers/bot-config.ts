import { Client4 } from '@mattermost/client';
import MattermostContainer from './mmcontainer';
import {
    mattermostAIAdminConfigApiFromClient,
    normalizeMattermostAiConfigFromApi,
    type PluginAdminConfigApi,
} from './plugin-http';

export interface BotConfig {
    id: string;
    name: string;
    displayName: string;
    customInstructions: string;
    serviceID: string;
    enableVision?: boolean;
    disableTools?: boolean;
    reasoningEnabled?: boolean;
    reasoningEffort?: string;
    thinkingBudget?: number;
}

export interface ServiceConfig {
    id: string;
    name: string;
    type: string;
    apiKey: string;
    apiURL: string;
    defaultModel?: string;
    tokenLimit?: number;
    streamingTimeoutSeconds?: number;
    useResponsesAPI?: boolean;
}

export interface MCPEmbeddedServerConfig {
    enabled?: boolean;
    tool_configs?: Array<{ name: string; policy: string; enabled: boolean }>;
}

export interface PluginMCPConfig {
    enabled?: boolean;
    enablePluginServer?: boolean;
    idleTimeoutMinutes?: number;
    servers?: unknown[];
    embeddedServer?: MCPEmbeddedServerConfig;
}

export interface PluginConfig {
    config: {
        allowPrivateChannels?: boolean;
        disableFunctionCalls?: boolean;
        enableLLMTrace?: boolean;
        enableUserRestrictions?: boolean;
        defaultBotName?: string;
        enableVectorIndex?: boolean;
        services: ServiceConfig[];
        bots: BotConfig[];
        mcp?: PluginMCPConfig;
    };
}

export class BotConfigHelper {
    private adminApi: PluginAdminConfigApi;

    constructor(client: Client4, baseUrl: string) {
        this.adminApi = mattermostAIAdminConfigApiFromClient(client, baseUrl);
    }

    /**
     * Get the current plugin configuration via the plugin's admin config API.
     * Configuration is stored in the plugin database when using database-config.
     */
    async getPluginConfig(): Promise<PluginConfig> {
        const apiConfig = await this.adminApi.get();
        const config = normalizeMattermostAiConfigFromApi(apiConfig);
        // API returns config.Config (flat); helper expects { config: {...} }
        return { config } as PluginConfig;
    }

    /**
     * Update the plugin configuration via the plugin's admin config API.
     */
    async updatePluginConfig(config: PluginConfig): Promise<void> {
        await this.adminApi.put(config.config as Record<string, unknown>);
    }

    /**
     * Get a specific bot configuration by ID
     */
    async getBot(botId: string): Promise<BotConfig | undefined> {
        const config = await this.getPluginConfig();
        return config.config.bots.find(bot => bot.id === botId);
    }

    /**
     * Get a bot by name
     */
    async getBotByName(botName: string): Promise<BotConfig | undefined> {
        const config = await this.getPluginConfig();
        return config.config.bots.find(bot => bot.name === botName);
    }

    /**
     * Update a bot configuration
     */
    async updateBot(botId: string, updates: Partial<BotConfig>): Promise<void> {
        const config = await this.getPluginConfig();
        const botIndex = config.config.bots.findIndex(bot => bot.id === botId);

        if (botIndex === -1) {
            throw new Error(`Bot with ID ${botId} not found`);
        }

        config.config.bots[botIndex] = {
            ...config.config.bots[botIndex],
            ...updates,
        };

        await this.updatePluginConfig(config);
    }

    /**
     * Add a new bot
     */
    async addBot(bot: BotConfig): Promise<void> {
        const config = await this.getPluginConfig();
        config.config.bots.push(bot);
        await this.updatePluginConfig(config);
    }

    /**
     * Delete a bot
     */
    async deleteBot(botId: string): Promise<void> {
        const config = await this.getPluginConfig();
        config.config.bots = config.config.bots.filter(bot => bot.id !== botId);
        await this.updatePluginConfig(config);
    }

    /**
     * Get a specific service configuration by ID
     */
    async getService(serviceId: string): Promise<ServiceConfig | undefined> {
        const config = await this.getPluginConfig();
        return config.config.services.find(service => service.id === serviceId);
    }

    /**
     * Update a service configuration
     */
    async updateService(serviceId: string, updates: Partial<ServiceConfig>): Promise<void> {
        const config = await this.getPluginConfig();
        const serviceIndex = config.config.services.findIndex(service => service.id === serviceId);

        if (serviceIndex === -1) {
            throw new Error(`Service with ID ${serviceId} not found`);
        }

        config.config.services[serviceIndex] = {
            ...config.config.services[serviceIndex],
            ...updates,
        };

        await this.updatePluginConfig(config);
    }

    /**
     * Add a new service
     */
    async addService(service: ServiceConfig): Promise<void> {
        const config = await this.getPluginConfig();
        config.config.services.push(service);
        await this.updatePluginConfig(config);
    }

    /**
     * Delete a service
     */
    async deleteService(serviceId: string): Promise<void> {
        const config = await this.getPluginConfig();
        config.config.services = config.config.services.filter(service => service.id !== serviceId);
        await this.updatePluginConfig(config);
    }

    /**
     * Verify bot configuration in database.
     * Config is stored in Agents_ConfigHistory when using database-config.
     */
    async verifyBotInDatabase(mattermost: MattermostContainer, botId: string): Promise<boolean> {
        let db;
        try {
            db = await mattermost.db();
            const result = await db.query(
                `SELECT Config FROM Agents_ConfigHistory WHERE Active = true LIMIT 1`
            );

            if (result.rows.length === 0) {
                return false;
            }

            const config = JSON.parse(result.rows[0].config);
            const bot = config.bots?.find((b: BotConfig) => b.id === botId);

            return !!bot;
        } catch (error) {
            throw error;
        } finally {
            if (db) {
                await db.end();
            }
        }
    }

    /**
     * Get bot configuration from database.
     * Config is stored in Agents_ConfigHistory when using database-config.
     */
    async getBotFromDatabase(mattermost: MattermostContainer, botId: string): Promise<BotConfig | null> {
        let db;
        try {
            db = await mattermost.db();
            const result = await db.query(
                `SELECT Config FROM Agents_ConfigHistory WHERE Active = true LIMIT 1`
            );

            if (result.rows.length === 0) {
                return null;
            }

            const config = JSON.parse(result.rows[0].config);
            const bot = config.bots?.find((b: BotConfig) => b.id === botId);

            return bot || null;
        } catch (error) {
            throw error;
        } finally {
            if (db) {
                await db.end();
            }
        }
    }
}

/**
 * Create a bot config helper from a Mattermost container
 */
export async function createBotConfigHelper(mattermost: MattermostContainer): Promise<BotConfigHelper> {
    const adminClient = await mattermost.getAdminClient();
    return new BotConfigHelper(adminClient, mattermost.url());
}

/**
 * Generate a unique bot ID
 */
export function generateBotId(): string {
    return Math.random().toString(36).substring(2, 11);
}
