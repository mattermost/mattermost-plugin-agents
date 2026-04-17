import { mattermostAIPluginRoutes, PluginRoutesApi } from './plugin-http';

// EnabledTool matches llm.EnabledMCPTool on the backend.
// Inner field names stay snake_case to match the backend's json:"server_origin"
// / json:"tool_name" tags; see .planning/phase-1/PLAN.md pitfall P2.
export interface EnabledTool {
    server_origin: string;
    tool_name: string;
}

export interface CreateAgentRequest {
    displayName: string;
    username: string;
    serviceID: string;
    customInstructions?: string;
    channelAccessLevel?: number;
    channelIDs?: string[];
    userAccessLevel?: number;
    userIDs?: string[];
    teamIDs?: string[];
    adminUserIDs?: string[];
    enabledMCPTools?: EnabledTool[] | null;
    enabledNativeTools?: string[];
    // Optional defaults-controlled fields (matches api.CreateAgentRequest pointer fields).
    model?: string;
    enableVision?: boolean;
    disableTools?: boolean;
    reasoningEnabled?: boolean;
    reasoningEffort?: string;
    thinkingBudget?: number;
    structuredOutputEnabled?: boolean;
}

export interface AgentResponse {
    id: string;
    name: string; // backend emits BotConfig.Name under JSON key "name" (see Phase 2 PLAN §2.5)
    displayName: string;
    customInstructions: string;
    serviceID: string;
    model: string;
    enableVision: boolean;
    disableTools: boolean;
    channelAccessLevel: number;
    channelIDs: string[];
    userAccessLevel: number;
    userIDs: string[];
    teamIDs: string[];
    enabledNativeTools: string[];
    enabledMCPTools?: EnabledTool[] | null;
    reasoningEnabled: boolean;
    reasoningEffort: string;
    thinkingBudget: number;
    structuredOutputEnabled: boolean;
    // Admin / lifecycle metadata (omitempty on backend).
    botUserID?: string;
    creatorID?: string;
    adminUserIDs?: string[];
    createAt?: number;
    updateAt?: number;
    deleteAt?: number;
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
            displayName: `Test Agent ${uniqueSuffix}`,
            username: `testagent${uniqueSuffix}`,
            serviceID: 'mock-service',
            ...overrides,
        };
        return this.createAgent(token, req);
    }
}
