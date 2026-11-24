// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {IntlShape} from 'react-intl';

import {fetchAgentDocument, getAgentVersion, getAgentVersions, type UserMCPServerInfo} from '@/client';
import {
    AgentExportAgent,
    AgentExportDocument,
    AgentExportDocumentFile,
    AgentExportKind,
    AgentExportMCPTool,
    AgentExportSchemaVersion,
    AgentExportSchemaVersions,
    MaxAgentDocumentBytes,
    MaxAgentDocumentNameLength,
    MaxAgentDocuments,
    EnabledTool,
    MaxSkillDescriptionRunes,
    SkillsConfig,
    UserAgent,
    codePointLength,
    normalizeSkillsConfig,
} from '@/types/agents';

import {saveBlobAsFile} from './download_blob';

// Far above any valid export (documents are base64, so up to 25 MiB of them
// take about a third more), so larger files are rejected before they are read.
const MAX_AGENT_FILE_BYTES = 40 * 1024 * 1024;

const SHA256_PATTERN = /^[0-9a-f]{64}$/;

// Matches model.IsValidId on the server, which validates skill IDs.
const SKILL_ID_PATTERN = /^[a-z0-9]{26}$/;

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
export function buildAgentExport(config: Partial<UserAgent>, agentVersion: number, exportedAt: number, documents: AgentExportDocumentFile[] = []): AgentExportDocument {
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
            skills: normalizeSkillsConfig(config.skills),
            documents,
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
    const documents: AgentExportDocumentFile[] = [];
    for (const ref of config.documents ?? []) {
        // Sequential so the file keeps the agent's document order.
        // eslint-disable-next-line no-await-in-loop
        const content = await readBlobBase64(await fetchAgentDocument(agentId, ref.id));
        documents.push({name: ref.name, mimeType: ref.mimeType, size: ref.size, sha256: ref.sha256, content});
    }
    const doc = buildAgentExport(config, currentVersion, Date.now(), documents);

    const blob = new Blob([JSON.stringify(doc, null, 2)], {type: 'application/json'});
    saveBlobAsFile(blob, `${doc.agent.name || 'agent'}-v${currentVersion}.agent.json`);
}

function readBlobBase64(blob: Blob): Promise<string> {
    return new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => {
            const result = typeof reader.result === 'string' ? reader.result : '';
            resolve(result.slice(result.indexOf(',') + 1));
        };
        reader.onerror = () => reject(reader.error ?? new Error('failed to read the document'));
        reader.readAsDataURL(blob);
    });
}

function decodeBase64(content: string): Uint8Array | null {
    try {
        const binary = atob(content);
        const bytes = new Uint8Array(binary.length);
        for (let i = 0; i < binary.length; i++) {
            bytes[i] = binary.charCodeAt(i);
        }
        return bytes;
    } catch {
        return null;
    }
}

async function sha256Hex(bytes: Uint8Array): Promise<string | null> {
    // Only available in secure contexts; the server hashes uploads itself.
    if (!globalThis.crypto?.subtle) {
        return null;
    }
    const digest = new Uint8Array(await globalThis.crypto.subtle.digest('SHA-256', bytes as BufferSource));
    return Array.from(digest, (b) => b.toString(16).padStart(2, '0')).join('');
}

// parseDocuments returns null when a document is malformed or does not match
// its declared size or hash.
async function parseDocuments(value: unknown): Promise<AgentExportDocumentFile[] | null> {
    if (typeof value === 'undefined' || value === null) {
        return [];
    }
    if (!Array.isArray(value) || value.length > MaxAgentDocuments) {
        return null;
    }

    const documents: AgentExportDocumentFile[] = [];
    for (const raw of value) {
        if (!isRecord(raw) || typeof raw.name !== 'string' || typeof raw.mimeType !== 'string' ||
            typeof raw.size !== 'number' || typeof raw.sha256 !== 'string' || typeof raw.content !== 'string') {
            return null;
        }
        const name = raw.name.trim();
        if (!name || codePointLength(name) > MaxAgentDocumentNameLength || !SHA256_PATTERN.test(raw.sha256) ||
            !Number.isInteger(raw.size) || raw.size <= 0 || raw.size > MaxAgentDocumentBytes) {
            return null;
        }
        const bytes = decodeBase64(raw.content);
        if (!bytes || bytes.length !== raw.size) {
            return null;
        }
        // eslint-disable-next-line no-await-in-loop
        const hash = await sha256Hex(bytes);
        if (hash !== null && hash !== raw.sha256) {
            return null;
        }
        documents.push({name, mimeType: raw.mimeType, size: raw.size, sha256: raw.sha256, content: raw.content});
    }
    return documents;
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

// parseSkills returns null when the section is malformed; files written
// before skills existed get the defaults.
function parseSkills(value: unknown): SkillsConfig | null {
    if (typeof value === 'undefined') {
        return normalizeSkillsConfig();
    }
    if (!isRecord(value)) {
        return null;
    }
    const disabled = readBoolean(value.disabled, false);
    const allowPersonal = readBoolean(value.allowPersonal, false);
    const rawIDs = value.enabledSharedIDs ?? [];
    const rawOverrides = value.descriptionOverrides ?? {};
    if (disabled === null || allowPersonal === null || !Array.isArray(rawIDs) || !isRecord(rawOverrides)) {
        return null;
    }

    const enabledSharedIDs: string[] = [];
    for (const id of rawIDs) {
        if (typeof id !== 'string' || !SKILL_ID_PATTERN.test(id)) {
            return null;
        }
        if (!enabledSharedIDs.includes(id)) {
            enabledSharedIDs.push(id);
        }
    }

    const descriptionOverrides: Record<string, string> = {};
    for (const [id, description] of Object.entries(rawOverrides)) {
        if (!SKILL_ID_PATTERN.test(id) || typeof description !== 'string' || codePointLength(description) > MaxSkillDescriptionRunes) {
            return null;
        }
        if (description.trim()) {
            descriptionOverrides[id] = description;
        }
    }

    return {disabled, allowPersonal, enabledSharedIDs, descriptionOverrides};
}

async function parseAgent(value: unknown, schemaVersion: number): Promise<AgentExportAgent | null> {
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
    const skills = parseSkills(value.skills);

    // Version 1 files have no documents section.
    const documents = schemaVersion >= 2 ? await parseDocuments(value.documents) : [];
    if (skills === null || documents === null || name === null || displayName === null || customInstructions === null || disableTools === null ||
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
        skills,
        documents,
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
    if (typeof parsed.schemaVersion !== 'number' || !AgentExportSchemaVersions.includes(parsed.schemaVersion)) {
        throw new Error(intl.formatMessage(
            {defaultMessage: 'This agent file uses format version {version}, but this server reads version {supported}.'},
            {version: String(parsed.schemaVersion), supported: AgentExportSchemaVersions.join(', ')},
        ));
    }
    const agent = await parseAgent(parsed.agent, parsed.schemaVersion);
    if (!agent) {
        throw invalid();
    }
    return {
        kind: AgentExportKind,
        schemaVersion: parsed.schemaVersion,
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
