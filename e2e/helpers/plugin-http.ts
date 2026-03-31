/**
 * HTTP helpers for Mattermost plugin APIs (admin config and plugin routes).
 * Use these instead of Client4 getConfig/patchConfig or ad hoc fetch strings for
 * mattermost-plugin-agents endpoints.
 */

import { Client4 } from '@mattermost/client';

export const MATTERMOST_AI_PLUGIN_ID = 'mattermost-ai';

const DEFAULT_PUT_SETTLE_MS = 500;

function stripTrailingSlash(url: string): string {
    return url.replace(/\/$/, '');
}

async function readErrorBody(response: Response): Promise<string> {
    try {
        return await response.text();
    } catch {
        return response.statusText;
    }
}

/**
 * GET/PUT /plugins/{pluginId}/admin/config — database-backed plugin configuration.
 */
export class PluginAdminConfigApi {
    constructor(
        private readonly baseUrl: string,
        private readonly getToken: () => string,
        private readonly pluginId: string,
    ) {}

    private adminConfigUrl(): string {
        return `${stripTrailingSlash(this.baseUrl)}/plugins/${this.pluginId}/admin/config`;
    }

    async get(): Promise<Record<string, unknown>> {
        const response = await fetch(this.adminConfigUrl(), {
            method: 'GET',
            headers: {
                Authorization: `Bearer ${this.getToken()}`,
            },
        });

        if (!response.ok) {
            const text = await readErrorBody(response);
            throw new Error(`Plugin ${this.pluginId} configuration not found: ${response.status} ${text}`);
        }

        return (await response.json()) as Record<string, unknown>;
    }

    /**
     * @param options.settleMs - Wait after success so listeners can apply (install uses a longer delay).
     */
    async put(config: Record<string, unknown>, options?: { settleMs?: number }): Promise<void> {
        const response = await fetch(this.adminConfigUrl(), {
            method: 'PUT',
            headers: {
                'Content-Type': 'application/json',
                Authorization: `Bearer ${this.getToken()}`,
            },
            body: JSON.stringify(config),
        });

        if (!response.ok) {
            const text = await readErrorBody(response);
            throw new Error(`Failed to update plugin config: ${response.status} ${text}`);
        }

        const settleMs = options?.settleMs ?? DEFAULT_PUT_SETTLE_MS;
        await new Promise((resolve) => setTimeout(resolve, settleMs));
    }
}

export function pluginAdminConfigApiFromClient(
    client: Client4,
    baseUrl: string,
    pluginId: string,
): PluginAdminConfigApi {
    return new PluginAdminConfigApi(baseUrl, () => client.getToken(), pluginId);
}

export function mattermostAIAdminConfigApiFromClient(client: Client4, baseUrl: string): PluginAdminConfigApi {
    return pluginAdminConfigApiFromClient(client, baseUrl, MATTERMOST_AI_PLUGIN_ID);
}

/**
 * Normalizes GET /admin/config JSON for helpers that expect non-null collections.
 */
export function normalizeMattermostAiConfigFromApi(apiConfig: Record<string, unknown>): Record<string, unknown> {
    return {
        ...apiConfig,
        bots: apiConfig.bots ?? [],
        services: apiConfig.services ?? [],
        mcp: apiConfig.mcp ?? {},
    };
}

/**
 * Authenticated requests under /plugins/{pluginId}/ (non-admin routes, e.g. mcp/tools, search).
 */
export class PluginRoutesApi {
    constructor(
        private readonly baseUrl: string,
        private readonly pluginId: string = MATTERMOST_AI_PLUGIN_ID,
    ) {}

    pluginUrl(path: string): string {
        const p = path.replace(/^\//, '');
        return `${stripTrailingSlash(this.baseUrl)}/plugins/${this.pluginId}/${p}`;
    }

    async getJson(path: string, token: string): Promise<unknown> {
        const response = await fetch(this.pluginUrl(path), {
            headers: {
                Authorization: `Bearer ${token}`,
            },
        });
        if (!response.ok) {
            throw new Error(`${path} failed: ${response.status} ${response.statusText}`);
        }
        return response.json();
    }

    async postJson(path: string, token: string, body: unknown): Promise<unknown> {
        const response = await fetch(this.pluginUrl(path), {
            method: 'POST',
            headers: {
                Authorization: `Bearer ${token}`,
                'Content-Type': 'application/json',
            },
            body: JSON.stringify(body),
        });
        if (!response.ok) {
            const text = await readErrorBody(response);
            throw new Error(`${path} failed: ${response.status} ${text}`);
        }
        return response.json();
    }

    async putJson(path: string, token: string, body: unknown): Promise<unknown> {
        const response = await fetch(this.pluginUrl(path), {
            method: 'PUT',
            headers: {
                Authorization: `Bearer ${token}`,
                'Content-Type': 'application/json',
            },
            body: JSON.stringify(body),
        });
        if (!response.ok) {
            const text = await readErrorBody(response);
            throw new Error(`${path} failed: ${response.status} ${text}`);
        }
        return response.json();
    }
}

export function mattermostAIPluginRoutes(baseUrl: string): PluginRoutesApi {
    return new PluginRoutesApi(baseUrl, MATTERMOST_AI_PLUGIN_ID);
}
