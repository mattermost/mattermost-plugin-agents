// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Canonical tool identity helpers shared by the tool cards and the renderer
// registry. Identity is derived from server origin + bare name so a call
// routes and renders the same live and after reload.

import {ToolCall} from '@/components/tool_types';
import {stripWirePrefix} from '@/utils/tool_names';

// Mirrors mcp.EmbeddedClientKey on the server.
const EmbeddedServerOrigin = 'embedded://mattermost';

// Mirrors mcp.pluginServerOriginKey ("plugin://<id>").
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

/** Prettify a bare tool name: underscores → spaces, Title Case each word. */
function prettifyBareName(bareName: string): string {
    return bareName.
        replace(/_/g, ' ').
        split(' ').
        map((word) => (word ? word.charAt(0).toUpperCase() + word.slice(1) : word)).
        join(' ');
}

/**
 * The display name for a tool call: the MCP-supplied title when present
 * (sanitized server-side), otherwise the prettified bare name — matching the
 * pre-title behavior so built-in/embedded names are unchanged.
 */
export function toolDisplayName(tool: Pick<ToolCall, 'name' | 'mcp_bare_name' | 'title'>): string {
    if (tool.title) {
        return tool.title;
    }
    return prettifyBareName(bareToolName(tool));
}
