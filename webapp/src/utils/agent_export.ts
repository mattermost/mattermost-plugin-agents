// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {IntlShape} from 'react-intl';

import {getAgentVersion, getAgentVersions, type UserMCPServerInfo} from '@/client';
import {
    AgentExportAgent,
    AgentExportDocument,
    AgentExportKind,
    AgentExportMCPTool,
    AgentExportSchemaVersion,
    EnabledTool,
    UserAgent,
} from '@/types/agents';

// Far above any valid export, so larger files are rejected before they are read.
const MAX_AGENT_FILE_BYTES = 2 * 1024 * 1024;

// normalizeMCPOrigin matches llm.NormalizeMCPServerOrigin on the server.
function normalizeMCPOrigin(origin: string): string {
    return origin.trim().replace(/\/+$/, '');
}

function uniqueTools(tools: AgentExportMCPTool[]): AgentExportMCPTool[] {
    const seen = new Set<string>();
    return tools.filter((tool) => {
        const key = `${tool.serverOrigin}\n${tool.toolName}`;
        if (seen.has(key)) {
            return false;
        }
        seen.add(key);
        return true;
    });
}

// buildAgentExport returns the export document of an agent configuration.
export function buildAgentExport(config: Partial<UserAgent>, agentVersion: number, exportedAt: number): AgentExportDocument {
    const tools = (config.enabledMCPTools ?? []).map((t) => ({
        serverOrigin: normalizeMCPOrigin(t.server_origin),
        toolName: t.tool_name.trim(),
    }));
    return {
        kind: AgentExportKind,
        schemaVersion: AgentExportSchemaVersion,
        exportedAt,
        agentVersion,
        agent: {
            name: config.name ?? '',
            displayName: config.displayName ?? '',
            customInstructions: config.customInstructions ?? '',
            disableTools: config.disableTools ?? false,
            maxToolTurns: config.maxToolTurns ?? 0,
            mcpDynamicToolLoading: config.mcpDynamicToolLoading ?? true,
            autoEnableNewMCPTools: config.autoEnableNewMCPTools ?? false,
            mcpTools: uniqueTools(tools),
        },
    };
}

/**
 * Downloads the export document of the agent's current version. The
 * configuration and its number come from one stored version, so a concurrent
 * save cannot label one version's settings with another's number.
 */
export async function downloadAgentExport(agentId: string): Promise<void> {
    const {currentVersion} = await getAgentVersions(agentId, {limit: 1});
    if (currentVersion < 1) {
        throw new Error('the agent has no saved version to export');
    }
    const {config} = await getAgentVersion(agentId, currentVersion);
    const doc = buildAgentExport(config, currentVersion, Date.now());

    const blob = new Blob([JSON.stringify(doc, null, 2)], {type: 'application/json'});
    const url = URL.createObjectURL(blob);
    try {
        const link = document.createElement('a');
        link.href = url;
        link.download = `${doc.agent.name || 'agent'}-v${currentVersion}.agent.json`;
        link.hidden = true;
        document.body.appendChild(link);
        link.click();
        document.body.removeChild(link);
    } finally {
        // Defer revocation so the browser has started the download.
        window.setTimeout(() => URL.revokeObjectURL(url), 0);
    }
}

function readFileText(file: File): Promise<string> {
    return new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(typeof reader.result === 'string' ? reader.result : '');
        reader.onerror = () => reject(reader.error ?? new Error('failed to read the file'));
        reader.readAsText(file);
    });
}

function isRecord(value: unknown): value is Record<string, unknown> {
    return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function readString(value: unknown): string | null {
    if (typeof value === 'undefined') {
        return '';
    }
    return typeof value === 'string' ? value : null;
}

function readBoolean(value: unknown, fallback: boolean): boolean | null {
    if (typeof value === 'undefined') {
        return fallback;
    }
    return typeof value === 'boolean' ? value : null;
}

function parseAgent(value: unknown): AgentExportAgent | null {
    if (!isRecord(value)) {
        return null;
    }
    const name = readString(value.name);
    const displayName = readString(value.displayName);
    const customInstructions = readString(value.customInstructions);
    const disableTools = readBoolean(value.disableTools, false);
    const autoEnableNewMCPTools = readBoolean(value.autoEnableNewMCPTools, false);

    // Files written before this field existed run with dynamic loading on.
    const mcpDynamicToolLoading = readBoolean(value.mcpDynamicToolLoading, true);
    const maxToolTurns = typeof value.maxToolTurns === 'undefined' ? 0 : value.maxToolTurns;
    const rawTools = value.mcpTools ?? [];
    if (name === null || displayName === null || customInstructions === null || disableTools === null ||
        autoEnableNewMCPTools === null || mcpDynamicToolLoading === null ||
        typeof maxToolTurns !== 'number' || !Number.isInteger(maxToolTurns) || !Array.isArray(rawTools)) {
        return null;
    }

    const mcpTools: AgentExportMCPTool[] = [];
    for (const tool of rawTools) {
        if (!isRecord(tool) || typeof tool.serverOrigin !== 'string' || typeof tool.toolName !== 'string') {
            return null;
        }
        const serverOrigin = normalizeMCPOrigin(tool.serverOrigin);
        const toolName = tool.toolName.trim();
        if (!serverOrigin || !toolName) {
            return null;
        }
        mcpTools.push({serverOrigin, toolName});
    }

    return {
        name,
        displayName,
        customInstructions,
        disableTools,
        maxToolTurns,
        mcpDynamicToolLoading,
        autoEnableNewMCPTools,
        mcpTools: uniqueTools(mcpTools),
    };
}

/**
 * Reads and validates an agent export file. It rejects with an Error whose
 * message is ready to show when the file is not a usable export. Field limits
 * (instruction length, tool turns) are checked when the agent is saved.
 */
export async function readAgentExportFile(file: File, intl: IntlShape): Promise<AgentExportDocument> {
    const invalid = () => new Error(intl.formatMessage({defaultMessage: 'This file is not a valid Mattermost agent export.'}));
    if (file.size > MAX_AGENT_FILE_BYTES) {
        throw new Error(intl.formatMessage({defaultMessage: 'This file is too large to be an agent export.'}));
    }

    let parsed: unknown;
    try {
        parsed = JSON.parse(await readFileText(file));
    } catch {
        throw invalid();
    }
    if (!isRecord(parsed) || parsed.kind !== AgentExportKind) {
        throw invalid();
    }
    if (parsed.schemaVersion !== AgentExportSchemaVersion) {
        throw new Error(intl.formatMessage(
            {defaultMessage: 'This agent file uses format version {version}, but this server reads version {supported}.'},
            {version: String(parsed.schemaVersion), supported: AgentExportSchemaVersion},
        ));
    }
    const agent = parseAgent(parsed.agent);
    if (!agent) {
        throw invalid();
    }
    return {
        kind: AgentExportKind,
        schemaVersion: AgentExportSchemaVersion,
        exportedAt: typeof parsed.exportedAt === 'number' ? parsed.exportedAt : 0,
        agentVersion: typeof parsed.agentVersion === 'number' ? parsed.agentVersion : 0,
        agent,
    };
}

/**
 * Maps the document's MCP tools onto the given servers (those the importing
 * user's MCPs tab lists) by origin, ignoring a trailing '/'. Tools take the
 * server's configured origin; tools of servers that are not listed are left
 * out and their origins returned.
 */
export function matchImportedMCPTools(tools: AgentExportMCPTool[], servers: UserMCPServerInfo[]): {enabledTools: EnabledTool[]; missingServerOrigins: string[]} {
    const configured = new Map<string, string>();
    for (const server of servers) {
        const key = normalizeMCPOrigin(server.serverOrigin);
        if (key && !configured.has(key)) {
            configured.set(key, server.serverOrigin);
        }
    }

    const enabledTools: EnabledTool[] = [];
    const missing = new Set<string>();
    for (const tool of tools) {
        const origin = configured.get(tool.serverOrigin);
        if (origin) {
            enabledTools.push({server_origin: origin, tool_name: tool.toolName});
        } else {
            missing.add(tool.serverOrigin);
        }
    }
    return {enabledTools, missingServerOrigins: Array.from(missing)};
}
