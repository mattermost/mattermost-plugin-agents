// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Client4 as Client4Class, ClientError} from '@mattermost/client';
import {ChannelWithTeamData} from '@mattermost/types/channels';

import {NotPagedTeamSearchOpts, Team} from '@mattermost/types/teams';

import {PluginConfig} from '@/components/system_console/plugin_config_types';
import type {Composition, ConversationResponse} from '@/types/conversation';
import {UserAgent, CreateAgentRequest, UpdateAgentRequest, ServiceInfo} from '@/types/agents';

import manifest from './manifest';

import {CustomPrompt} from './types';

const Client4 = new Client4Class();

type MCPToolPolicy = 'auto_run_in_dm' | 'auto_run_everywhere' | 'ask';
type VettedToolConfig = {name: string; policy: MCPToolPolicy; enabled: boolean};

// Mirrors components/system_console/mcp_servers.tsx MCPToolConfig; duplicated to
// avoid client.tsx depending on UI components.
type MCPToolConfig = {
    name: string;
    policy: MCPToolPolicy;
    enabled: boolean;
};

export function setSiteURL(siteURL: string) {
    Client4.setUrl(siteURL);
}

function baseRoute(): string {
    return `${Client4.url}/plugins/${manifest.id}`;
}

function postRoute(postid: string): string {
    return `${baseRoute()}/post/${postid}`;
}

function channelRoute(channelid: string): string {
    return `${baseRoute()}/channel/${channelid}`;
}

function agentRoute(agentId: string): string {
    return `${baseRoute()}/agents/${agentId}`;
}

// readAgentErrorMessage extracts the server-provided error message from an
// agent endpoint response body. The agent API returns `{"error": "..."}` for
// non-2xx responses so the UI can surface actionable validation feedback
// (oversized prompt, taken username, etc.) instead of a generic retry hint.
async function readAgentErrorMessage(response: Response): Promise<string> {
    try {
        const data: unknown = await response.json();
        if (
            data !== null &&
            typeof data === 'object' &&
            'error' in data &&
            typeof (data as {error?: unknown}).error === 'string'
        ) {
            return (data as {error: string}).error;
        }
    } catch {
        // Body was empty or not JSON — fall through to empty string so the
        // caller can apply a generic fallback.
    }
    return '';
}

export async function doReaction(postid: string) {
    const url = `${postRoute(postid)}/react`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
    }));

    if (response.ok) {
        return;
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function doThreadAnalysis(postid: string, analysisType: string, botUsername: string) {
    const url = `${postRoute(postid)}/analyze?botUsername=${botUsername}`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
        body: JSON.stringify({
            analysis_type: analysisType,
        }),
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function doChannelAnalysis(channelId: string, analysisType: string, botUsername: string, options?: any) {
    const url = `${channelRoute(channelId)}/analyze?botUsername=${botUsername}`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
        body: JSON.stringify({
            analysis_type: analysisType,
            ...options,
        }),
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function doTranscribe(postid: string, fileID: string) {
    const url = `${postRoute(postid)}/transcribe/file/${fileID}`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function doSummarizeTranscription(postid: string) {
    const url = `${postRoute(postid)}/summarize_transcription`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function doStopGenerating(postid: string) {
    const url = `${postRoute(postid)}/stop`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
    }));

    if (response.ok) {
        return;
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function doRegenerate(postid: string) {
    const url = `${postRoute(postid)}/regenerate`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
    }));

    if (response.ok) {
        return;
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function doToolCall(postid: string, toolIDs: string[]) {
    const url = `${postRoute(postid)}/tool_call`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
        body: JSON.stringify({
            accepted_tool_ids: toolIDs,
        }),
    }));

    if (response.ok) {
        return;
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function doToolResult(postid: string, toolIDs: string[]): Promise<void> {
    const url = `${postRoute(postid)}/tool_result`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
        body: JSON.stringify({
            accepted_tool_ids: toolIDs,
        }),
    }));

    if (response.ok) {
        return;
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function doPostbackSummary(postid: string) {
    const url = `${postRoute(postid)}/postback_summary`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function doLoopInAgent(postid: string, botUsername: string) {
    const url = `${postRoute(postid)}/loop_in_agent?botUsername=${encodeURIComponent(botUsername)}`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
    }));

    if (response.ok) {
        return;
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function viewMyChannel(channelID: string) {
    return Client4.viewMyChannel(channelID);
}

export async function getAIDirectChannel(currentUserId: string) {
    const botUser = await Client4.getUserByUsername('ai');
    const dm = await Client4.createDirectChannel([currentUserId, botUser.id]);
    return dm.id;
}

export async function getBotDirectChannel(currentUserId: string, botUserID: string) {
    const dm = await Client4.createDirectChannel([currentUserId, botUserID]);
    return dm.id;
}

export async function getAIThreads() {
    const url = `${baseRoute()}/ai_threads`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

// normalizeConversationResponse coerces every turn's content to a non-null
// array. The backend may persist a turn whose content column is the JSON
// literal `null` (e.g. when a stream finalizes before any blocks accumulate),
// and downstream code iterates turn.content freely. Normalizing once here
// keeps every consumer free of defensive null checks.
export function normalizeConversationResponse(raw: ConversationResponse): ConversationResponse {
    return {
        ...raw,
        turns: (raw.turns ?? []).map((turn) => ({
            ...turn,
            content: turn.content ?? [],
        })),
    };
}

export async function getConversation(conversationId: string): Promise<ConversationResponse> {
    const url = `${baseRoute()}/conversations/${conversationId}`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        const raw = await response.json() as ConversationResponse;
        return normalizeConversationResponse(raw);
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function getConversationContext(conversationId: string): Promise<Composition> {
    const url = `${baseRoute()}/conversations/${conversationId}/context`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        return response.json() as Promise<Composition>;
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function getAIBots() {
    const url = `${baseRoute()}/ai_bots`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function createPost(post: any) {
    const created = await Client4.createPost(post);
    return created;
}

export function updateRead(userId: string, teamId: string, selectedPostId: string, timestamp: number) {
    return Client4.updateThreadReadForUser(userId, teamId, selectedPostId, timestamp);
}

export function getProfilePictureUrl(userId: string, lastIconUpdate: number) {
    return Client4.getProfilePictureUrl(userId, lastIconUpdate);
}

export async function getBotProfilePictureUrl(username: string) {
    const user = await Client4.getUserByUsername(username);
    if (!user || user.id === '') {
        return '';
    }
    return getProfilePictureUrl(user.id, user.last_picture_update);
}

export async function doRunSearch(query: string, teamId: string, channelId: string, botUsername?: string): Promise<{postid: string; channelid: string}> {
    const url = `${baseRoute()}/search/run${botUsername ? `?botUsername=${botUsername}` : ''}`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
        body: JSON.stringify({
            query,
            teamId,
            channelId,
        }),
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function setUserProfilePictureByUsername(username: string, file: File) {
    const user = await Client4.getUserByUsername(username);
    if (!user || user.id === '') {
        return;
    }
    await setUserProfilePicture(user.id, file);
}

export async function setUserProfilePicture(userId: string, file: File) {
    await Client4.uploadProfileImage(userId, file);
}

export async function getAutocompleteAllUsers(name: string) {
    return Client4.autocompleteUsers(name, '', '');
}

export async function getProfilesByIds(userIds: string[]) {
    if (userIds.length === 0) {
        return [];
    }
    return Client4.getProfilesByIds(userIds);
}

export async function searchAllChannels(term: string): Promise<ChannelWithTeamData[]> {
    return Client4.searchAllChannels(term, {

        // Use the non-admin search path so regular users can search visible channels
        // without requiring system console permissions.
        nonAdminSearch: true,
        public: true,
        private: true,
        include_deleted: false,
        deleted: false,
    }) as Promise<ChannelWithTeamData[]>; // With these paremeters we should always get ChannelWithTeamData[]
}

export async function getChannelById(channelId: string): Promise<ChannelWithTeamData> {
    const channel = await Client4.getChannel(channelId);
    const team = await Client4.getTeam(channel.team_id);
    return {
        ...channel,
        team_name: team.display_name,
        team_display_name: team.display_name,
        team_update_at: team.update_at,
    };
}

export async function getTeamsByIds(teamIds: string[]) {
    if (teamIds.length === 0) {
        return [];
    }
    return Promise.all(teamIds.map((id) => Client4.getTeam(id)));
}

export async function searchTeams(term: string): Promise<Team[]> {
    const opts: NotPagedTeamSearchOpts = {};

    // Types are messed up
    return Client4.searchTeams(term, opts) as unknown as Promise<Team[]>;
}

export function getTeamIconUrl(teamId: string, lastTeamIconUpdate: number) {
    return Client4.getTeamIconUrl(teamId, lastTeamIconUpdate);
}

export function getPost(postId: string) {
    return Client4.getPost(postId);
}

export async function doReindexPosts(clearIndex = true) {
    const url = `${baseRoute()}/admin/reindex`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
        body: JSON.stringify({clearIndex}),
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function getReindexStatus() {
    const url = `${baseRoute()}/admin/reindex/status`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function cancelReindex() {
    const url = `${baseRoute()}/admin/reindex/cancel`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function catchUpIndex() {
    const url = `${baseRoute()}/admin/reindex/catchup`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function checkIndexHealth() {
    const url = `${baseRoute()}/admin/reindex/health-check`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function getMCPTools() {
    const url = `${baseRoute()}/admin/mcp/tools`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function clearMCPToolsCache() {
    const url = `${baseRoute()}/admin/mcp/tools/cache/clear`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

// Omitted fields are preserved server-side; tool_configs: [] clears policy.
export async function updatePluginServer(
    pluginID: string,
    update: {
        enabled?: boolean;
        tool_configs?: MCPToolConfig[];
    },
) {
    const encoded = encodeURIComponent(pluginID);
    const url = `${baseRoute()}/admin/mcp/plugin-servers/${encoded}`;
    const response = await fetch(url, Client4.getOptions({
        method: 'PUT',
        body: JSON.stringify(update),
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

/** Authoritative vetted default tool_configs for a base URL (matches mcp.SeedVettedToolConfigs). */
export async function getVettedToolSeed(baseURL: string): Promise<VettedToolConfig[]> {
    const trimmed = baseURL.trim();
    if (!trimmed) {
        return [];
    }

    const url = `${baseRoute()}/admin/mcp/vetted-tool-seed?base_url=${encodeURIComponent(trimmed)}`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        const data = await response.json() as {tool_configs?: VettedToolConfig[]};
        return data.tool_configs ?? [];
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export type FetchModelsOptions = {
    region?: string;
    vertexProjectID?: string;
    vertexProjectNumber?: string;
    vertexAuthCredentials?: string;
}

export async function fetchModels(serviceType: string, apiKey: string, apiURL: string, orgID: string, options: FetchModelsOptions = {}) {
    const url = `${baseRoute()}/admin/models/fetch`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
        body: JSON.stringify({
            serviceType,
            apiKey,
            apiURL,
            orgID,
            region: options.region || '',
            vertexProjectID: options.vertexProjectID || '',
            vertexProjectNumber: options.vertexProjectNumber || '',
            vertexAuthCredentials: options.vertexAuthCredentials || '',
        }),
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function getUserMCPTools(): Promise<{servers: any[]}> {
    const url = `${baseRoute()}/mcp/tools`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function getUserToolPreferences(): Promise<{disabled_servers: string[]}> {
    const url = `${baseRoute()}/mcp/user-preferences`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function updateUserToolPreferences(prefs: {disabled_servers: string[]}): Promise<{disabled_servers: string[]}> {
    const url = `${baseRoute()}/mcp/user-preferences`;
    const response = await fetch(url, Client4.getOptions({
        method: 'PUT',
        body: JSON.stringify(prefs),
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function disconnectMCPOAuth(serverName: string): Promise<void> {
    const url = `${baseRoute()}/mcp/oauth/${encodeURIComponent(serverName)}`;
    const response = await fetch(url, Client4.getOptions({
        method: 'DELETE',
    }));

    if (response.ok) {
        return;
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function getChannelInterval(
    channelID: string,
    startTime: number,
    endTime: number,
    presetPrompt: string,
    prompt?: string,
    botUsername?: string,
): Promise<{postid: string; channelid: string}> {
    const url = `${channelRoute(channelID)}/interval${botUsername ? `?botUsername=${botUsername}` : ''}`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
        body: JSON.stringify({
            start_time: startTime,
            end_time: endTime,
            preset_prompt: presetPrompt,
            prompt: prompt || '',
        }),
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function getPluginConfig(): Promise<PluginConfig> {
    const url = `${baseRoute()}/admin/config`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function savePluginConfig(config: PluginConfig): Promise<void> {
    const url = `${baseRoute()}/admin/config`;
    const response = await fetch(url, Client4.getOptions({
        method: 'PUT',
        body: JSON.stringify(config),
        headers: {'Content-Type': 'application/json'},
    }));

    if (response.ok) {
        return;
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

// --- Agent CRUD ---

export type AgentsListResult = {
    agents: UserAgent[];
    activeAgentCount?: number;
};

export async function getAgents(): Promise<AgentsListResult> {
    const url = `${baseRoute()}/agents`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        const agents = await response.json() as UserAgent[];
        const activeCountHeader = response.headers.get('X-Agent-Active-Count');
        const result: AgentsListResult = {agents};

        // Only trust a strict non-negative integer (rejects e.g. "1foo", "", null).
        if (activeCountHeader !== null && (/^\d+$/).test(activeCountHeader)) {
            result.activeAgentCount = Number.parseInt(activeCountHeader, 10);
        }
        return result;
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function createAgent(agent: CreateAgentRequest): Promise<UserAgent> {
    const url = `${baseRoute()}/agents`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
        body: JSON.stringify(agent),
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: await readAgentErrorMessage(response),
        status_code: response.status,
        url,
    });
}

export async function updateAgent(id: string, agent: UpdateAgentRequest): Promise<UserAgent> {
    const url = agentRoute(id);
    const response = await fetch(url, Client4.getOptions({
        method: 'PUT',
        body: JSON.stringify(agent),
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: await readAgentErrorMessage(response),
        status_code: response.status,
        url,
    });
}

export async function deleteAgent(id: string): Promise<void> {
    const url = agentRoute(id);
    const response = await fetch(url, Client4.getOptions({
        method: 'DELETE',
    }));

    if (response.ok) {
        return;
    }

    throw new ClientError(Client4.url, {
        message: await readAgentErrorMessage(response),
        status_code: response.status,
        url,
    });
}

export async function uploadAgentAvatar(agentId: string, file: File): Promise<void> {
    const url = `${agentRoute(agentId)}/avatar`;
    const formData = new FormData();
    formData.append('image', file);

    const headers = {...(Client4.getOptions({method: 'POST'}).headers as Record<string, string>)};
    delete headers['Content-Type'];

    const response = await fetch(url, {
        method: 'POST',
        headers,
        body: formData,
    });

    if (response.ok) {
        return;
    }

    throw new ClientError(Client4.url, {
        message: await readAgentErrorMessage(response),
        status_code: response.status,
        url,
    });
}

export async function getServices(): Promise<ServiceInfo[]> {
    const url = `${baseRoute()}/services`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export type ModelListItem = {
    id: string;
    displayName: string;
}

/** Fetches models for a configured service using server-stored credentials (POST /agents/models/fetch). */
export async function fetchModelsForAgentService(serviceId: string, signal?: AbortSignal): Promise<ModelListItem[]> {
    const url = `${baseRoute()}/agents/models/fetch`;
    const response = await fetch(url, {
        ...Client4.getOptions({
            method: 'POST',
            body: JSON.stringify({serviceID: serviceId}),
        }),
        signal,
    });

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function getCustomPrompts(): Promise<CustomPrompt[]> {
    const url = `${baseRoute()}/custom-prompts`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function createCustomPrompt(prompt: {name: string; description: string; template: string; is_shared: boolean}): Promise<CustomPrompt> {
    const url = `${baseRoute()}/custom-prompts`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
        body: JSON.stringify(prompt),
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function updateCustomPrompt(id: string, prompt: {name: string; description: string; template: string; is_shared: boolean}): Promise<void> {
    const url = `${baseRoute()}/custom-prompts/${id}`;
    const response = await fetch(url, Client4.getOptions({
        method: 'PUT',
        body: JSON.stringify(prompt),
    }));

    if (!response.ok) {
        throw new ClientError(Client4.url, {
            message: '',
            status_code: response.status,
            url,
        });
    }
}

export async function deleteCustomPrompt(id: string): Promise<void> {
    const url = `${baseRoute()}/custom-prompts/${id}`;
    const response = await fetch(url, Client4.getOptions({
        method: 'DELETE',
    }));

    if (!response.ok) {
        throw new ClientError(Client4.url, {
            message: '',
            status_code: response.status,
            url,
        });
    }
}

export async function getCustomPromptPins(): Promise<string[]> {
    const url = `${baseRoute()}/custom-prompts/pins`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function setCustomPromptPin(promptId: string, pinned: boolean): Promise<void> {
    const url = `${baseRoute()}/custom-prompts/pins`;
    const response = await fetch(url, Client4.getOptions({
        method: 'PUT',
        body: JSON.stringify({prompt_id: promptId, pinned}),
    }));

    if (!response.ok) {
        throw new ClientError(Client4.url, {
            message: '',
            status_code: response.status,
            url,
        });
    }
}

export async function renderCustomPrompt(id: string, channelId?: string, botUsername?: string): Promise<{rendered: string}> {
    const url = `${baseRoute()}/custom-prompts/${id}/render`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
        body: JSON.stringify({channel_id: channelId, bot_username: botUsername}),
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}
