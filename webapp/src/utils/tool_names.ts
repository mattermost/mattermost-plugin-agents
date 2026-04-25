// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Helpers for displaying MCP tool names emitted by public/mcphelper.

// Mirrors public/mcphelper/tools.go:sanitizeForToolName.
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

// pluginIDFromServerOrigin parses "plugin://<pluginID><optional-path>" and
// returns the pluginID portion. Returns "" for non-plugin origins.
export function pluginIDFromServerOrigin(serverOrigin: string): string {
    const scheme = 'plugin://';
    if (!serverOrigin.startsWith(scheme)) {
        return '';
    }
    const rest = serverOrigin.slice(scheme.length);
    const slash = rest.indexOf('/');
    return slash === -1 ? rest : rest.slice(0, slash);
}

// stripPluginPrefix removes the "<sanitizedPluginID>__" prefix when present.
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

// stripWirePrefix is a heuristic for call sites that only have a wire-format
// name and no server context.
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
