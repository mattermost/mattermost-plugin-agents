import { mattermostAIPluginRoutes, PluginRoutesApi } from './plugin-http';

export interface EnabledTool {
    server_origin: string;
    tool_name: string;
}

export interface CreateAgentRequest {
    display_name: string;
    username: string;
    service_id: string;
    custom_instructions?: string;
    channel_access_level?: number;
    channel_ids?: string[];
    user_access_level?: number;
    user_ids?: string[];
    team_ids?: string[];
    admin_user_ids?: string[];
    enabled_tools?: EnabledTool[];
}

export interface AgentResponse {
    id: string;
    bot_user_id: string;
    creator_id: string;
    display_name: string;
    username: string;
    service_id: string;
    custom_instructions: string;
    channel_access_level: number;
    channel_ids: string[];
    user_access_level: number;
    user_ids: string[];
    team_ids: string[];
    admin_user_ids: string[];
    enabled_tools: EnabledTool[];
    create_at: number;
    update_at: number;
    delete_at: number;
}

/**
 * AgentAPIHelper — programmatic agent CRUD for test setup/teardown.
 * Uses the plugin's REST API (Phase 2 endpoints).
 */
export class AgentAPIHelper {
    private routes: PluginRoutesApi;

    constructor(baseUrl: string) {
        this.routes = mattermostAIPluginRoutes(baseUrl);
    }

    async createAgent(token: string, req: CreateAgentRequest): Promise<AgentResponse> {
        return this.routes.postJson('agents', token, req) as Promise<AgentResponse>;
    }

    async getAgents(token: string): Promise<AgentResponse[]> {
        return this.routes.getJson('agents', token) as Promise<AgentResponse[]>;
    }

    async getAgent(token: string, agentId: string): Promise<AgentResponse> {
        return this.routes.getJson(`agents/${agentId}`, token) as Promise<AgentResponse>;
    }

    async updateAgent(token: string, agentId: string, updates: Partial<CreateAgentRequest>): Promise<AgentResponse> {
        return this.routes.putJson(`agents/${agentId}`, token, updates) as Promise<AgentResponse>;
    }

    async deleteAgent(token: string, agentId: string): Promise<void> {
        const url = this.routes.pluginUrl(`agents/${agentId}`);
        const response = await fetch(url, {
            method: 'DELETE',
            headers: { Authorization: `Bearer ${token}` },
        });
        if (!response.ok) {
            throw new Error(`DELETE agents/${agentId} failed: ${response.status}`);
        }
    }

    /**
     * Create an agent with auto-generated unique username.
     */
    async createTestAgent(
        token: string,
        overrides: Partial<CreateAgentRequest> = {},
    ): Promise<AgentResponse> {
        const uniqueSuffix = Date.now().toString(36);
        const req: CreateAgentRequest = {
            display_name: `Test Agent ${uniqueSuffix}`,
            username: `testagent${uniqueSuffix}`,
            service_id: 'mock-service',
            ...overrides,
        };
        return this.createAgent(token, req);
    }
}
