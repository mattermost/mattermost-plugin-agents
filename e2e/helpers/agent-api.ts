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
    mcpDynamicToolLoading?: boolean;
    useServiceAccountAuth?: boolean;
    enabledNativeTools?: string[];
    model?: string;
    enableVision?: boolean;
    disableTools?: boolean;
    reasoningEnabled?: boolean;
    reasoningEffort?: string;
    thinkingBudget?: number;
    structuredOutputEnabled?: boolean;
    maxToolTurns?: number;
    documents?: AgentDocumentRef[];
}

// AgentDocumentRef is a reference document in agent create/update requests (api.AgentDocumentRef).
export interface AgentDocumentRef {
    id: string;
    name: string;
}

// AgentDocument matches llm.AgentDocument (and the POST /agents/documents response).
export interface AgentDocument {
    id: string;
    name: string;
    mimeType: string;
    size: number;
    sha256: string;
    textRunes: number;
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
    mcpDynamicToolLoading?: boolean;
    useServiceAccountAuth: boolean;
    reasoningEnabled: boolean;
    reasoningEffort: string;
    thinkingBudget: number;
    structuredOutputEnabled: boolean;
    maxToolTurns?: number;
    documents?: AgentDocument[];
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
        mcpDynamicToolLoading: agent.mcpDynamicToolLoading ?? true,
        useServiceAccountAuth: agent.useServiceAccountAuth,
        enabledNativeTools: agent.enabledNativeTools,
        model: agent.model,
        enableVision: agent.enableVision,
        disableTools: agent.disableTools,
        reasoningEnabled: agent.reasoningEnabled,
        reasoningEffort: agent.reasoningEffort,
        thinkingBudget: agent.thinkingBudget,
        structuredOutputEnabled: agent.structuredOutputEnabled,
        maxToolTurns: agent.maxToolTurns,
        documents: (agent.documents ?? []).map(({ id, name }) => ({ id, name })),
    };
    return { ...base, ...overrides };
}

// Mirrors store.AgentVersion / store.AgentVersionDetail.
export type AgentVersionSource = 'initial' | 'create' | 'update' | 'restore' | 'import' | 'system';

export interface AgentVersionSummary {
    version: number;
    createdBy: string;
    createAt: number;
    source: AgentVersionSource;
    restoredFromVersion: number;
    changedFields: string[];
}

export interface AgentVersionList {
    currentVersion: number;
    versions: AgentVersionSummary[];
}

export interface AgentVersionDetail extends AgentVersionSummary {
    config: Partial<AgentResponse>;
}

// Mirrors agentexport.Document (schemaVersion 1, or 2 with documents).
export interface AgentExportMCPTool {
    serverOrigin: string;
    serverName: string;
    toolName: string;
}

export interface AgentExportReferenceDocument {
    name: string;
    mimeType: string;
    size: number;
    sha256: string;
    /** Base64 of the original bytes. */
    content: string;
}

export interface AgentExportDocument {
    kind: string;
    schemaVersion: number;
    exportedAt: number;
    agentVersion: number;
    agent: {
        name: string;
        displayName: string;
        customInstructions: string;
        disableTools: boolean;
        maxToolTurns: number;
        mcpDynamicToolLoading: boolean;
        autoEnableNewMCPTools: boolean;
        mcpTools: AgentExportMCPTool[];
        documents?: AgentExportReferenceDocument[];
    };
}

export interface AgentImportPreview {
    document: AgentExportDocument;
    documents?: Array<{ name: string; mimeType: string; size: number }>;
    mcpServers: Array<{
        sourceOrigin: string;
        sourceName: string;
        toolNames: string[];
        autoTargetOrigin: string;
    }>;
    availableMCPServers: Array<{ origin: string; name: string }>;
    existingAgent?: { id: string; displayName: string; username: string; canManage: boolean } | null;
}

export interface AgentImportRequest {
    document: AgentExportDocument;
    mode: 'create' | 'update';
    agentID?: string;
    username?: string;
    displayName?: string;
    serviceID?: string;
    model?: string;
    mcpServerMappings?: Array<{ sourceOrigin: string; targetOrigin: string }>;
}

/** Status and parsed JSON body of a plugin response, for asserting on error paths. */
export interface RawJSONResponse<T> {
    status: number;
    headers: Headers;
    body: T;
}

async function toRawJSON<T>(response: Response): Promise<RawJSONResponse<T>> {
    const text = await response.text();
    let body: unknown = text;
    try {
        body = JSON.parse(text);
    } catch {
        // Keep the raw text for non-JSON bodies.
    }
    return { status: response.status, headers: response.headers, body: body as T };
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

    async getAgentVersions(token: string, agentId: string): Promise<AgentVersionList> {
        return this.routes.getJson(`agents/${agentId}/versions`, token) as Promise<AgentVersionList>;
    }

    async getAgentVersion(token: string, agentId: string, version: number): Promise<AgentVersionDetail> {
        return this.routes.getJson(`agents/${agentId}/versions/${version}`, token) as Promise<AgentVersionDetail>;
    }

    async exportAgent(token: string, agentId: string): Promise<RawJSONResponse<AgentExportDocument>> {
        return toRawJSON(await this.routes.request('GET', `agents/${agentId}/export`, token));
    }

    async previewAgentImport(
        token: string,
        document: AgentExportDocument,
    ): Promise<RawJSONResponse<AgentImportPreview & { error?: string }>> {
        return toRawJSON(await this.routes.request('POST', 'agents/import/preview', token, { document }));
    }

    async importAgent(
        token: string,
        req: AgentImportRequest,
    ): Promise<RawJSONResponse<AgentResponse & { error?: string }>> {
        return toRawJSON(await this.routes.request('POST', 'agents/import', token, req));
    }

    /** PUT /agents/:id with exactly body (no merging), returning the raw response. */
    async putAgentRaw(
        token: string,
        agentId: string,
        body: UpdateAgentRequest,
    ): Promise<RawJSONResponse<AgentResponse & { error?: string }>> {
        return toRawJSON(await this.routes.request('PUT', `agents/${agentId}`, token, body));
    }

    /** POST /agents/documents with a single multipart `file`. */
    async uploadAgentDocument(
        token: string,
        file: { name: string; mimeType: string; buffer: Buffer },
    ): Promise<RawJSONResponse<AgentDocument & { error?: string }>> {
        const form = new FormData();
        form.append('file', new Blob([new Uint8Array(file.buffer)], { type: file.mimeType }), file.name);
        const response = await fetch(this.routes.pluginUrl('agents/documents'), {
            method: 'POST',
            headers: { Authorization: `Bearer ${token}` },
            body: form,
        });
        return toRawJSON(response);
    }

    /** Uploads a document and fails the test setup when the server rejects it. */
    async uploadAgentDocumentOrThrow(
        token: string,
        file: { name: string; mimeType: string; buffer: Buffer },
    ): Promise<AgentDocument> {
        const response = await this.uploadAgentDocument(token, file);
        if (response.status !== 201) {
            throw new Error(`POST agents/documents ${file.name} failed: ${response.status} ${JSON.stringify(response.body)}`);
        }
        return response.body;
    }

    /** GET /agents/:id/documents/:documentid (the original bytes). */
    async downloadAgentDocument(token: string, agentId: string, documentId: string): Promise<Response> {
        return this.routes.request('GET', `agents/${agentId}/documents/${documentId}`, token);
    }

    /** GET /agents/:id/documents/:documentid/text. */
    async getAgentDocumentText(
        token: string,
        agentId: string,
        documentId: string,
    ): Promise<RawJSONResponse<{ id: string; text: string; textRunes: number; error?: string }>> {
        return toRawJSON(await this.routes.request('GET', `agents/${agentId}/documents/${documentId}/text`, token));
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
