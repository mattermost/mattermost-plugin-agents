import fs from 'fs';
import path from 'path';

import { expect, Page } from '@playwright/test';
import { containerTest } from 'helpers/container-fixture';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { OpenAIMockContainer, RunOpenAIMocks, responseTest } from 'helpers/openai-mock';
import { MockMCPTool, registerMCPToolsServerMocks } from 'helpers/mcp-tools-mock';
import {
    RunAgentContainer,
    agentAdminUsername, agentAdminPassword,
    agentRegularUsername, agentRegularPassword,
    agentUnprivilegedUsername, agentUnprivilegedPassword,
    mockServiceId, secondServiceId,
} from 'helpers/agent-container';
import { AgentAPIHelper, AgentExportDocument, AgentResponse, EnabledTool } from 'helpers/agent-api';
import { AgentPageHelper } from 'helpers/agent-page';
import { mattermostAIAdminConfigApiFromClient, mattermostAIPluginRoutes } from 'helpers/plugin-http';

/** Matches mcp.EmbeddedClientKey — MCP server origin for the embedded Mattermost tools server. */
const embeddedMattermostOrigin = 'embedded://mattermost';

// A remote MCP server configured on the instance but disabled, so nothing tries
// to connect to it and the MCPs tab does not list it.
const jiraServerName = 'Jira';
const jiraOrigin = 'https://mcp.example.com/mcp';
const goneOrigin = 'https://gone.example.com/mcp';

// An enabled MCP server served by the Smocker container. Its configured BaseURL
// keeps a trailing slash, which the normalized origin used for matching drops.
const trackerServerName = 'Tracker';
const trackerMockPath = '/mcp-tools/';
const trackerOrigin = `http://openai:8080${trackerMockPath}`;
const trackerNormalizedOrigin = trackerOrigin.replace(/\/$/, '');
const trackerTools: MockMCPTool[] = [
    { name: 'create_issue', description: 'Create an issue' },
    { name: 'search_issues', description: 'Search issues' },
];

const avatarFixture = path.join(__dirname, '..', '..', '..', 'assets', 'bot_icon.png');

// Keys that must never appear anywhere in an export document: server-specific
// settings, access rules, identities and lifecycle metadata.
const forbiddenExportKeys = [
    'id', 'serviceID', 'service', 'model', 'enableVision', 'reasoningEnabled', 'reasoningEffort',
    'thinkingBudget', 'enabledNativeTools', 'channelAccessLevel', 'channelIDs', 'userAccessLevel',
    'userIDs', 'teamIDs', 'adminUserIDs', 'creatorID', 'botUserID', 'useServiceAccountAuth',
    'createAt', 'updateAt', 'deleteAt', 'enabledMCPTools',
];

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

function collectKeys(value: unknown, keys = new Set<string>()): Set<string> {
    if (Array.isArray(value)) {
        value.forEach((item) => collectKeys(item, keys));
    } else if (value && typeof value === 'object') {
        for (const [key, child] of Object.entries(value)) {
            keys.add(key);
            collectKeys(child, keys);
        }
    }
    return keys;
}

function sortTools(tools: EnabledTool[] | undefined | null): EnabledTool[] {
    return [...(tools ?? [])].sort((a, b) =>
        `${a.server_origin}/${a.tool_name}`.localeCompare(`${b.server_origin}/${b.tool_name}`));
}

function exportDocument(agent: Partial<AgentExportDocument['agent']> & { name: string }): AgentExportDocument {
    return {
        kind: 'mattermost-agent',
        schemaVersion: 1,
        exportedAt: Date.now(),
        agentVersion: 4,
        agent: {
            displayName: agent.name,
            customInstructions: '',
            disableTools: false,
            maxToolTurns: 20,
            mcpDynamicToolLoading: true,
            autoEnableNewMCPTools: false,
            mcpTools: [],
            ...agent,
        },
    };
}

async function adminToken(): Promise<string> {
    const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
    return adminClient.getToken();
}

async function findAgentByName(agentApi: AgentAPIHelper, token: string, name: string): Promise<AgentResponse | undefined> {
    const agents = await agentApi.getAgents(token);
    return agents.find((a) => a.name === name);
}

async function loginToAgents(page: Page): Promise<AgentPageHelper> {
    const mmPage = new MattermostPage(page);
    const agentPage = new AgentPageHelper(page);
    await mmPage.login(mattermost.url(), agentAdminUsername, agentAdminPassword);
    await agentPage.navigateToAgents(mattermost.url());
    return agentPage;
}

/** Runs trigger and returns the downloaded file's name, path and parsed JSON. */
async function captureDownload(page: Page, trigger: () => Promise<void>): Promise<{
    filename: string;
    filePath: string;
    doc: AgentExportDocument;
}> {
    const [download] = await Promise.all([
        page.waitForEvent('download', { timeout: 15000 }),
        trigger(),
    ]);
    const filePath = await download.path();
    expect(filePath).toBeTruthy();
    const doc = JSON.parse(fs.readFileSync(filePath, 'utf8')) as AgentExportDocument;
    return { filename: download.suggestedFilename(), filePath, doc };
}

const test = containerTest(async (own) => {
    const mattermost = own(await RunAgentContainer({
        mcpServers: [
            { name: jiraServerName, enabled: false, baseURL: jiraOrigin },
            { name: trackerServerName, enabled: true, baseURL: trackerOrigin },
        ],
    }));
    const openAIMock = own(await RunOpenAIMocks(mattermost.network));
    await openAIMock.addCompletionMock(responseTest);
    await registerMCPToolsServerMocks(openAIMock, trackerMockPath, trackerTools);
    return { mattermost, openAIMock };
});

test.describe('Agent versioning, export and import', () => {
    test.beforeAll(async ({ containers }) => {
        ({ mattermost, openAIMock } = containers);
    });

    test('the version history modal lists every save and shows an old version read-only', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const token = await adminToken();
        const v1 = 'You are the original history agent.\n  Keep this indentation.';
        const v2 = 'You are the second history agent.';
        const v3 = 'You are the third history agent.';

        const agent = await agentApi.createTestAgent(token, {
            displayName: 'History Agent',
            username: 'historyagent',
            serviceID: mockServiceId,
            customInstructions: v1,
        });

        const agentPage = await loginToAgents(page);
        for (const instructions of [v2, v3]) {
            await agentPage.openAgentEditor('History Agent');
            await agentPage.getCustomInstructionsInput().fill(instructions);
            await agentPage.getModalSaveButton().click();
            await agentPage.waitForModalClosed();
        }

        await agentPage.openAgentEditor('History Agent');

        // History is a version line by the title, not a tab.
        await expect(agentPage.getModalTab('Configuration')).toBeVisible();
        await expect(page.getByRole('tab', { name: 'History', exact: true })).toHaveCount(0);
        await expect(agentPage.getVersionLine()).toContainText('v3');
        await expect(agentPage.getVersionLine()).toContainText(agentAdminUsername);
        await expect(agentPage.getVersionHistoryButton()).toBeVisible();
        await agentPage.openVersionHistory();

        await expect(agentPage.getVersionItems()).toHaveCount(3, { timeout: 10000 });
        await expect(agentPage.getVersionItems().first()).toContainText('Version 3');
        await expect(agentPage.getVersionItem(3)).toContainText('Current');
        await expect(agentPage.getVersionItem(2)).not.toContainText('Current');
        await expect(agentPage.getVersionItem(1)).not.toContainText('Current');

        // Rows stay simple; the selected version marks the fields it changed.
        for (const version of [1, 2, 3]) {
            await expect(agentPage.getVersionItem(version)).not.toContainText('Changed');
            await expect(agentPage.getVersionItem(version)).not.toContainText('Edited');
        }
        await expect(agentPage.getVersionFieldLabel('Custom instructions')).toContainText('Changed');
        await expect(agentPage.getVersionFieldLabel('Model')).not.toContainText('Changed');
        await expect(agentPage.getVersionHistoryDialog()).toContainText('Edited');
        await expect(agentPage.getVersionHistoryDialog()).toContainText(`Created by ${agentAdminUsername}`);
        // The current version has no row menu.
        await expect(agentPage.getVersionActionsButton(3)).toHaveCount(0);
        await expect(agentPage.getVersionActionsButton(2)).toHaveCount(1);
        for (const version of [1, 2, 3]) {
            await expect(agentPage.getVersionItem(version)).toContainText(agentAdminUsername);
            await expect(agentPage.getVersionItem(version)).not.toContainText('Unknown user');
        }

        // The current version is selected by default and cannot be restored onto itself.
        await expect(agentPage.getVersionItem(3)).toHaveAttribute('aria-current', 'true');
        await expect(agentPage.getVersionSnapshotInstructions()).toHaveText(v3);
        await expect(agentPage.getRestoreVersionButton()).toHaveCount(0);

        await agentPage.openHistoryVersion(1);
        await expect(agentPage.getVersionHistoryDialog()).toContainText('Created');
        await expect(agentPage.getVersionSnapshotInstructions()).toHaveText(v1);
        // Whitespace in the snapshot is shown exactly as saved.
        expect(await agentPage.getVersionSnapshotInstructions().textContent()).toBe(v1);
        await expect(agentPage.getVersionSnapshot()).toContainText('Mock Service');
        await expect(agentPage.getRestoreVersionButton()).toBeVisible();

        // Closing the modal returns to the editor, which still holds the current configuration.
        await agentPage.closeVersionHistory();
        await expect(agentPage.getCustomInstructionsInput()).toHaveValue(v3);

        const history = await agentApi.getAgentVersions(token, agent.id);
        expect(history.currentVersion).toBe(3);
        expect(history.versions.map((v) => v.source)).toEqual(['update', 'update', 'create']);
        expect(history.versions[0].changedFields).toEqual(['customInstructions']);
    });

    test('restoring a version from the UI resets the editor and records a restore version', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const token = await adminToken();
        const v1 = 'Original instructions to restore.';

        const agent = await agentApi.createTestAgent(token, {
            displayName: 'Restore Agent',
            username: 'restoreagent',
            serviceID: mockServiceId,
            customInstructions: v1,
        });
        await agentApi.updateAgent(token, agent.id, { customInstructions: 'Second instructions.' });
        await agentApi.updateAgent(token, agent.id, { customInstructions: 'Third instructions.' });

        const agentPage = await loginToAgents(page);
        await agentPage.openAgentEditor('Restore Agent');

        // Unsaved edits: new instructions and a pending avatar upload. The
        // pending avatar's preview must survive switching tabs.
        await agentPage.getCustomInstructionsInput().fill('Unsaved instructions that restore discards.');
        await agentPage.getAvatarFileInput().setInputFiles(avatarFixture);
        await expect(agentPage.getAvatarPreview()).toHaveAttribute('src', /^blob:/);
        await agentPage.getModalTab('Access').click();
        await agentPage.getModalTab('Configuration').click();
        await expect(agentPage.getAvatarPreview()).toHaveAttribute('src', /^blob:/);

        await agentPage.openHistoryVersion(1);
        await expect(agentPage.getVersionSnapshotInstructions()).toHaveText(v1);
        await agentPage.getRestoreVersionButton().click();

        await expect(agentPage.getRestoreDialog()).toBeVisible();
        await expect(agentPage.getRestoreDialog()).toContainText('Restoring will discard them');
        // The confirmation names what the restore changes.
        await expect(agentPage.getRestoreDialog().getByTestId('restore-changes')).toContainText('Custom instructions');
        await agentPage.getRestoreConfirmButton().click();
        await expect(agentPage.getRestoreDialog()).toBeHidden({ timeout: 10000 });

        await expect(page.getByText('Version 1 was restored as the current version.')).toBeVisible({ timeout: 10000 });
        await expect(agentPage.getVersionItems()).toHaveCount(4, { timeout: 10000 });
        await expect(agentPage.getVersionItem(4)).toContainText('Current');
        await expect(agentPage.getVersionHistoryDialog()).toContainText('Restored from version 1');
        await expect(agentPage.getVersionItem(4)).toHaveAttribute('aria-current', 'true');
        await expect(agentPage.getVersionSnapshotInstructions()).toHaveText(v1);

        // The editor now holds the restored agent with no leftover unsaved state.
        await agentPage.closeVersionHistory();
        await expect(agentPage.getVersionLine()).toContainText('v4');
        await expect(agentPage.getVersionLine()).not.toContainText('Unsaved changes');
        await expect(agentPage.getCustomInstructionsInput()).toHaveValue(v1);
        await expect(agentPage.getAvatarPreview()).not.toHaveAttribute('src', /^blob:/);
        await agentPage.getBackButton().click();
        await expect(agentPage.getDiscardChangesDialog()).not.toBeVisible();
        await agentPage.waitForModalClosed();

        const restored = await agentApi.getAgent(token, agent.id);
        expect(restored.customInstructions).toBe(v1);
        const history = await agentApi.getAgentVersions(token, agent.id);
        expect(history.currentVersion).toBe(4);
        expect(history.versions[0]).toMatchObject({ version: 4, source: 'restore', restoredFromVersion: 1 });
        expect(history.versions[0].changedFields).toContain('customInstructions');
    });

    test('restoring a version whose AI service was removed shows the server error', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();

        const agent = await agentApi.createTestAgent(token, {
            displayName: 'Removed Service Agent',
            username: 'removedserviceagent',
            serviceID: secondServiceId,
            customInstructions: 'Runs on the second service.',
        });
        await agentApi.updateAgent(token, agent.id, { serviceID: mockServiceId, customInstructions: 'Moved to the mock service.' });

        const configApi = mattermostAIAdminConfigApiFromClient(adminClient, mattermost.url());
        const originalConfig = await configApi.get();
        const services = (originalConfig.services ?? []) as Array<{ id: string }>;
        await configApi.put({ ...originalConfig, services: services.filter((s) => s.id !== secondServiceId) });
        try {
            const agentPage = await loginToAgents(page);
            await agentPage.openAgentEditor('Removed Service Agent');
            await agentPage.openHistoryVersion(1);
            await expect(agentPage.getVersionSnapshot()).toContainText('Unknown service');
            await agentPage.getRestoreVersionButton().click();
            await agentPage.getRestoreConfirmButton().click();

            await expect(page.getByRole('alert').filter({ hasText: 'AI service that no longer exists' })).toBeVisible({ timeout: 10000 });
            await expect(agentPage.getVersionItems()).toHaveCount(2);
            const current = await agentApi.getAgent(token, agent.id);
            expect(current).toMatchObject({ serviceID: mockServiceId, customInstructions: 'Moved to the mock service.' });
        } finally {
            await configApi.put(originalConfig);
        }
    });

    test('export downloads only the transferable configuration', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const token = await adminToken();
        const instructions = 'Export me.\nLine two of the instructions.';

        await agentApi.createTestAgent(token, {
            displayName: 'Export Agent',
            username: 'exportagent',
            serviceID: mockServiceId,
            customInstructions: instructions,
            model: 'gpt-secret-model',
            channelAccessLevel: 0,
            userAccessLevel: 0,
            autoEnableNewMCPTools: false,
            enabledMCPTools: [{ server_origin: embeddedMattermostOrigin, tool_name: 'read_post' }],
            enabledNativeTools: [],
            maxToolTurns: 12,
            mcpDynamicToolLoading: false,
        });

        const agentPage = await loginToAgents(page);
        await agentPage.openAgentEditor('Export Agent');
        const fromEditor = await captureDownload(page, () => agentPage.getEditorExportButton().click());

        expect(fromEditor.filename).toBe('exportagent-v1.agent.json');
        const doc = fromEditor.doc;
        expect(doc.kind).toBe('mattermost-agent');
        expect(doc.schemaVersion).toBe(1);
        expect(doc.agentVersion).toBe(1);
        expect(doc.agent).toMatchObject({
            name: 'exportagent',
            displayName: 'Export Agent',
            customInstructions: instructions,
            disableTools: false,
            maxToolTurns: 12,
            mcpDynamicToolLoading: false,
            autoEnableNewMCPTools: false,
        });
        expect(doc.agent.mcpTools).toEqual([
            { serverOrigin: embeddedMattermostOrigin, toolName: 'read_post' },
        ]);
        const keys = collectKeys(doc);
        for (const key of forbiddenExportKeys) {
            expect(keys.has(key), `export must not contain "${key}"`).toBe(false);
        }
        const raw = fs.readFileSync(fromEditor.filePath, 'utf8');
        expect(raw).not.toContain('gpt-secret-model');
        expect(raw).not.toContain(mockServiceId);

        // The row menu exports the same document.
        await agentPage.getBackButton().click();
        await agentPage.waitForModalClosed();
        await agentPage.openAgentActions('Export Agent');
        const fromRow = await captureDownload(page, () => agentPage.clickExportAction('Export Agent'));
        expect(fromRow.filename).toBe('exportagent-v1.agent.json');
        expect(fromRow.doc.agent).toEqual(doc.agent);
    });

    test('importing an exported file from the Agents list opens a prefilled new agent', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const token = await adminToken();
        const instructions = 'You were exported from one agent and imported as another.';

        await agentApi.createTestAgent(token, {
            displayName: 'Import Source',
            username: 'importsource',
            serviceID: mockServiceId,
            customInstructions: instructions,
            autoEnableNewMCPTools: false,
            enabledMCPTools: [{ server_origin: embeddedMattermostOrigin, tool_name: 'read_post' }],
            maxToolTurns: 9,
        });

        const agentPage = await loginToAgents(page);
        await agentPage.openAgentActions('Import Source');
        const exported = await captureDownload(page, () => agentPage.clickExportAction('Import Source'));

        await agentPage.importAgentFile(agentPage.getImportButton(), exported.filePath);
        await expect(page.getByRole('heading', { name: 'New Agent' })).toBeVisible({ timeout: 10000 });
        await expect(agentPage.getImportedNotice()).toBeVisible({ timeout: 10000 });
        await expect(agentPage.getCustomInstructionsInput()).toHaveValue(instructions);
        await expect(agentPage.getUsernameInput()).toHaveValue('importsource');

        // The file's username is taken here, so saving reports it on the form.
        await agentPage.getModalSaveButton().click();
        await expect(page.getByText('This username is already taken')).toBeVisible({ timeout: 10000 });

        await agentPage.getUsernameInput().fill('importedcopy');
        await agentPage.getDisplayNameInput().fill('Imported Copy');
        await agentPage.getModalSaveButton().click();
        await agentPage.waitForModalClosed();
        await expect(agentPage.getAgentRowByName('Imported Copy')).toBeVisible({ timeout: 10000 });

        const imported = await findAgentByName(agentApi, token, 'importedcopy');
        expect(imported).toBeDefined();
        expect(imported!).toMatchObject({
            displayName: 'Imported Copy',
            serviceID: mockServiceId,
            customInstructions: instructions,
            maxToolTurns: 9,
            autoEnableNewMCPTools: false,
        });
        expect(sortTools(imported!.enabledMCPTools)).toEqual([
            { server_origin: embeddedMattermostOrigin, tool_name: 'read_post' },
        ]);
        const history = await agentApi.getAgentVersions(token, imported!.id);
        expect(history.versions.map((v) => v.source)).toEqual(['create']);

        const source = await findAgentByName(agentApi, token, 'importsource');
        expect(source?.customInstructions).toBe(instructions);
        expect((await agentApi.getAgentVersions(token, source!.id)).currentVersion).toBe(1);
    });

    test('importing into an existing agent only replaces instructions and tool settings', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const token = await adminToken();

        const target = await agentApi.createTestAgent(token, {
            displayName: 'Update Target',
            username: 'updatetarget',
            serviceID: mockServiceId,
            customInstructions: 'Instructions before import.',
            model: 'gpt-target-model',
            channelAccessLevel: 0,
            userAccessLevel: 0,
            enabledNativeTools: [],
            autoEnableNewMCPTools: true,
            maxToolTurns: 10,
        });

        const doc = exportDocument({
            name: 'someoneelse',
            displayName: 'Display Name From File',
            customInstructions: 'Instructions after import.',
            maxToolTurns: 7,
            mcpTools: [{ serverOrigin: embeddedMattermostOrigin, toolName: 'search_posts' }],
        });

        const agentPage = await loginToAgents(page);
        await agentPage.openAgentEditor('Update Target');
        await agentPage.importAgentFile(agentPage.getEditorImportButton(), {
            name: 'updatetarget-edited.agent.json',
            mimeType: 'application/json',
            buffer: Buffer.from(JSON.stringify(doc)),
        });
        await expect(agentPage.getImportedNotice()).toBeVisible({ timeout: 10000 });
        await expect(agentPage.getCustomInstructionsInput()).toHaveValue('Instructions after import.');
        await expect(agentPage.getUsernameInput()).toHaveValue('updatetarget');
        await expect(agentPage.getDisplayNameInput()).toHaveValue('Update Target');

        // Nothing is saved until the editor is saved.
        expect((await agentApi.getAgent(token, target.id)).customInstructions).toBe('Instructions before import.');
        await agentPage.getModalSaveButton().click();
        await agentPage.waitForModalClosed();

        const updated = await agentApi.getAgent(token, target.id);
        expect(updated).toMatchObject({
            name: 'updatetarget',
            displayName: 'Update Target',
            serviceID: mockServiceId,
            model: 'gpt-target-model',
            channelAccessLevel: target.channelAccessLevel,
            userAccessLevel: target.userAccessLevel,
            enableVision: target.enableVision,
            reasoningEnabled: target.reasoningEnabled,
            useServiceAccountAuth: target.useServiceAccountAuth,
            customInstructions: 'Instructions after import.',
            maxToolTurns: 7,
            autoEnableNewMCPTools: false,
        });
        expect(sortTools(updated.enabledMCPTools)).toEqual([
            { server_origin: embeddedMattermostOrigin, tool_name: 'search_posts' },
        ]);

        const history = await agentApi.getAgentVersions(token, target.id);
        expect(history.currentVersion).toBe(2);
        expect(history.versions[0].source).toBe('update');
        expect(history.versions[0].changedFields).toEqual(expect.arrayContaining([
            'customInstructions', 'maxToolTurns', 'autoEnableNewMCPTools', 'enabledMCPTools',
        ]));
        for (const unchanged of ['displayName', 'model', 'serviceID', 'name']) {
            expect(history.versions[0].changedFields).not.toContain(unchanged);
        }
    });

    test('import keeps tools of MCP servers the MCPs tab lists and names the rest', async ({ page }) => {
        test.setTimeout(120000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const token = await adminToken();
        const doc = exportDocument({
            name: 'mcpimport',
            displayName: 'MCP Import',
            customInstructions: 'Imported with tools from four servers.',
            mcpTools: [
                { serverOrigin: goneOrigin, toolName: 'do_thing' },
                { serverOrigin: jiraOrigin, toolName: 'create_issue' },
                { serverOrigin: trackerNormalizedOrigin, toolName: 'create_issue' },
                { serverOrigin: embeddedMattermostOrigin, toolName: 'read_post' },
            ],
        });

        const agentPage = await loginToAgents(page);
        await agentPage.importAgentFile(agentPage.getImportButton(), {
            name: 'mcpimport.agent.json',
            mimeType: 'application/json',
            buffer: Buffer.from(JSON.stringify(doc)),
        });
        const notice = agentPage.getImportedNotice();
        await expect(notice).toContainText('left out', { timeout: 30000 });
        await expect(notice).toContainText(goneOrigin);
        await expect(notice).toContainText(jiraOrigin);
        await expect(notice).not.toContainText(trackerNormalizedOrigin);

        await agentPage.getModalSaveButton().click();
        await agentPage.waitForModalClosed();

        // Tools take the origin as configured, trailing slash included, so the
        // MCPs tab shows them enabled and a later save keeps them.
        const expectedTools = sortTools([
            { server_origin: embeddedMattermostOrigin, tool_name: 'read_post' },
            { server_origin: trackerOrigin, tool_name: 'create_issue' },
        ]);
        const imported = await findAgentByName(agentApi, token, 'mcpimport');
        expect(imported).toBeDefined();
        expect(sortTools(imported!.enabledMCPTools)).toEqual(expectedTools);

        await agentPage.openAgentEditor('MCP Import');
        await agentPage.getModalTab('MCPs').click();
        const trackerHeader = page.getByRole('button', { name: new RegExp(`^${trackerServerName}, 1 of 2 tools enabled`) });
        await expect(trackerHeader).toBeVisible({ timeout: 30000 });
        await trackerHeader.click();
        await expect(page.getByRole('switch', { name: `Disable tool create_issue on ${trackerServerName}` })).toBeChecked();
        await expect(page.getByRole('switch', { name: `Enable tool search_issues on ${trackerServerName}` })).not.toBeChecked();

        await agentPage.getModalTab('Configuration').click();
        await agentPage.getCustomInstructionsInput().fill('Edited after the import.');
        await agentPage.getModalSaveButton().click();
        await agentPage.waitForModalClosed();

        const saved = await agentApi.getAgent(token, imported!.id);
        expect(saved.customInstructions).toBe('Edited after the import.');
        expect(sortTools(saved.enabledMCPTools)).toEqual(expectedTools);
    });

    test('an invalid agent file is rejected without opening the editor', async ({ page }) => {
        test.setTimeout(60000);
        const agentPage = await loginToAgents(page);
        await agentPage.importAgentFile(agentPage.getImportButton(), {
            name: 'notes.json',
            mimeType: 'application/json',
            buffer: Buffer.from(JSON.stringify({ kind: 'something-else' })),
        });
        await expect(page.getByRole('alert').filter({ hasText: 'This file is not a valid Mattermost agent export.' })).toBeVisible({ timeout: 10000 });
        await expect(page.getByRole('heading', { name: 'New Agent' })).toHaveCount(0);
    });

    test('a bot migrated from the plugin config has a version', async () => {
        test.setTimeout(60000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const token = await adminToken();

        const legacy = await findAgentByName(agentApi, token, 'mock');
        expect(legacy).toBeDefined();
        const history = await agentApi.getAgentVersions(token, legacy!.id);
        expect(history.versions.length).toBeGreaterThanOrEqual(1);
        expect(history.versions[history.versions.length - 1].version).toBe(1);
    });

    test('deleting versions from the row menu, by multi-select, and all previous from the list menu', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const token = await adminToken();
        const agent = await agentApi.createTestAgent(token, {
            displayName: 'Prune Agent',
            username: 'pruneagent',
            serviceID: mockServiceId,
            customInstructions: 'Version 1 holds a leaked secret.',
        });
        for (const instructions of ['Version 2.', 'Version 3.', 'Version 4.']) {
            await agentApi.updateAgent(token, agent.id, { customInstructions: instructions });
        }

        const agentPage = await loginToAgents(page);
        await agentPage.openAgentEditor('Prune Agent');
        await agentPage.openVersionHistory();
        await expect(agentPage.getVersionItems()).toHaveCount(4, { timeout: 10000 });

        // Deleting is tucked away: no standalone Delete button or checkboxes until
        // multi-select is turned on, and the current version (the live
        // configuration) has no row menu.
        await expect(agentPage.getVersionActionsButton(4)).toHaveCount(0);

        await agentPage.clickVersionAction(1, 'Delete this version');
        await expect(page.getByRole('dialog', { name: 'Delete version 1?' })).toBeVisible();
        await agentPage.confirmVersionDelete();
        await expect(page.getByText('Deleted 1 version.')).toBeVisible({ timeout: 10000 });
        await expect(agentPage.getVersionItems()).toHaveCount(3);
        expect((await agentApi.getAgentVersions(token, agent.id)).versions.map((v) => v.version)).toEqual([4, 3, 2]);

        await expect(agentPage.getVersionList().getByRole('checkbox')).toHaveCount(0);
        await expect(page.getByRole('button', { name: 'Delete', exact: true })).toHaveCount(0);
        await agentPage.startSelectingVersions();
        await expect(agentPage.getVersionList().getByRole('checkbox')).toHaveCount(2);
        await expect(agentPage.getVersionCheckbox(4)).toHaveCount(0);
        await expect(agentPage.getDeleteSelectedVersionsButton()).toBeDisabled();
        await agentPage.getVersionCheckbox(2).check();
        await agentPage.getDeleteSelectedVersionsButton().click();
        await agentPage.confirmVersionDelete();
        await expect(page.getByText('Deleted 1 version.')).toBeVisible({ timeout: 10000 });
        await expect(agentPage.getVersionItems()).toHaveCount(2);
        expect((await agentApi.getAgentVersions(token, agent.id)).versions.map((v) => v.version)).toEqual([4, 3]);

        await agentPage.clickDeleteAllPreviousVersions();
        await expect(page.getByRole('dialog', { name: 'Delete all previous versions?' })).toBeVisible();
        await agentPage.confirmVersionDelete();
        await expect(agentPage.getVersionItems()).toHaveCount(1, { timeout: 10000 });
        await expect(agentPage.getVersionItem(4)).toContainText('Current');
        await expect(agentPage.getVersionHistoryMenuButton()).toHaveCount(0);

        const history = await agentApi.getAgentVersions(token, agent.id);
        expect(history).toMatchObject({ currentVersion: 4, hasMore: false });
        expect(history.versions.map((v) => v.version)).toEqual([4]);
        expect((await agentApi.getAgent(token, agent.id)).customInstructions).toBe('Version 4.');

        const refused = await agentApi.deleteAgentVersions(token, agent.id, [4]);
        expect(refused.status).toBe(400);
    });

    test('the version history limit prunes existing agents and every later save', async () => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();
        const agent = await agentApi.createTestAgent(token, {
            displayName: 'Retention Agent',
            username: 'retentionagent',
            serviceID: mockServiceId,
            customInstructions: 'Version 1.',
        });
        for (const instructions of ['Version 2.', 'Version 3.', 'Version 4.']) {
            await agentApi.updateAgent(token, agent.id, { customInstructions: instructions });
        }

        const configApi = mattermostAIAdminConfigApiFromClient(adminClient, mattermost.url());
        const originalConfig = await configApi.get();
        try {
            await configApi.put({ ...originalConfig, agentVersionsToKeep: 2 });
            await expect.poll(async () => (await agentApi.getAgentVersions(token, agent.id)).versions.map((v) => v.version), { timeout: 15000 }).toEqual([4, 3]);

            await agentApi.updateAgent(token, agent.id, { customInstructions: 'Version 5.' });
            expect((await agentApi.getAgentVersions(token, agent.id)).versions.map((v) => v.version)).toEqual([5, 4]);
        } finally {
            await configApi.put({ ...originalConfig, agentVersionsToKeep: 0 });
        }

        // Keeping every version again stops pruning.
        await agentApi.updateAgent(token, agent.id, { customInstructions: 'Version 6.' });
        expect((await agentApi.getAgentVersions(token, agent.id)).versions.map((v) => v.version)).toEqual([6, 5, 4]);
    });

    test('users who cannot manage an agent cannot see or change its history', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const token = await adminToken();
        const secret = 'Secret instructions only managers may read.';
        const agent = await agentApi.createTestAgent(token, {
            displayName: 'Admin Only Agent',
            username: 'adminonlyagent',
            serviceID: mockServiceId,
            customInstructions: secret,
            userAccessLevel: 0,
        });
        await agentApi.updateAgent(token, agent.id, { customInstructions: `${secret} Edited.` });

        const regularClient = await mattermost.getClient(agentRegularUsername, agentRegularPassword);
        const regularToken = regularClient.getToken();
        const routes = mattermostAIPluginRoutes(mattermost.url());
        for (const [method, route, body] of [
            ['GET', `agents/${agent.id}/versions`, undefined],
            ['GET', `agents/${agent.id}/versions/1`, undefined],
            ['POST', `agents/${agent.id}/versions/1/restore`, undefined],
            ['DELETE', `agents/${agent.id}/versions`, { all: true }],
        ] as const) {
            const response = await routes.request(method, route, regularToken, body);
            expect(response.status, `${method} ${route}`).toBe(403);
            expect(await response.text()).not.toContain(secret);
        }
        expect((await agentApi.getAgentVersions(token, agent.id)).versions).toHaveLength(2);

        await mattermost.revokeManageOwnAgentFromSystemUser();
        try {
            const mmPage = new MattermostPage(page);
            const agentPage = new AgentPageHelper(page);
            await mmPage.login(mattermost.url(), agentUnprivilegedUsername, agentUnprivilegedPassword);
            await agentPage.navigateToAgents(mattermost.url());
            await expect(agentPage.getAgentRowByName('Admin Only Agent')).toBeVisible({ timeout: 10000 });
            await expect(agentPage.getImportButton()).toHaveCount(0);
        } finally {
            await mattermost.grantSelfServiceAgentPermissions();
        }
    });
});
