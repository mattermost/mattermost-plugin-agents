// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Helpers for rendering mcphelper's wire-format names:
// "<sanitizedPluginID>__<rawToolName>".

// sanitizeForToolName mirrors public/mcphelper/tools.go:sanitizeForToolName.
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

// stripPluginPrefix removes the "<sanitizedPluginID>__" prefix from toolName
// when present.
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

// stripWirePrefix is a heuristic used at call sites that don't carry server
// context (e.g. the runtime tool-call card, which gets only the wire-format
// name from an LLM response). Strips the leading "<token>__" segment when
// <token> matches the sanitizeForToolName charset.
//
// Safe because the "__" separator is a convention specific to mcphelper's
// prefixing scheme: embedded MCP tools (mcpserver/tools/*.go) and typical
// remote MCP tools name themselves with single-underscore words. If a remote
// server ever ships a tool that legitimately contains "__", this heuristic
// will strip the prefix; the workaround is to pass server context to the
// caller and switch to stripPluginPrefix.
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
