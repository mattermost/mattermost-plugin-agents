// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Canonical tool identity helpers shared by the tool cards and the (Phase 3)
// renderer registry. Identity is derived from the tool's server origin and bare
// (unprefixed) name so a call routes and renders the same whether it arrives
// live over the websocket or is rehydrated from the conversation API.

import {ToolCall} from '@/components/tool_types';
import {stripWirePrefix} from '@/utils/tool_names';

// EmbeddedServerOrigin mirrors mcp.EmbeddedClientKey on the server: the
// ServerOrigin stamped on tools from the embedded Mattermost MCP server.
export const EmbeddedServerOrigin = 'embedded://mattermost';

// PluginServerOriginPrefix mirrors mcp.pluginServerOriginKey ("plugin://<id>").
const PluginServerOriginPrefix = 'plugin://';

export type ToolOriginKind = 'builtin' | 'embedded' | 'plugin' | 'external';

/**
 * Classify a tool's server origin. Empty/undefined is a built-in (non-MCP)
 * tool; "embedded://mattermost" is the embedded server; "plugin://…" is a
 * plugin MCP server; anything else (a URL) is an external MCP server.
 */
export function originKind(serverOrigin?: string): ToolOriginKind {
    if (!serverOrigin) {
        return 'builtin';
    }
    if (serverOrigin === EmbeddedServerOrigin) {
        return 'embedded';
    }
    if (serverOrigin.startsWith(PluginServerOriginPrefix)) {
        return 'plugin';
    }
    return 'external';
}

/**
 * The unprefixed tool name. Prefers the server-supplied mcp_bare_name; falls
 * back to the heuristic wire-prefix strip for legacy persisted data that
 * predates mcp_bare_name plumbing.
 */
export function bareToolName(tool: Pick<ToolCall, 'name' | 'mcp_bare_name'>): string {
    return tool.mcp_bare_name || stripWirePrefix(tool.name);
}

/**
 * A stable identity key for a tool call: origin kind + bare name. Used by the
 * renderer registry to match calls to rich cards regardless of MCP server
 * namespacing.
 */
export function canonicalToolKey(tool: Pick<ToolCall, 'name' | 'mcp_bare_name' | 'server_origin'>): string {
    return `${originKind(tool.server_origin)}:${bareToolName(tool)}`;
}

/** Prettify a bare tool name: underscores → spaces, Title Case each word. */
function prettifyBareName(bareName: string): string {
    return bareName.
        replace(/_/g, ' ').
        split(' ').
        map((word) => (word ? word.charAt(0).toUpperCase() + word.slice(1) : word)).
        join(' ');
}

/**
 * The human-readable display name for a tool call. Uses the MCP-supplied title
 * when present (already Unicode-sanitized and effective-title-resolved
 * server-side); otherwise prettifies the bare name exactly as the tool cards
 * did before titles existed — so built-in and embedded tool names (and every
 * e2e assertion on them) are unchanged.
 */
export function toolDisplayName(tool: Pick<ToolCall, 'name' | 'mcp_bare_name' | 'title'>): string {
    if (tool.title) {
        return tool.title;
    }
    return prettifyBareName(bareToolName(tool));
}
