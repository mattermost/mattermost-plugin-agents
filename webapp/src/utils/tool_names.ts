// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Helpers for displaying MCP tool names emitted by external/pluginmcp.

// Mirrors external/pluginmcp/tools.go:sanitizeForToolName.
export function sanitizeForToolName(pluginID: string): string {
    let out = '';
    for (const ch of pluginID) {
        const isLower = ch >= 'a' && ch <= 'z';
        const isUpper = ch >= 'A' && ch <= 'Z';
        const isDigit = ch >= '0' && ch <= '9';
        if (isLower || isUpper || isDigit || ch === '_' || ch === '-') {
            out += ch;
        } else {
            out += '_';
        }
    }
    return out;
}

export function pluginIDFromServerOrigin(serverOrigin: string): string {
    const scheme = 'plugin://';
    if (!serverOrigin.startsWith(scheme)) {
        return '';
    }
    const rest = serverOrigin.slice(scheme.length);
    const slash = rest.indexOf('/');
    return slash === -1 ? rest : rest.slice(0, slash);
}

export function stripPluginPrefix(toolName: string, pluginID: string): string {
    if (!pluginID) {
        return toolName;
    }
    const prefix = sanitizeForToolName(pluginID) + '__';
    if (toolName.startsWith(prefix)) {
        return toolName.slice(prefix.length);
    }
    return toolName;
}

// Heuristic prefix strip for call sites without server context.
export function stripWirePrefix(toolName: string): string {
    const idx = toolName.indexOf('__');
    if (idx <= 0) {
        return toolName;
    }
    const prefix = toolName.slice(0, idx);
    if (!(/^[a-zA-Z0-9_-]+$/).test(prefix)) {
        return toolName;
    }
    return toolName.slice(idx + 2);
}
