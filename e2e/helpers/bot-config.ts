import { Client4 } from '@mattermost/client';
import MattermostContainer from './mmcontainer';
import { mergeAgentIntoUpdate, type AgentResponse, type UpdateAgentRequest } from './agent-api';
import {
    mattermostAIAdminConfigApiFromClient,
    mattermostAIPluginRoutes,
    normalizeMattermostAiConfigFromApi,
    type PluginAdminConfigApi,
    type PluginRoutesApi,
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

/** Maps legacy BotConfig partial updates to UpdateAgentRequest overrides for mergeAgentIntoUpdate. */
function botConfigPartialToUpdateOverrides(updates: Partial<BotConfig>): Partial<UpdateAgentRequest> {
    const o: Partial<UpdateAgentRequest> = {};
    if (updates.displayName !== undefined) {
        o.displayName = updates.displayName;
    }
    if (updates.customInstructions !== undefined) {
        o.customInstructions = updates.customInstructions;
    }
    if (updates.serviceID !== undefined) {
        o.serviceID = updates.serviceID;
    }
    if (updates.enableVision !== undefined) {
        o.enableVision = updates.enableVision;
    }
    if (updates.disableTools !== undefined) {
        o.disableTools = updates.disableTools;
    }
    if (updates.reasoningEnabled !== undefined) {
        o.reasoningEnabled = updates.reasoningEnabled;
    }
    if (updates.reasoningEffort !== undefined) {
        o.reasoningEffort = updates.reasoningEffort;
    }
    if (updates.thinkingBudget !== undefined) {
        o.thinkingBudget = updates.thinkingBudget;
    }
    return o;
}

export class BotConfigHelper {
    private adminApi: PluginAdminConfigApi;
    private routes: PluginRoutesApi;
    private client: Client4;

    constructor(client: Client4, baseUrl: string) {
        this.client = client;
        this.adminApi = mattermostAIAdminConfigApiFromClient(client, baseUrl);
        this.routes = mattermostAIPluginRoutes(baseUrl);
    }

    private async listAgents(): Promise<AgentResponse[]> {
        return this.routes.getJson('agents', this.client.getToken()) as Promise<AgentResponse[]>;
    }

    /** Map a DB-backed user agent to the legacy BotConfig shape used by older tests. */
    private agentToBotConfig(a: AgentResponse): BotConfig {
        return {
            id: a.id,
            name: a.name,
            displayName: a.displayName,
            customInstructions: a.customInstructions,
            serviceID: a.serviceID,
            enableVision: a.enableVision,
            disableTools: a.disableTools,
            reasoningEnabled: a.reasoningEnabled,
            reasoningEffort: a.reasoningEffort,
            thinkingBudget: a.thinkingBudget,
        };
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
        const fromConfig = config.config.bots.find(bot => bot.id === botId);
        if (fromConfig) {
            return fromConfig;
        }
        try {
            const agents = await this.listAgents();
            const match = agents.find(a => a.id === botId);
            return match ? this.agentToBotConfig(match) : undefined;
        } catch {
            return undefined;
        }
    }

    /**
     * Get a bot by name
     */
    async getBotByName(botName: string): Promise<BotConfig | undefined> {
        const config = await this.getPluginConfig();
        const fromConfig = config.config.bots.find(bot => bot.name === botName);
        if (fromConfig) {
            return fromConfig;
        }
        try {
            const agents = await this.listAgents();
            const match = agents.find(a => a.name === botName);
            return match ? this.agentToBotConfig(match) : undefined;
        } catch {
            return undefined;
        }
    }

    /**
     * Update a bot configuration
     */
    async updateBot(botId: string, updates: Partial<BotConfig>): Promise<void> {
        const config = await this.getPluginConfig();
        const botIndex = config.config.bots.findIndex(bot => bot.id === botId);

        if (botIndex !== -1) {
            config.config.bots[botIndex] = {
                ...config.config.bots[botIndex],
                ...updates,
            };
            await this.updatePluginConfig(config);
            return;
        }

        // Legacy config bots were migrated to Agents_UserAgents; update via user-agent API.
        // PUT /agents/:id requires a full replacement body; partial JSON is rejected with 400.
        const overrides = botConfigPartialToUpdateOverrides(updates);
        if (Object.keys(overrides).length === 0) {
            throw new Error(`Bot with ID ${botId} not found and no migratable fields to update`);
        }
        const token = this.client.getToken();
        const current = (await this.routes.getJson(
            `agents/${botId}`,
            token,
        )) as AgentResponse;
        const body = mergeAgentIntoUpdate(current, overrides);
        await this.routes.putJson(`agents/${botId}`, token, body);
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
