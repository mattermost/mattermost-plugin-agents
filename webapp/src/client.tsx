// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Client4 as Client4Class, ClientError} from '@mattermost/client';
import {ChannelWithTeamData} from '@mattermost/types/channels';
import type {FileInfo} from '@mattermost/types/files';
import {PreferenceType} from '@mattermost/types/preferences';

import {NotPagedTeamSearchOpts, Team} from '@mattermost/types/teams';

import {PluginConfig} from '@/components/system_console/plugin_config_types';
import type {ToolAnswer} from '@/components/tool_types';
import type {Composition, ConversationResponse} from '@/types/conversation';
import {
    UserAgent,
    CreateAgentRequest,
    UpdateAgentRequest,
    ServiceInfo,
    AgentVersionList,
    AgentVersionDetail,
} from '@/types/agents';
import {isValidId} from '@/utils/ids';

import manifest from './manifest';

import {CustomPrompt} from './types';

export const Client4 = new Client4Class();

type MCPToolPolicy = 'auto_run_in_dm' | 'auto_run_everywhere' | 'ask';
type VettedToolConfig = {name: string; policy: MCPToolPolicy; enabled: boolean};
export type UserMCPToolInfo = {
    name: string;
    description: string;
    enabled: boolean;
    policy: MCPToolPolicy;
};
export type MCPServerKind = 'remote' | 'embedded' | 'plugin';

export type UserMCPServerInfo = {
    name: string;
    serverOrigin: string;
    kind: MCPServerKind;
    authenticated: boolean;
    needsOAuth: boolean;
    authEmail?: string;
    authURL?: string;
    serviceAccountConfigured: boolean;
    tools: UserMCPToolInfo[];
};
export type UserMCPToolsResponse = {
    servers: UserMCPServerInfo[];
};

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

export function savePreferences(userId: string, preferences: PreferenceType[]) {
    return Client4.savePreferences(userId, preferences);
}

export function baseRoute(): string {
    return `${Client4.url}/plugins/${manifest.id}`;
}

// Path of the sandboxed HTML artifact document served by the plugin, without
// the per-load nonce the server requires.
export function artifactPath(fileId: string): string {
    return `${baseRoute()}/artifacts/${encodeURIComponent(fileId)}`;
}

// URL of the artifact document for one host load of its frame.
export function artifactURL(fileId: string, nonce: string): string {
    return `${artifactPath(fileId)}?n=${encodeURIComponent(nonce)}`;
}

export interface ArtifactTokenInfo {
    token: string;

    // The artifact exceeds the render size limit; loading it would fail.
    tooLarge: boolean;
}

// Fetches the viewer's token that authenticates messages from the artifact's
// bridge script to this page. The token is only valid for the frame load
// that uses the same nonce.
export async function getArtifactToken(fileId: string, nonce: string): Promise<ArtifactTokenInfo> {
    const url = `${artifactPath(fileId)}/token?n=${encodeURIComponent(nonce)}`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        const data: unknown = await response.json();
        const {token, tooLarge} = (data as {token?: unknown; tooLarge?: unknown} | null) ?? {};
        if (typeof token === 'string' && token !== '') {
            return {token, tooLarge: tooLarge === true};
        }
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export function fileDownloadURL(fileId: string): string {
    return `${Client4.url}/api/v4/files/${encodeURIComponent(fileId)}?download=1`;
}

// Interpolated ids are encoded so each one can only ever occupy one path segment.
function postRoute(postid: string): string {
    return `${baseRoute()}/post/${encodeURIComponent(postid)}`;
}

function channelRoute(channelid: string): string {
    return `${baseRoute()}/channel/${encodeURIComponent(channelid)}`;
}

export function agentRoute(agentId: string): string {
    return `${baseRoute()}/agents/${encodeURIComponent(agentId)}`;
}

function conversationRoute(conversationId: string): string {
    return `${baseRoute()}/conversations/${encodeURIComponent(conversationId)}`;
}

// readAgentErrorMessage extracts the server-provided error message from an
// agent endpoint response body. The agent API returns `{"error": "..."}` for
// non-2xx responses so the UI can surface actionable validation feedback
// (oversized prompt, taken username, etc.) instead of a generic retry hint.
export async function readAgentErrorMessage(response: Response): Promise<string> {
    return (await readAgentError(response)).message;
}

// readAgentError extracts the server-provided error message and, when the
// server identifies the error, its code (as server_error_id) so the UI can
// show a translated message.
export async function readAgentError(response: Response): Promise<{message: string; server_error_id?: string}> {
    try {
        const data: unknown = await response.json();
        if (data !== null && typeof data === 'object') {
            const {error, code} = data as {error?: unknown; code?: unknown};
            return {
                message: typeof error === 'string' ? error : '',
                ...(typeof code === 'string' && code ? {server_error_id: code} : {}),
            };
        }
    } catch {
        // Body was empty or not JSON — fall through to an empty message so the
        // caller can apply a generic fallback.
    }
    return {message: ''};
}

// readClientErrorMessage returns the server's error text for 403 responses,
// which carry `{error, license_required}` for license denials. Other statuses
// yield an empty message; callers inspect status_code for those.
export async function readClientErrorMessage(response: Response): Promise<string> {
    if (response.status !== 403) {
        return '';
    }
    return readAgentErrorMessage(response);
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
        message: await readClientErrorMessage(response),
        status_code: response.status,
        url,
    });
}

// Agents are selected by immutable bot user ID; omitted means the server default.
const botIdQuery = (botId?: string) => (botId ? `?botId=${encodeURIComponent(botId)}` : '');

export async function doThreadAnalysis(postid: string, analysisType: string, botId: string) {
    const url = `${postRoute(postid)}/analyze${botIdQuery(botId)}`;
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
        message: await readClientErrorMessage(response),
        status_code: response.status,
        url,
    });
}

export async function doChannelAnalysis(channelId: string, analysisType: string, botId: string, options?: any) {
    const url = `${channelRoute(channelId)}/analyze${botIdQuery(botId)}`;
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
        message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
        status_code: response.status,
        url,
    });
}

export async function doToolCall(postid: string, toolIDs: string[], toolAnswers?: Record<string, ToolAnswer>) {
    const url = `${postRoute(postid)}/tool_call`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
        body: JSON.stringify({
            accepted_tool_ids: toolIDs,
            tool_answers: toolAnswers,
        }),
    }));

    if (response.ok) {
        return;
    }

    throw new ClientError(Client4.url, {
        message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
        status_code: response.status,
        url,
    });
}

export async function doLoopInAgent(postid: string, botId: string) {
    if (!isValidId(postid)) {
        throw new ClientError(Client4.url, {
            message: 'Invalid post id',
            status_code: 400,
            url: Client4.url,
        });
    }

    const url = `${postRoute(postid)}/loop_in_agent${botIdQuery(botId)}`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
    }));

    if (response.ok) {
        return;
    }

    throw new ClientError(Client4.url, {
        message: await readClientErrorMessage(response),
        status_code: response.status,
        url,
    });
}

export async function viewMyChannel(channelID: string) {
    return Client4.viewMyChannel(channelID);
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
        message: await readClientErrorMessage(response),
        status_code: response.status,
        url,
    });
}

function invalidConversationIdError() {
    return new ClientError(Client4.url, {
        message: 'Invalid conversation id',
        status_code: 400,
        url: Client4.url,
    });
}

// renameConversation sets the user-visible title of one of the caller's conversations.
export async function renameConversation(conversationId: string, title: string): Promise<void> {
    if (!isValidId(conversationId)) {
        throw invalidConversationIdError();
    }

    const url = `${conversationRoute(conversationId)}/title`;
    const response = await fetch(url, Client4.getOptions({
        method: 'PUT',
        body: JSON.stringify({title}),
    }));

    if (response.ok) {
        return;
    }

    throw new ClientError(Client4.url, {
        message: await readClientErrorMessage(response),
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
    // The id can arrive straight off a free-form post prop; refuse to build a
    // request from a value that is not a well-formed id.
    if (!isValidId(conversationId)) {
        throw new ClientError(Client4.url, {
            message: 'Invalid conversation id',
            status_code: 400,
            url: Client4.url,
        });
    }

    const url = conversationRoute(conversationId);
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        const raw = await response.json() as ConversationResponse;
        return normalizeConversationResponse(raw);
    }

    throw new ClientError(Client4.url, {
        message: await readClientErrorMessage(response),
        status_code: response.status,
        url,
    });
}

export async function getConversationContext(conversationId: string): Promise<Composition> {
    // Same as getConversation: never build a request from a malformed id.
    if (!isValidId(conversationId)) {
        throw new ClientError(Client4.url, {
            message: 'Invalid conversation id',
            status_code: 400,
            url: Client4.url,
        });
    }

    const url = `${conversationRoute(conversationId)}/context`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        return response.json() as Promise<Composition>;
    }

    throw new ClientError(Client4.url, {
        message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
        status_code: response.status,
        url,
    });
}

export async function createPost(post: any) {
    const created = await Client4.createPost(post);
    return created;
}

export interface FileUpload {
    promise: Promise<FileInfo>;
    abort: () => void;
}

export class FileUploadAbortedError extends Error {
    constructor() {
        super('File upload was cancelled');
        this.name = 'FileUploadAbortedError';
    }
}

// Uploads one file to a channel as the host composer does (POST /files with
// channel_id, client_ids and the file as multipart form data), but over XHR so
// the caller gets upload progress and can cancel. The returned FileInfo's id
// goes into the post's file_ids.
// A stalled upload fails instead of staying "uploading" forever.
export const UPLOAD_TIMEOUT_MS = 5 * 60 * 1000;

export function uploadFile(file: File, channelId: string, clientId: string, onProgress?: (percent: number) => void): FileUpload {
    const url = Client4.getFilesRoute();
    const xhr = new XMLHttpRequest();
    const promise = new Promise<FileInfo>((resolve, reject) => {
        xhr.open('POST', url, true);
        xhr.timeout = UPLOAD_TIMEOUT_MS;
        const headers = Client4.getOptions({method: 'POST'}).headers as Record<string, string | undefined>;
        Object.entries(headers).forEach(([name, value]) => {
            if (value) {
                xhr.setRequestHeader(name, value);
            }
        });
        xhr.setRequestHeader('Accept', 'application/json');

        if (onProgress && xhr.upload) {
            xhr.upload.onprogress = (event) => {
                if (event.lengthComputable && event.total > 0) {
                    onProgress(Math.floor((event.loaded / event.total) * 100));
                }
            };
        }

        const fail = (message: string) => reject(new ClientError(Client4.url, {message, status_code: xhr.status, url}));
        xhr.onload = () => {
            let body: any = null;
            try {
                body = JSON.parse(xhr.responseText);
            } catch {
                body = null;
            }
            if (xhr.status === 201) {
                const index = body?.client_ids?.indexOf(clientId) ?? -1;
                const info: FileInfo | undefined = body?.file_infos?.[index >= 0 ? index : 0];
                if (info?.id) {
                    resolve(info);
                    return;
                }
                fail('File upload returned no file');
                return;
            }
            fail(body?.message || `File upload failed with status ${xhr.status}`);
        };
        xhr.onerror = () => fail('File upload failed: network error');
        xhr.ontimeout = () => fail('File upload failed: timed out');
        xhr.onabort = () => reject(new FileUploadAbortedError());

        const formData = new FormData();
        formData.append('channel_id', channelId);
        formData.append('client_ids', clientId);

        // The file goes last so the server can stream it.
        formData.append('files', file, file.name);
        xhr.send(formData);
    });
    return {promise, abort: () => xhr.abort()};
}

export function getFileThumbnailUrl(fileId: string): string {
    return Client4.getFileThumbnailUrl(fileId, 0);
}

export function updateRead(userId: string, teamId: string, selectedPostId: string, timestamp: number) {
    return Client4.updateThreadReadForUser(userId, teamId, selectedPostId, timestamp);
}

export function getProfilePictureUrl(userId: string, lastIconUpdate: number) {
    return Client4.getProfilePictureUrl(userId, lastIconUpdate);
}

// The root posts of chats for the history list's previews (message and reply count). The server
// only returns the posts the user may read.
export function getPostsByIds(postIds: string[]) {
    return Client4.getPostsByIds(postIds);
}

export async function getBotProfilePictureUrl(username: string) {
    const user = await Client4.getUserByUsername(username);
    if (!user || user.id === '') {
        return '';
    }
    return getProfilePictureUrl(user.id, user.last_picture_update);
}

// doRunSearch starts a search conversation with the agent. teamId is the
// user's current team (used for citation links); channelId, when set, is the
// channel the agent searches first.
export async function doRunSearch(query: string, teamId: string, channelId: string, botId?: string): Promise<{postid: string; channelid: string}> {
    const url = `${baseRoute()}/search/run${botIdQuery(botId)}`;
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
        message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
        status_code: response.status,
        url,
    });
}

export async function rebuildVectorIndex() {
    const url = `${baseRoute()}/admin/reindex/rebuild-vector-index`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
        status_code: response.status,
        url,
    });
}

// A draft of a saved remote MCP server's description, taken from how the
// server describes itself to the requesting admin (api.MCPSuggestedDescriptionResponse).
export type MCPSuggestedDescription = {
    description: string;
    source: '' | 'description' | 'instructions';
    needsOAuth: boolean;
    authURL?: string;
};

export async function getMCPSuggestedDescription(serverID: string): Promise<MCPSuggestedDescription> {
    const url = `${baseRoute()}/admin/mcp/${encodeURIComponent(serverID)}/suggested-description`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
        status_code: response.status,
        url,
    });
}

export async function getUserMCPTools(opts?: {
    agentId?: string;
    serviceAccount?: boolean;
}): Promise<UserMCPToolsResponse> {
    const params = new URLSearchParams();
    if (opts?.serviceAccount) {
        params.set('catalog', 'service_account');
    }
    if (opts?.agentId) {
        params.set('agent_id', opts.agentId);
    }
    const query = params.toString();
    const url = `${baseRoute()}/mcp/tools${query ? `?${query}` : ''}`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: await readClientErrorMessage(response),
        status_code: response.status,
        url,
    });
}

export async function refreshUserMCPTools(): Promise<UserMCPToolsResponse> {
    const url = `${baseRoute()}/mcp/tools/refresh`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
        status_code: response.status,
        url,
    });
}

export type ChannelAutoReplyMode = 'off' | 'root_posts' | 'threads' | 'ambient' | 'ambient_threads';

export type ChannelAutoReplySettings = {
    bot_id: string;
    mode: ChannelAutoReplyMode;
    instructions: string;
};

export async function getChannelAutoReply(channelId: string): Promise<ChannelAutoReplySettings> {
    const url = `${channelRoute(channelId)}/autoreply`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: await readClientErrorMessage(response),
        status_code: response.status,
        url,
    });
}

export async function updateChannelAutoReply(channelId: string, settings: ChannelAutoReplySettings): Promise<void> {
    const url = `${channelRoute(channelId)}/autoreply`;
    const response = await fetch(url, Client4.getOptions({
        method: 'PUT',
        body: JSON.stringify(settings),
    }));

    if (response.ok) {
        return;
    }

    throw new ClientError(Client4.url, {
        message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
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
    botId?: string,

    // A username the user typed. The server checks which agent holds it now.
    botUsername?: string,
): Promise<{postid: string; channelid: string}> {
    const agentQuery = botId || !botUsername ? botIdQuery(botId) : `?botUsername=${encodeURIComponent(botUsername)}`;
    const url = `${channelRoute(channelID)}/interval${agentQuery}`;
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
        message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
        status_code: response.status,
        url,
    });
}

// savePluginConfig persists the config and returns the normalized config the
// server saved, including server-minted service/MCP server IDs the payload
// did not have yet.
export async function savePluginConfig(config: PluginConfig): Promise<PluginConfig> {
    const url = `${baseRoute()}/admin/config`;
    const response = await fetch(url, Client4.getOptions({
        method: 'PUT',
        body: JSON.stringify(config),
        headers: {'Content-Type': 'application/json'},
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        ...(await readAgentError(response)),
        status_code: response.status,
        url,
    });
}

// --- Settings access ---

// How much of the plugin configuration a user may reach from the Agents
// product: system admins and read/write delegates 'write', read-only
// delegates 'read'.
export type SettingsAccessLevel = 'none' | 'read' | 'write';

// The users a system admin delegated access to the plugin configuration.
export type SettingsAccess = {
    readOnlyUserIDs: string[];
    readWriteUserIDs: string[];
};

async function settingsRequest<T>(path: string, init: Parameters<typeof Client4.getOptions>[0]): Promise<T> {
    const url = `${baseRoute()}${path}`;
    const response = await fetch(url, Client4.getOptions(init));
    if (response.ok) {
        return response.json();
    }
    throw new ClientError(Client4.url, {
        message: await readClientErrorMessage(response),
        status_code: response.status,
        url,
    });
}

export async function getMySettingsAccess(): Promise<SettingsAccessLevel> {
    const {level} = await settingsRequest<{level: SettingsAccessLevel}>('/settings/access', {method: 'GET'});
    return level;
}

export async function getSettingsAccess(): Promise<SettingsAccess> {
    return settingsRequest<SettingsAccess>('/admin/settings-access', {method: 'GET'});
}

export async function saveSettingsAccess(access: SettingsAccess): Promise<SettingsAccess> {
    return settingsRequest<SettingsAccess>('/admin/settings-access', {
        method: 'PUT',
        body: JSON.stringify(access),
        headers: {'Content-Type': 'application/json'},
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
        message: await readClientErrorMessage(response),
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
        ...(await readAgentError(response)),
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

// fetchAgentJSON sends an authenticated request to an agent route and throws
// a ClientError carrying the server's message for any non-2xx response.
async function fetchAgentJSON<T>(url: string, init: {method: string; body?: string}): Promise<T> {
    const response = await fetch(url, Client4.getOptions(init));
    if (!response.ok) {
        throw new ClientError(Client4.url, {
            message: await readAgentErrorMessage(response),
            status_code: response.status,
            url,
        });
    }
    return response.json();
}

// getAgentVersions fetches one page of versions, newest first: those older
// than `before` (from the newest when omitted), at most `limit` long.
export function getAgentVersions(agentId: string, page: {before?: number; limit?: number} = {}): Promise<AgentVersionList> {
    const params = new URLSearchParams();
    if (typeof page.before === 'number') {
        params.set('before', String(page.before));
    }
    if (typeof page.limit === 'number') {
        params.set('limit', String(page.limit));
    }
    const query = params.toString();
    return fetchAgentJSON(`${agentRoute(agentId)}/versions${query ? `?${query}` : ''}`, {method: 'GET'});
}

export function getAgentVersion(agentId: string, version: number): Promise<AgentVersionDetail> {
    return fetchAgentJSON(`${agentRoute(agentId)}/versions/${version}`, {method: 'GET'});
}

export function restoreAgentVersion(agentId: string, version: number): Promise<UserAgent> {
    return fetchAgentJSON(`${agentRoute(agentId)}/versions/${version}/restore`, {method: 'POST'});
}

// deleteAgentVersions permanently deletes the given versions, or with 'all'
// every version except the current one, and resolves to how many were deleted.
export async function deleteAgentVersions(agentId: string, versions: number[] | 'all'): Promise<number> {
    const body = versions === 'all' ? {all: true} : {versions};
    const result: {deleted: number} = await fetchAgentJSON(`${agentRoute(agentId)}/versions`, {method: 'DELETE', body: JSON.stringify(body)});
    return result.deleted;
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
        message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
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
            message: await readClientErrorMessage(response),
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
            message: await readClientErrorMessage(response),
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
        message: await readClientErrorMessage(response),
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
            message: await readClientErrorMessage(response),
            status_code: response.status,
            url,
        });
    }
}

export async function renderCustomPrompt(id: string, channelId?: string, botId?: string): Promise<{rendered: string}> {
    const url = `${baseRoute()}/custom-prompts/${id}/render`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
        body: JSON.stringify({channel_id: channelId, bot_id: botId}),
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: await readClientErrorMessage(response),
        status_code: response.status,
        url,
    });
}

// The core server's composer rewrite actions (server/public/model/post.go
// RewriteAction), the same list the host composer's AI Rewrite menu offers.
export type RewriteAction = 'shorten' | 'elaborate' | 'improve_writing' | 'fix_spelling' | 'simplify' | 'summarize' | 'custom';

// Reads a core server AppError body ({id, message}). Only the translated
// summary is kept; detailed_error never reaches the UI.
async function readAppError(response: Response): Promise<{message: string; server_error_id?: string}> {
    try {
        const data: unknown = await response.json();
        if (data !== null && typeof data === 'object') {
            const {message, id} = data as {message?: unknown; id?: unknown};
            return {
                message: typeof message === 'string' ? message : '',
                ...(typeof id === 'string' && id ? {server_error_id: id} : {}),
            };
        }
    } catch {
        // Empty or non-JSON body: the caller applies its own fallback text.
    }
    return {message: ''};
}

// Rewrites composer text through the core server's AI rewrite route (the call
// the host composer makes, Client4.getAIRewrittenMessage), which completes
// through this plugin's LLM bridge with the given agent. rootId adds the
// thread as context.
export async function rewriteMessage(
    agentId: string,
    message: string,
    action: RewriteAction,
    opts: {customPrompt?: string; rootId?: string; signal?: AbortSignal} = {},
): Promise<string> {
    const body: {agent_id: string; message: string; action: RewriteAction; custom_prompt?: string; root_id?: string} = {
        agent_id: agentId,
        message,
        action,
    };
    if (opts.customPrompt) {
        body.custom_prompt = opts.customPrompt;
    }
    if (opts.rootId) {
        body.root_id = opts.rootId;
    }
    const url = `${Client4.getPostsRoute()}/rewrite`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
        body: JSON.stringify(body),
        signal: opts.signal,
    }));

    if (response.ok) {
        const data = await response.json() as {rewritten_text?: unknown};
        if (typeof data?.rewritten_text !== 'string') {
            throw new ClientError(Client4.url, {message: '', status_code: response.status, url});
        }
        return data.rewritten_text;
    }

    const err = await readAppError(response);
    throw new ClientError(Client4.url, {
        message: err.message,
        server_error_id: err.server_error_id,
        status_code: response.status,
        url,
    });
}

// Whether the core server can reach this plugin's LLM bridge, which the host
// checks (useGetAgentsBridgeEnabled) before offering AI Rewrite.
export async function getAgentsBridgeStatus(): Promise<{available: boolean; reason?: string}> {
    const url = `${Client4.getAgentsRoute()}/status`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        const data = await response.json() as {available?: unknown; reason?: unknown};
        return {
            available: data?.available === true,
            ...(typeof data?.reason === 'string' && data.reason ? {reason: data.reason} : {}),
        };
    }

    const err = await readAppError(response);
    throw new ClientError(Client4.url, {
        message: err.message,
        status_code: response.status,
        url,
    });
}
