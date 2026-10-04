import fs from 'fs';
import path from 'path';

import { test, expect, Page } from '@playwright/test';
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
// to connect to it; import still offers it as a mapping target to system admins.
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

test.describe('Agent versioning, export and import', () => {
    test.beforeAll(async () => {
        test.setTimeout(180000);
        mattermost = await RunAgentContainer({
            mcpServers: [
                { name: jiraServerName, enabled: false, baseURL: jiraOrigin },
                { name: trackerServerName, enabled: true, baseURL: trackerOrigin },
            ],
        });
        openAIMock = await RunOpenAIMocks(mattermost.network);
        await openAIMock.addCompletionMock(responseTest);
        await registerMCPToolsServerMocks(openAIMock, trackerMockPath, trackerTools);
    });

    test.afterAll(async () => {
        await openAIMock?.stop();
        await mattermost?.stop();
    });

    test('History tab lists every save and shows an old version read-only', async ({ page }) => {
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
        await agentPage.getModalTab('History').click();

        await expect(agentPage.getVersionItems()).toHaveCount(3, { timeout: 10000 });
        await expect(agentPage.getVersionItems().first()).toContainText('Version 3');
        await expect(agentPage.getVersionItem(3)).toContainText('Current');
        await expect(agentPage.getVersionItem(3)).toContainText('Edited');
        await expect(agentPage.getVersionItem(3)).toContainText('Changed: Custom instructions');
        await expect(agentPage.getVersionItem(2)).not.toContainText('Current');
        await expect(agentPage.getVersionItem(1)).not.toContainText('Current');
        await expect(agentPage.getVersionItem(1)).toContainText('Created');
        for (const version of [1, 2, 3]) {
            await expect(agentPage.getVersionItem(version)).toContainText(agentAdminUsername);
            await expect(agentPage.getVersionItem(version)).not.toContainText('Unknown user');
        }

        // The current version is selected by default and cannot be restored onto itself.
        await expect(agentPage.getVersionItem(3)).toHaveAttribute('aria-current', 'true');
        await expect(agentPage.getVersionSnapshotInstructions()).toHaveText(v3);
        await expect(agentPage.getRestoreVersionButton()).toHaveCount(0);

        // Nothing on the History tab is saved by the editor footer.
        await expect(agentPage.getModalSaveButton()).toBeHidden();

        await agentPage.openHistoryVersion(1);
        await expect(agentPage.getVersionSnapshotInstructions()).toHaveText(v1);
        // Whitespace in the snapshot is shown exactly as saved.
        expect(await agentPage.getVersionSnapshotInstructions().textContent()).toBe(v1);
        await expect(agentPage.getVersionSnapshot()).toContainText('Mock Service');
        await expect(agentPage.getCustomInstructionsInput()).toHaveCount(0);
        await expect(agentPage.getRestoreVersionButton()).toBeVisible();

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
        await agentPage.getRestoreConfirmButton().click();
        await expect(agentPage.getRestoreDialog()).toBeHidden({ timeout: 10000 });

        await expect(page.getByText('Version 1 was restored as the current version.')).toBeVisible({ timeout: 10000 });
        await expect(agentPage.getVersionItems()).toHaveCount(4, { timeout: 10000 });
        await expect(agentPage.getVersionItem(4)).toContainText('Current');
        await expect(agentPage.getVersionItem(4)).toContainText('Restored from version 1');
        await expect(agentPage.getVersionItem(4)).toHaveAttribute('aria-current', 'true');
        await expect(agentPage.getVersionSnapshotInstructions()).toHaveText(v1);

        // The editor now holds the restored agent with no leftover unsaved state.
        await agentPage.getModalTab('Configuration').click();
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
            { serverOrigin: embeddedMattermostOrigin, serverName: 'Mattermost', toolName: 'read_post' },
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

    test('importing an exported file creates a new agent', async ({ page }) => {
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

        await agentPage.openImportModal();
        await agentPage.chooseImportFile(exported.filePath);
        await expect(agentPage.getImportSummary()).toContainText('Import Source');
        await expect(agentPage.getImportSummary()).toContainText('Exported from version 1');
        await expect(agentPage.getImportSummary()).toContainText('MCP tools: 1 tool');
        await expect(agentPage.getImportSummary()).toContainText(instructions);

        // The source agent exists here and is manageable, so update is preselected.
        await expect(agentPage.getImportModeRadio('update')).toBeChecked();
        await agentPage.getImportModeRadio('create').check();

        await expect(agentPage.getImportUsernameInput()).toHaveValue('importsource');
        await expect(agentPage.getImportDisplayNameInput()).toHaveValue('Import Source');
        await agentPage.getImportUsernameInput().fill('importedcopy');
        await agentPage.getImportDisplayNameInput().fill('Imported Copy');
        await agentPage.getImportServiceSelect().selectOption({ label: 'Mock Service' });
        await expect(agentPage.getImportMCPMappingSelect('Mattermost')).toHaveValue(embeddedMattermostOrigin);

        await agentPage.getImportSubmitButton().click();
        await expect(agentPage.getImportDialog()).toBeHidden({ timeout: 10000 });
        await expect(page.getByText('Imported agent Imported Copy.')).toBeVisible({ timeout: 10000 });
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
        expect(history.versions.map((v) => v.source)).toEqual(['import']);

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

        const exported = await agentApi.exportAgent(token, target.id);
        expect(exported.status).toBe(200);
        const doc = exported.body;
        doc.agent.displayName = 'Display Name From File';
        doc.agent.customInstructions = 'Instructions after import.';
        doc.agent.maxToolTurns = 7;
        doc.agent.disableTools = false;
        doc.agent.autoEnableNewMCPTools = false;
        doc.agent.mcpTools = [{ serverOrigin: embeddedMattermostOrigin, serverName: 'Mattermost', toolName: 'search_posts' }];

        const agentPage = await loginToAgents(page);
        await agentPage.openImportModal();
        await agentPage.chooseImportFile({
            name: 'updatetarget-edited.agent.json',
            mimeType: 'application/json',
            buffer: Buffer.from(JSON.stringify(doc)),
        });
        await expect(agentPage.getImportModeRadio('update')).toBeChecked();
        await expect(agentPage.getImportTargetAgentSelect()).toHaveValue(target.id);
        await agentPage.getImportSubmitButton().click();
        await expect(agentPage.getImportDialog()).toBeHidden({ timeout: 10000 });
        await expect(page.getByText('Updated agent Update Target from the imported file.')).toBeVisible({ timeout: 10000 });

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
        expect(history.versions[0].source).toBe('import');
        expect(history.versions[0].changedFields).toEqual(expect.arrayContaining([
            'customInstructions', 'maxToolTurns', 'autoEnableNewMCPTools', 'enabledMCPTools',
        ]));
        for (const unchanged of ['displayName', 'model', 'serviceID', 'name']) {
            expect(history.versions[0].changedFields).not.toContain(unchanged);
        }
    });

    test('import API requires every unknown MCP server to be mapped or removed', async () => {
        test.setTimeout(60000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const token = await adminToken();
        const doc = exportDocument({
            name: 'mcpmappingapi',
            customInstructions: 'Agent with tools from three servers.',
            mcpTools: [
                { serverOrigin: goneOrigin, serverName: 'Gone Server', toolName: 'do_thing' },
                { serverOrigin: `${jiraOrigin}/`, serverName: 'Jira Elsewhere', toolName: 'create_issue' },
                { serverOrigin: embeddedMattermostOrigin, serverName: 'Mattermost', toolName: 'read_post' },
            ],
        });

        const preview = await agentApi.previewAgentImport(token, doc);
        expect(preview.status).toBe(200);
        expect(preview.body.existingAgent ?? null).toBeNull();
        const groups = Object.fromEntries(preview.body.mcpServers.map((s) => [s.sourceOrigin, s]));
        expect(Object.keys(groups).sort()).toEqual([embeddedMattermostOrigin, goneOrigin, jiraOrigin].sort());
        expect(groups[goneOrigin]).toMatchObject({ autoTargetOrigin: '', toolNames: ['do_thing'], sourceName: 'Gone Server' });
        expect(groups[jiraOrigin]).toMatchObject({ autoTargetOrigin: jiraOrigin, toolNames: ['create_issue'] });
        expect(groups[embeddedMattermostOrigin]).toMatchObject({ autoTargetOrigin: embeddedMattermostOrigin });
        expect(preview.body.availableMCPServers).toEqual(expect.arrayContaining([
            { origin: jiraOrigin, name: jiraServerName },
            { origin: trackerOrigin, name: trackerServerName },
            { origin: embeddedMattermostOrigin, name: 'Mattermost' },
        ]));

        const createBase = {
            document: doc,
            mode: 'create' as const,
            displayName: 'MCP Mapping API',
            serviceID: mockServiceId,
        };

        // A manager who is not a system admin is offered only the servers their
        // MCPs tab lists, so the disabled Jira server is neither shown nor accepted.
        const regularToken = (await mattermost.getClient(agentRegularUsername, agentRegularPassword)).getToken();
        const regularPreview = await agentApi.previewAgentImport(regularToken, doc);
        expect(regularPreview.status).toBe(200);
        const regularOrigins = regularPreview.body.availableMCPServers.map((s) => s.origin);
        expect(regularOrigins).toEqual(expect.arrayContaining([trackerOrigin, embeddedMattermostOrigin]));
        expect(regularOrigins).not.toContain(jiraOrigin);
        const regularGroups = Object.fromEntries(regularPreview.body.mcpServers.map((s) => [s.sourceOrigin, s]));
        expect(regularGroups[jiraOrigin]).toMatchObject({ autoTargetOrigin: '' });
        const regularImport = await agentApi.importAgent(regularToken, {
            ...createBase,
            username: 'mcpmappingregular',
            mcpServerMappings: [
                { sourceOrigin: goneOrigin, targetOrigin: '' },
                { sourceOrigin: jiraOrigin, targetOrigin: jiraOrigin },
            ],
        });
        expect(regularImport.status).toBe(400);
        expect(regularImport.body.error).toContain('not configured');
        expect(await findAgentByName(agentApi, token, 'mcpmappingregular')).toBeUndefined();

        const unmapped = await agentApi.importAgent(token, { ...createBase, username: 'mcpmappingapi' });
        expect(unmapped.status).toBe(400);
        expect(unmapped.body.error).toContain('must be mapped to a server on this instance or removed');
        expect(unmapped.body.error).toContain(goneOrigin);

        const unknownTarget = await agentApi.importAgent(token, {
            ...createBase,
            username: 'mcpmappingapi',
            mcpServerMappings: [{ sourceOrigin: goneOrigin, targetOrigin: 'https://not-here.example.com/mcp' }],
        });
        expect(unknownTarget.status).toBe(400);
        expect(unknownTarget.body.error).toContain('not configured');
        expect(await findAgentByName(agentApi, token, 'mcpmappingapi')).toBeUndefined();

        const removed = await agentApi.importAgent(token, {
            ...createBase,
            username: 'mcpmappingapi',
            mcpServerMappings: [{ sourceOrigin: goneOrigin, targetOrigin: '' }],
        });
        expect(removed.status).toBe(201);
        expect(sortTools(removed.body.enabledMCPTools)).toEqual([
            { server_origin: embeddedMattermostOrigin, tool_name: 'read_post' },
            { server_origin: jiraOrigin, tool_name: 'create_issue' },
        ]);

        const remapped = await agentApi.importAgent(token, {
            ...createBase,
            username: 'mcpmappingapi2',
            displayName: 'MCP Mapping API 2',
            mcpServerMappings: [{ sourceOrigin: goneOrigin, targetOrigin: jiraOrigin }],
        });
        expect(remapped.status).toBe(201);
        expect(sortTools(remapped.body.enabledMCPTools)).toEqual([
            { server_origin: embeddedMattermostOrigin, tool_name: 'read_post' },
            { server_origin: jiraOrigin, tool_name: 'create_issue' },
            { server_origin: jiraOrigin, tool_name: 'do_thing' },
        ]);

        // Auto-enable-all ignores the allowlist, so unknown servers need no mapping.
        const autoAll = await agentApi.importAgent(token, {
            ...createBase,
            document: { ...doc, agent: { ...doc.agent, autoEnableNewMCPTools: true } },
            username: 'mcpmappingauto',
            displayName: 'MCP Mapping Auto',
        });
        expect(autoAll.status).toBe(201);
        expect(autoAll.body.autoEnableNewMCPTools).toBe(true);
        expect(autoAll.body.enabledMCPTools ?? []).toEqual([]);
    });

    test('a bot migrated from the plugin config has a version and can be exported', async () => {
        test.setTimeout(60000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const token = await adminToken();

        const legacy = await findAgentByName(agentApi, token, 'mock');
        expect(legacy).toBeDefined();
        const history = await agentApi.getAgentVersions(token, legacy!.id);
        expect(history.versions.length).toBeGreaterThanOrEqual(1);
        expect(history.versions[history.versions.length - 1].version).toBe(1);

        const exported = await agentApi.exportAgent(token, legacy!.id);
        expect(exported.status).toBe(200);
        expect(exported.headers.get('content-type')).toContain('application/json');
        expect(exported.headers.get('content-disposition'))
            .toBe(`attachment; filename="mock-v${history.currentVersion}.agent.json"`);
        expect(exported.body.agentVersion).toBe(history.currentVersion);
        expect(exported.body.agent.name).toBe('mock');
    });

    test('users who cannot manage an agent cannot see its history, export it or import over it', async ({ page }) => {
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

        const regularClient = await mattermost.getClient(agentRegularUsername, agentRegularPassword);
        const regularToken = regularClient.getToken();
        const routes = mattermostAIPluginRoutes(mattermost.url());
        for (const [method, route] of [
            ['GET', `agents/${agent.id}/versions`],
            ['GET', `agents/${agent.id}/versions/1`],
            ['POST', `agents/${agent.id}/versions/1/restore`],
            ['GET', `agents/${agent.id}/export`],
        ] as const) {
            const response = await routes.request(method, route, regularToken);
            expect(response.status, `${method} ${route}`).toBe(403);
            expect(await response.text()).not.toContain(secret);
        }

        const exported = (await agentApi.exportAgent(token, agent.id)).body;
        const updateOther = await agentApi.importAgent(regularToken, {
            document: { ...exported, agent: { ...exported.agent, customInstructions: 'Hijacked.' } },
            mode: 'update',
            agentID: agent.id,
        });
        expect(updateOther.status).toBe(403);
        expect((await agentApi.getAgent(token, agent.id)).customInstructions).toBe(secret);

        await mattermost.revokeManageOwnAgentFromSystemUser();
        try {
            const unprivilegedClient = await mattermost.getClient(agentUnprivilegedUsername, agentUnprivilegedPassword);
            const preview = await agentApi.previewAgentImport(unprivilegedClient.getToken(), exported);
            expect(preview.status).toBe(403);

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

    test('import modal preselects same-origin MCP servers and blocks until the rest are mapped', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const token = await adminToken();
        const doc = exportDocument({
            name: 'mcpmappingui',
            displayName: 'MCP Mapping UI',
            customInstructions: 'Imported through the modal with MCP mappings.',
            mcpTools: [
                { serverOrigin: goneOrigin, serverName: 'Gone Server', toolName: 'do_thing' },
                { serverOrigin: jiraOrigin, serverName: 'Jira Elsewhere', toolName: 'create_issue' },
                { serverOrigin: embeddedMattermostOrigin, serverName: 'Mattermost', toolName: 'read_post' },
            ],
        });

        const agentPage = await loginToAgents(page);
        await agentPage.openImportModal();
        await agentPage.chooseImportFile({
            name: 'mcpmappingui.agent.json',
            mimeType: 'application/json',
            buffer: Buffer.from(JSON.stringify(doc)),
        });

        await expect(agentPage.getImportModeRadio('create')).toBeChecked();
        await expect(agentPage.getImportMCPMappingSelect('Jira Elsewhere')).toHaveValue(jiraOrigin);
        await expect(agentPage.getImportMCPMappingSelect('Mattermost')).toHaveValue(embeddedMattermostOrigin);
        await expect(agentPage.getImportMCPMappingSelect('Gone Server')).toHaveValue('');
        await expect(agentPage.getImportSubmitButton()).toBeDisabled();

        await agentPage.getImportMCPMappingSelect('Gone Server').selectOption({ label: 'Remove these tools' });
        await expect(agentPage.getImportSubmitButton()).toBeEnabled();

        // A server-side rejection is shown in the modal, which stays open for a retry.
        await agentPage.getImportUsernameInput().fill('mock');
        await agentPage.getImportSubmitButton().click();
        await expect(agentPage.getImportDialog().getByRole('alert')).toContainText('username "mock" is already taken');
        await expect(agentPage.getImportDialog()).toBeVisible();

        await agentPage.getImportUsernameInput().fill('mcpmappingui');
        await agentPage.getImportSubmitButton().click();
        await expect(agentPage.getImportDialog()).toBeHidden({ timeout: 10000 });
        await expect(agentPage.getAgentRowByName('MCP Mapping UI')).toBeVisible({ timeout: 10000 });

        const imported = await findAgentByName(agentApi, token, 'mcpmappingui');
        expect(imported).toBeDefined();
        expect(imported!.customInstructions).toBe('Imported through the modal with MCP mappings.');
        expect(sortTools(imported!.enabledMCPTools)).toEqual([
            { server_origin: embeddedMattermostOrigin, tool_name: 'read_post' },
            { server_origin: jiraOrigin, tool_name: 'create_issue' },
        ]);
    });

    test('tools imported for a server configured with a trailing slash stay enabled through an editor save', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const token = await adminToken();
        const doc = exportDocument({
            name: 'trackerimport',
            displayName: 'Tracker Import',
            customInstructions: 'Uses tools from a server configured with a trailing slash.',
            mcpTools: [{ serverOrigin: trackerNormalizedOrigin, serverName: 'Tracker Elsewhere', toolName: 'create_issue' }],
        });

        const preview = await agentApi.previewAgentImport(token, doc);
        expect(preview.status).toBe(200);
        expect(preview.body.mcpServers).toEqual([
            expect.objectContaining({ sourceOrigin: trackerNormalizedOrigin, autoTargetOrigin: trackerOrigin }),
        ]);

        const imported = await agentApi.importAgent(token, {
            document: doc,
            mode: 'create',
            username: 'trackerimport',
            displayName: 'Tracker Import',
            serviceID: mockServiceId,
        });
        expect(imported.status).toBe(201);
        const expectedTools = [{ server_origin: trackerOrigin, tool_name: 'create_issue' }];
        expect(sortTools(imported.body.enabledMCPTools)).toEqual(expectedTools);

        const agentPage = await loginToAgents(page);
        await agentPage.openAgentEditor('Tracker Import');
        await agentPage.getModalTab('MCPs').click();
        const trackerHeader = page.getByRole('button', { name: new RegExp(`^${trackerServerName}, 1 of 2 tools enabled`) });
        await expect(trackerHeader).toBeVisible({ timeout: 30000 });
        await trackerHeader.click();
        await expect(page.getByRole('button', { name: `Disable tool create_issue on ${trackerServerName}` }))
            .toHaveAttribute('aria-checked', 'true');
        await expect(page.getByRole('button', { name: `Enable tool search_issues on ${trackerServerName}` }))
            .toHaveAttribute('aria-checked', 'false');

        // Saving an unrelated edit sends the reconciled tool list back to the server.
        await agentPage.getModalTab('Configuration').click();
        await agentPage.getCustomInstructionsInput().fill('Edited after the import.');
        await agentPage.getModalSaveButton().click();
        await agentPage.waitForModalClosed();

        const saved = await agentApi.getAgent(token, imported.body.id);
        expect(saved.customInstructions).toBe('Edited after the import.');
        expect(sortTools(saved.enabledMCPTools)).toEqual(expectedTools);
    });
});
