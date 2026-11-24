// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export type SearchableMCPTool = {
    name: string;
    displayName?: string;
    description: string;
    retrievalDescriptionOverride?: string;
};

export type SearchableMCPServer<TTool extends SearchableMCPTool = SearchableMCPTool> = {
    name: string;
    tools: TTool[];
};

export type FilteredMCPServer<TServer extends SearchableMCPServer> = {
    server: TServer;
    matchedTools: Array<TServer['tools'][number]>;

    /** True when the server's tools were narrowed to a subset matching the query. */
    isFiltered: boolean;
};

/**
 * Filters MCP servers and their tools by a search query.
 *
 * - Empty or whitespace-only query: every server with all of its tools.
 * - Queries shorter than 3 characters (after trim): match server name and tool names
 *   (display name or wire name) only, so short substrings in long prose do not surface
 *   unrelated servers.
 * - Queries of 3+ characters: also match tool descriptions and retrieval description overrides.
 * - When a server's name matches, all of its tools stay visible (`isFiltered: false`).
 *   Otherwise, only matching tools are returned (`isFiltered: true`).
 */
export function filterMCPServers<TServer extends SearchableMCPServer>(
    servers: TServer[],
    searchQuery: string,
): Array<FilteredMCPServer<TServer>> {
    const trimmed = searchQuery.trim();
    if (!trimmed) {
        return servers.map((server) => ({
            server,
            matchedTools: server.tools as Array<TServer['tools'][number]>,
            isFiltered: false,
        }));
    }

    const q = trimmed.toLowerCase();
    const useDescriptions = q.length >= 3;
    const results: Array<FilteredMCPServer<TServer>> = [];

    for (const server of servers) {
        if (server.name.toLowerCase().includes(q)) {
            results.push({
                server,
                matchedTools: server.tools as Array<TServer['tools'][number]>,
                isFiltered: false,
            });
            continue;
        }

        const matchedTools = (server.tools as Array<TServer['tools'][number]>).filter((tool) => {
            if (tool.name.toLowerCase().includes(q)) {
                return true;
            }
            if (tool.displayName && tool.displayName.toLowerCase().includes(q)) {
                return true;
            }
            if (!useDescriptions) {
                return false;
            }
            if (tool.description.toLowerCase().includes(q)) {
                return true;
            }
            if (tool.retrievalDescriptionOverride && tool.retrievalDescriptionOverride.toLowerCase().includes(q)) {
                return true;
            }
            return false;
        });

        if (matchedTools.length > 0) {
            results.push({
                server,
                matchedTools,
                isFiltered: true,
            });
        }
    }

    return results;
}
