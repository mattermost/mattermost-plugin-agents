import { mattermostAIPluginRoutes, PluginRoutesApi } from './plugin-http';

// EnabledTool matches llm.EnabledMCPTool on the backend.
// Inner field names stay snake_case to match the backend's json:"server_origin"
// / json:"tool_name" tags; see .planning/phase-1/PLAN.md pitfall P2.
export interface EnabledTool {
    server_origin: string;
    tool_name: string;
}

// CreateAgentRequest matches api.CreateAgentRequest in Go.
//
// Create is an explicit full-object request: the UI / calling helper is the sole
// source of truth for create-time defaults; the backend no longer substitutes hidden
// defaults for omitted fields. MCP tool access is controlled by two independent fields:
//   - autoEnableNewMCPTools=true  → agent gets every MCP tool, current and future.
//   - autoEnableNewMCPTools=false → agent gets only the tools listed in enabledMCPTools.
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
    enabledMCPTools?: EnabledTool[];
    autoEnableNewMCPTools: boolean;
    enabledNativeTools?: string[];
    model?: string;
    enableVision?: boolean;
    disableTools?: boolean;
    reasoningEnabled?: boolean;
    reasoningEffort?: string;
    thinkingBudget?: number;
    structuredOutputEnabled?: boolean;
    maxToolTurns?: number;
}

// UpdateAgentRequest matches api.UpdateAgentRequest in Go.
//
// Update is a full-object replacement, not a patch: every mutable field the caller
// wants to keep must be included in the request. Fields omitted from the payload are
// overwritten with their JSON zero values.
export type UpdateAgentRequest = CreateAgentRequest;

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
    enabledMCPTools?: EnabledTool[];
    autoEnableNewMCPTools: boolean;
    reasoningEnabled: boolean;
    reasoningEffort: string;
    thinkingBudget: number;
    structuredOutputEnabled: boolean;
    maxToolTurns?: number;
    // Admin / lifecycle metadata (omitempty on backend).
    botUserID?: string;
    creatorID?: string;
    adminUserIDs?: string[];
    createAt?: number;
    updateAt?: number;
    deleteAt?: number;
}

/** Build a full UpdateAgentRequest payload from an existing agent response, merging
 * in the caller's overrides. This matches the backend's full-replacement contract:
 * every mutable field is sent on every update. */
export function mergeAgentIntoUpdate(
    agent: AgentResponse,
    overrides: Partial<UpdateAgentRequest>,
): UpdateAgentRequest {
    const base: UpdateAgentRequest = {
        displayName: agent.displayName,
        username: agent.name,
        serviceID: agent.serviceID,
        customInstructions: agent.customInstructions,
        channelAccessLevel: agent.channelAccessLevel,
        channelIDs: agent.channelIDs,
        userAccessLevel: agent.userAccessLevel,
        userIDs: agent.userIDs,
        teamIDs: agent.teamIDs,
        adminUserIDs: agent.adminUserIDs ?? [],
        enabledMCPTools: agent.enabledMCPTools ?? [],
        autoEnableNewMCPTools: agent.autoEnableNewMCPTools,
        enabledNativeTools: agent.enabledNativeTools,
        model: agent.model,
        enableVision: agent.enableVision,
        disableTools: agent.disableTools,
        reasoningEnabled: agent.reasoningEnabled,
        reasoningEffort: agent.reasoningEffort,
        thinkingBudget: agent.thinkingBudget,
        structuredOutputEnabled: agent.structuredOutputEnabled,
        maxToolTurns: agent.maxToolTurns,
    };
    return { ...base, ...overrides };
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

    /**
     * Update an existing agent. The backend requires a full-object replacement, so this
     * helper fetches the current agent, merges in the provided overrides, and then
     * sends the complete document. Callers only need to pass the fields they want to
     * change.
     */
    async updateAgent(
        token: string,
        agentId: string,
        overrides: Partial<UpdateAgentRequest>,
    ): Promise<AgentResponse> {
        const current = await this.getAgent(token, agentId);
        const body = mergeAgentIntoUpdate(current, overrides);
        return this.routes.putJson(`agents/${agentId}`, token, body) as Promise<AgentResponse>;
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
     * Create an agent with auto-generated unique username. By default the agent
     * auto-enables every MCP tool so tests that don't care about MCP policy still
     * behave like pre-allowlist bots.
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
            autoEnableNewMCPTools: true,
            ...overrides,
        };
        return this.createAgent(token, req);
    }
}
