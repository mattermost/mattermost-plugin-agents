// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

/**
 * Tool row shape used by MCP server search (matches GET /mcp/tools user response).
 */
export type McpsSearchToolRow = {
    name: string;
    description: string;
    enabled: boolean;
};

/**
 * Server row shape used by MCP server search.
 */
export type McpsSearchServerRow = {
    name: string;
    tools: McpsSearchToolRow[];
};

/**
 * Returns servers visible for the given search query.
 *
 * - Empty or whitespace-only query: all servers (unchanged ordering).
 * - Queries shorter than 3 characters (after trim): match server name and **enabled**
 *   tool names only (not descriptions), so short substrings in long prose do not surface
 *   unrelated servers.
 * - Queries of 3+ characters: also match against **enabled** tool descriptions.
 *
 * Disabled admin-level tools never participate in search matching.
 */
export function filterMcpsServersBySearchQuery<T extends McpsSearchServerRow>(
    servers: T[],
    searchQuery: string,
): T[] {
    const trimmed = searchQuery.trim();
    if (!trimmed) {
        return servers;
    }
    const q = trimmed.toLowerCase();
    const useDescriptions = q.length >= 3;
    return servers.filter((server) => {
        const searchableTools = server.tools.filter((t) => t.enabled);
        const nameMatches = server.name.toLowerCase().includes(q);
        const toolNameMatches = searchableTools.some((t) => t.name.toLowerCase().includes(q));
        if (!useDescriptions) {
            return nameMatches || toolNameMatches;
        }
        return nameMatches ||
            toolNameMatches ||
            searchableTools.some((t) => t.description.toLowerCase().includes(q));
    });
}
