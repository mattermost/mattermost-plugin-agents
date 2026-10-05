import crypto from 'crypto';
import fs from 'fs';

import { test, expect, Page } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import {
    OpenAIMockContainer,
    RunOpenAIMocks,
    TITLE_GENERATION_BODY_MATCH,
    buildChatCompletionMockRule,
    buildTextResponse,
    titleGenerationMockRule,
} from 'helpers/openai-mock';
import {
    RunAgentContainer,
    agentAdminUsername, agentAdminPassword,
    agentRegularUsername, agentRegularPassword,
    agentUnprivilegedUsername, agentUnprivilegedPassword,
    mockServiceId,
} from 'helpers/agent-container';
import {
    AgentAPIHelper,
    AgentDocument,
    AgentExportDocument,
    AgentResponse,
    CreateAgentRequest,
    mergeAgentIntoUpdate,
} from 'helpers/agent-api';
import { AgentPageHelper } from 'helpers/agent-page';
import { buildPDF } from 'helpers/pdf-fixture';
import { mattermostAIPluginRoutes } from 'helpers/plugin-http';

type FixtureFile = { name: string; mimeType: string; buffer: Buffer };

const handbookText = 'Handbook: the office opens at 8am.\nVisitors sign in at the front desk.';
const handbook: FixtureFile = { name: 'handbook.txt', mimeType: 'text/plain', buffer: Buffer.from(`${handbookText}\n`) };

const glossaryText = '# Glossary\n\nSLA: service level agreement.';
const glossary: FixtureFile = { name: 'glossary.md', mimeType: 'text/markdown', buffer: Buffer.from(`${glossaryText}\n`) };

const refundLines = ['Refund policy: returns are accepted within 30 days.', 'Support hours: 9 to 5 on weekdays.'];
const refundText = refundLines.join('\n');
const refunds: FixtureFile = { name: 'refunds.pdf', mimeType: 'application/pdf', buffer: buildPDF(refundLines) };

const maxTextRunes = 100000;

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

function sha256(buffer: Buffer): string {
    return crypto.createHash('sha256').update(buffer).digest('hex');
}

/** Mirrors formatDocumentSize in the webapp for the en locale. */
function sizeLabel(bytes: number): string {
    if (bytes >= 1024 * 1024) {
        return `${(Math.round((bytes / (1024 * 1024)) * 10) / 10).toLocaleString('en-US')} MB`;
    }
    if (bytes >= 1024) {
        return `${(Math.round((bytes / 1024) * 10) / 10).toLocaleString('en-US')} KB`;
    }
    return `${bytes.toLocaleString('en-US')} B`;
}

function metaLabel(type: string, bytes: number, runes: number): string {
    return `${type} · ${sizeLabel(bytes)} · ${runes.toLocaleString('en-US')} characters extracted`;
}

function usageLabel(runes: number): string {
    return `${runes.toLocaleString('en-US')} of ${maxTextRunes.toLocaleString('en-US')} characters of extracted text used`;
}

/** The document the server should store for a fixture with the given extracted text. */
function expectedDocument(file: FixtureFile, text: string, name = file.name) {
    return {
        id: expect.any(String),
        name,
        mimeType: file.mimeType,
        size: file.buffer.length,
        sha256: sha256(file.buffer),
        textRunes: [...text].length,
    };
}

/** Compares documents ignoring ids, which deduplication may or may not reuse. */
function withoutIds(docs: AgentDocument[] | undefined) {
    return (docs ?? []).map(({ id: _id, ...rest }) => rest);
}

/**
 * Creates an agent through the API with the defaults the editor would save, so
 * an editor save only reports the fields the test changes.
 */
async function createAgent(agentApi: AgentAPIHelper, token: string, overrides: Partial<CreateAgentRequest>): Promise<AgentResponse> {
    return agentApi.createTestAgent(token, {
        serviceID: mockServiceId,
        maxToolTurns: 20,
        reasoningEffort: 'medium',
        ...overrides,
    });
}

async function adminToken(): Promise<string> {
    const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
    return adminClient.getToken();
}

async function loginToAgents(page: Page): Promise<AgentPageHelper> {
    const mmPage = new MattermostPage(page);
    const agentPage = new AgentPageHelper(page);
    await mmPage.login(mattermost.url(), agentAdminUsername, agentAdminPassword);
    await agentPage.navigateToAgents(mattermost.url());
    return agentPage;
}

/** Runs trigger and returns the downloaded file's name and bytes. */
async function captureDownload(page: Page, trigger: () => Promise<void>): Promise<{ filename: string; filePath: string; bytes: Buffer }> {
    const [download] = await Promise.all([
        page.waitForEvent('download', { timeout: 15000 }),
        trigger(),
    ]);
    const filePath = await download.path();
    expect(filePath).toBeTruthy();
    return { filename: download.suggestedFilename(), filePath, bytes: fs.readFileSync(filePath) };
}

async function downloadBytes(agentApi: AgentAPIHelper, token: string, agentId: string, documentId: string): Promise<Buffer> {
    const response = await agentApi.downloadAgentDocument(token, agentId, documentId);
    expect(response.status, `download ${documentId}`).toBe(200);
    return Buffer.from(await response.arrayBuffer());
}

async function findAgentByName(agentApi: AgentAPIHelper, token: string, name: string): Promise<AgentResponse | undefined> {
    const agents = await agentApi.getAgents(token);
    return agents.find((a) => a.name === name);
}

/** Content of the system message of an OpenAI chat completion request body. */
function systemPrompt(body: string): string {
    const parsed = JSON.parse(body) as { messages: Array<{ role: string; content: string | Array<{ text?: string }> }> };
    const system = parsed.messages.find((m) => m.role === 'system');
    if (!system) {
        return '';
    }
    return typeof system.content === 'string' ? system.content : system.content.map((part) => part.text ?? '').join('');
}

test.describe('Agent reference documents', () => {
    test.beforeAll(async () => {
        test.setTimeout(180000);
        mattermost = await RunAgentContainer();
        openAIMock = await RunOpenAIMocks(mattermost.network);
    });

    test.afterAll(async () => {
        await openAIMock?.stop();
        await mattermost?.stop();
    });

    test('documents uploaded in the editor are saved as a new version', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const token = await adminToken();
        const agent = await createAgent(agentApi, token, {
            displayName: 'Docs Upload Agent',
            username: 'docsuploadagent',
            serviceID: mockServiceId,
            customInstructions: 'Answer questions about the office.',
        });

        const agentPage = await loginToAgents(page);
        await agentPage.openAgentEditor('Docs Upload Agent');
        await expect(agentPage.getDocumentsUsage()).toHaveText(usageLabel(0));

        await agentPage.getDocumentsFileInput().setInputFiles([handbook, refunds]);
        await expect(agentPage.getDocumentRows()).toHaveCount(2, { timeout: 15000 });
        await expect(agentPage.getDocumentRows().nth(0)).toContainText('handbook.txt');
        await expect(agentPage.getDocumentRows().nth(1)).toContainText('refunds.pdf');
        await expect(agentPage.getDocumentRow('handbook.txt'))
            .toContainText(metaLabel('Text', handbook.buffer.length, handbookText.length));
        await expect(agentPage.getDocumentRow('refunds.pdf'))
            .toContainText(metaLabel('PDF', refunds.buffer.length, refundText.length));
        await expect(agentPage.getDocumentsUsage()).toHaveText(usageLabel(handbookText.length + refundText.length));
        // Unsaved documents cannot be downloaded from the agent yet.
        await expect(agentPage.getDocumentRow('handbook.txt').getByRole('button', { name: 'Download handbook.txt' })).toHaveCount(0);

        await agentPage.getModalSaveButton().click();
        await agentPage.waitForModalClosed();

        const saved = await agentApi.getAgent(token, agent.id);
        expect(saved.documents).toEqual([
            expectedDocument(handbook, handbookText),
            expectedDocument(refunds, refundText),
        ]);
        const history = await agentApi.getAgentVersions(token, agent.id);
        expect(history.currentVersion).toBe(2);
        expect(history.versions[0]).toMatchObject({ version: 2, source: 'update', changedFields: ['documents'] });

        const pdfText = await agentApi.getAgentDocumentText(token, agent.id, saved.documents![1].id);
        expect(pdfText.status).toBe(200);
        expect(pdfText.body).toEqual({ id: saved.documents![1].id, text: refundText, textRunes: refundText.length });

        await agentPage.openAgentEditor('Docs Upload Agent');
        await expect(agentPage.getDocumentRows()).toHaveCount(2);
        const fromEditor = await captureDownload(page, () =>
            agentPage.getDocumentRow('refunds.pdf').getByRole('button', { name: 'Download refunds.pdf' }).click());
        expect(fromEditor.filename).toBe('refunds.pdf');
        expect(fromEditor.bytes.equals(refunds.buffer)).toBe(true);

        await agentPage.getModalTab('History').click();
        await expect(agentPage.getVersionItem(2)).toContainText('Changed: Reference documents');
        await expect(agentPage.getVersionSnapshotDocuments()).toContainText('handbook.txt');
        await expect(agentPage.getVersionSnapshotDocuments()).toContainText('refunds.pdf');
    });

    test('a new agent can be created with documents, and a repeated file is attached once', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const token = await adminToken();
        const unicodeName = 'Händbuch – Büro.txt';

        const agentPage = await loginToAgents(page);
        await agentPage.getCreateButton().click();
        await agentPage.waitForModal();
        await agentPage.fillConfigTab({ displayName: 'Docs Created Agent', username: 'docscreatedagent', serviceLabel: 'Mock Service' });

        // The same bytes under another name resolve to the already attached document.
        await agentPage.getDocumentsFileInput().setInputFiles([handbook, { ...handbook, name: 'handbook copy.txt' }]);
        await expect(agentPage.getDocumentUploadRow('handbook copy.txt')).toContainText('This file is already attached to the agent.', { timeout: 15000 });
        await expect(agentPage.getDocumentRows()).toHaveCount(1);

        await agentPage.getDocumentRow('handbook.txt').getByRole('button', { name: 'Rename handbook.txt' }).click();
        await agentPage.getDocumentsSection().getByLabel('Document name').fill(`  ${unicodeName}  `);
        await agentPage.getDocumentsSection().getByLabel('Document name').press('Enter');
        await expect(agentPage.getDocumentRows().nth(0)).toContainText(unicodeName);

        await agentPage.getModalSaveButton().click();
        await agentPage.waitForModalClosed();

        const created = await findAgentByName(agentApi, token, 'docscreatedagent');
        expect(created?.documents).toEqual([expectedDocument(handbook, handbookText, unicodeName)]);
        const history = await agentApi.getAgentVersions(token, created!.id);
        expect(history.versions.map((v) => v.source)).toEqual(['create']);

        const response = await agentApi.downloadAgentDocument(token, created!.id, created!.documents![0].id);
        expect(response.headers.get('content-type')).toContain('text/plain');
        expect(response.headers.get('x-content-type-options')).toBe('nosniff');
        expect(response.headers.get('content-disposition')).toContain(`filename*=UTF-8''${encodeURIComponent(unicodeName)}`);

        await agentPage.openAgentEditor('Docs Created Agent');
        const downloaded = await captureDownload(page, () =>
            agentPage.getDocumentRow(unicodeName).getByRole('button', { name: `Download ${unicodeName}` }).click());
        expect(downloaded.filename).toBe(unicodeName);
        expect(downloaded.bytes.equals(handbook.buffer)).toBe(true);
    });

    test('renaming and removing documents create versions, and restoring an old version brings them back', async ({ page }) => {
        test.setTimeout(120000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const token = await adminToken();
        const handbookDoc = await agentApi.uploadAgentDocumentOrThrow(token, handbook);
        const glossaryDoc = await agentApi.uploadAgentDocumentOrThrow(token, glossary);
        const agent = await createAgent(agentApi, token, {
            displayName: 'Docs History Agent',
            username: 'docshistoryagent',
            serviceID: mockServiceId,
            documents: [{ id: handbookDoc.id, name: 'handbook.txt' }, { id: glossaryDoc.id, name: 'glossary.md' }],
        });
        expect(agent.documents?.map((d) => d.name)).toEqual(['handbook.txt', 'glossary.md']);

        const agentPage = await loginToAgents(page);
        await agentPage.openAgentEditor('Docs History Agent');
        await agentPage.getDocumentRow('glossary.md').getByRole('button', { name: 'Rename glossary.md' }).click();
        await agentPage.getDocumentsSection().getByLabel('Document name').fill('Glossary 2026.md');
        await agentPage.getDocumentsSection().getByRole('button', { name: 'Done' }).click();
        await expect(agentPage.getDocumentRows().nth(1)).toContainText('Glossary 2026.md');
        // A renamed document is an unsaved change.
        await agentPage.getBackButton().click();
        await expect(agentPage.getDiscardChangesDialog()).toBeVisible();
        await agentPage.getDiscardChangesKeepEditingButton().click();
        await agentPage.getModalSaveButton().click();
        await agentPage.waitForModalClosed();

        await agentPage.openAgentEditor('Docs History Agent');
        await agentPage.getDocumentRow('handbook.txt').getByRole('button', { name: 'Remove handbook.txt' }).click();
        await expect(agentPage.getDocumentRows()).toHaveCount(1);
        await expect(agentPage.getDocumentsUsage()).toHaveText(usageLabel(glossaryText.length));
        await agentPage.getModalSaveButton().click();
        await agentPage.waitForModalClosed();

        const current = await agentApi.getAgent(token, agent.id);
        expect(current.documents).toEqual([{ ...expectedDocument(glossary, glossaryText, 'Glossary 2026.md'), id: glossaryDoc.id }]);
        const history = await agentApi.getAgentVersions(token, agent.id);
        expect(history.currentVersion).toBe(3);
        expect(history.versions.slice(0, 2).map((v) => v.changedFields)).toEqual([['documents'], ['documents']]);

        // Old versions keep the documents they had, under the names they had.
        await agentPage.openAgentEditor('Docs History Agent');
        await agentPage.openHistoryVersion(2);
        await expect(agentPage.getVersionSnapshotDocuments().getByRole('listitem')).toHaveCount(2);
        await expect(agentPage.getVersionSnapshotDocuments()).toContainText('Glossary 2026.md');
        await agentPage.openHistoryVersion(1);
        await expect(agentPage.getVersionSnapshotDocuments().getByRole('listitem')).toHaveCount(2);
        await expect(agentPage.getVersionSnapshotDocuments()).toContainText('glossary.md');
        await expect(agentPage.getVersionSnapshotDocuments()).toContainText(sizeLabel(handbook.buffer.length));

        // The removed document still downloads from the version that had it.
        const removed = await captureDownload(page, () =>
            agentPage.getVersionSnapshotDocuments().getByRole('link', { name: 'handbook.txt' }).click());
        expect(removed.filename).toBe('handbook.txt');
        expect(removed.bytes.equals(handbook.buffer)).toBe(true);

        await agentPage.getRestoreVersionButton().click();
        await agentPage.getRestoreConfirmButton().click();
        await expect(agentPage.getRestoreDialog()).toBeHidden({ timeout: 10000 });
        await expect(page.getByText('Version 1 was restored as the current version.')).toBeVisible({ timeout: 10000 });

        await agentPage.getModalTab('Configuration').click();
        await expect(agentPage.getDocumentRows()).toHaveCount(2);
        await expect(agentPage.getDocumentRows().nth(0)).toContainText('handbook.txt');
        await expect(agentPage.getDocumentRows().nth(1)).toContainText('glossary.md');
        await expect(agentPage.getDocumentsUsage()).toHaveText(usageLabel(handbookText.length + glossaryText.length));
        // Restored documents are saved, so they can be downloaded right away.
        const restoredDownload = await captureDownload(page, () =>
            agentPage.getDocumentRow('handbook.txt').getByRole('button', { name: 'Download handbook.txt' }).click());
        expect(restoredDownload.bytes.equals(handbook.buffer)).toBe(true);

        const restored = await agentApi.getAgent(token, agent.id);
        expect(restored.documents).toEqual([
            { ...expectedDocument(handbook, handbookText), id: handbookDoc.id },
            { ...expectedDocument(glossary, glossaryText), id: glossaryDoc.id },
        ]);
        const afterRestore = await agentApi.getAgentVersions(token, agent.id);
        expect(afterRestore.currentVersion).toBe(4);
        expect(afterRestore.versions[0]).toMatchObject({ source: 'restore', restoredFromVersion: 1 });
        expect(afterRestore.versions[0].changedFields).toContain('documents');
    });

    test('the agent receives its document text in the system prompt after the custom instructions', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const token = await adminToken();
        const instructions = 'INSTRUCTIONS-4410: You are the office assistant.';
        const policyText = 'POLICY-7731: Badges must be worn at all times.';
        const policy = await agentApi.uploadAgentDocumentOrThrow(token, {
            name: 'policy.txt',
            mimeType: 'text/plain',
            buffer: Buffer.from(policyText),
        });
        const agent = await createAgent(agentApi, token, {
            displayName: 'Docs Runtime Agent',
            username: 'docsruntimeagent',
            serviceID: mockServiceId,
            customInstructions: instructions,
            documents: [{ id: policy.id, name: 'Badge policy.txt' }],
        });

        const question = 'Do I need to wear my badge in the kitchen?';
        const reply = 'Yes, badges are required everywhere.';
        await openAIMock.addMocks([
            buildChatCompletionMockRule(buildTextResponse('The reference document was missing from the prompt.')),
            titleGenerationMockRule(),
            buildChatCompletionMockRule(buildTextResponse(reply), { bodyContains: 'POLICY-7731' }),
        ]);

        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), agentAdminUsername, agentAdminPassword);
        const { client, channelId, botUserId } = await mmPage.getClientAndDmChannelForBot(
            mattermost, agentAdminUsername, agentAdminPassword, agent.name);
        await page.goto(`${mattermost.url()}/test/messages/@${agent.name}`);
        await page.getByTestId('channel_view').waitFor({ state: 'visible', timeout: 30000 });
        await mmPage.sendChannelMessage(question);

        await expect.poll(async () => {
            const posts = await client.getPosts(channelId, 0, 50);
            return Object.values(posts.posts).some((p) => p.user_id === botUserId && p.message.includes('badges are required everywhere'));
        }, { timeout: 45000, intervals: [500, 1000, 2000] }).toBe(true);

        const requests = (await openAIMock.getRequestHistory()).filter((r) =>
            r.path.endsWith('/chat/completions') && r.body.includes(question) && !r.body.includes(TITLE_GENERATION_BODY_MATCH));
        expect(requests.length).toBeGreaterThan(0);
        const prompt = systemPrompt(requests[requests.length - 1].body);
        const documentBlock = `<document name="Badge policy.txt">\n${policyText}\n</document>`;
        expect(prompt).toContain(instructions);
        expect(prompt).toContain('The agent administrators provided the following reference documents.');
        expect(prompt).toContain(documentBlock);
        expect(prompt.indexOf(instructions)).toBeLessThan(prompt.indexOf(documentBlock));

        // After the agent's documents change, the next request uses the new ones.
        const updatedText = 'POLICY-8842: Visitors need an escort.';
        const updatedPolicy = await agentApi.uploadAgentDocumentOrThrow(token, {
            name: 'visitors.txt',
            mimeType: 'text/plain',
            buffer: Buffer.from(updatedText),
        });
        await agentApi.updateAgent(token, agent.id, { documents: [{ id: updatedPolicy.id, name: 'Visitor policy.txt' }] });
        const followUp = 'Can my friend visit the office alone?';
        await openAIMock.appendMocks([
            buildChatCompletionMockRule(buildTextResponse('No, visitors need an escort.'), { bodyContains: 'POLICY-8842' }),
        ]);
        await mmPage.sendChannelMessage(followUp);
        await expect.poll(async () => {
            const posts = await client.getPosts(channelId, 0, 50);
            return Object.values(posts.posts).some((p) => p.user_id === botUserId && p.message.includes('visitors need an escort'));
        }, { timeout: 45000, intervals: [500, 1000, 2000] }).toBe(true);

        const followUps = (await openAIMock.getRequestHistory()).filter((r) =>
            r.path.endsWith('/chat/completions') && r.body.includes(followUp) && !r.body.includes(TITLE_GENERATION_BODY_MATCH));
        expect(followUps.length).toBeGreaterThan(0);
        const updatedPrompt = systemPrompt(followUps[followUps.length - 1].body);
        expect(updatedPrompt).toContain(`<document name="Visitor policy.txt">\n${updatedText}\n</document>`);
        expect(updatedPrompt).not.toContain(policyText);
    });

    test('exported documents carry their bytes and import recreates them', async ({ page }) => {
        test.setTimeout(120000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const token = await adminToken();
        const handbookDoc = await agentApi.uploadAgentDocumentOrThrow(token, handbook);
        const refundsDoc = await agentApi.uploadAgentDocumentOrThrow(token, refunds);
        const source = await createAgent(agentApi, token, {
            displayName: 'Docs Export Agent',
            username: 'docsexportagent',
            serviceID: mockServiceId,
            customInstructions: 'Exported with documents.',
            documents: [{ id: handbookDoc.id, name: 'handbook.txt' }, { id: refundsDoc.id, name: 'Refunds 2026.pdf' }],
        });

        const agentPage = await loginToAgents(page);
        await agentPage.openAgentActions('Docs Export Agent');
        const [download] = await Promise.all([
            page.waitForEvent('download', { timeout: 15000 }),
            agentPage.clickExportAction('Docs Export Agent'),
        ]);
        const exportPath = await download.path();
        const doc = JSON.parse(fs.readFileSync(exportPath, 'utf8')) as AgentExportDocument;
        expect(doc.schemaVersion).toBe(2);
        expect(doc.agent.documents).toEqual([
            { name: 'handbook.txt', mimeType: 'text/plain', size: handbook.buffer.length, sha256: sha256(handbook.buffer), content: handbook.buffer.toString('base64') },
            { name: 'Refunds 2026.pdf', mimeType: 'application/pdf', size: refunds.buffer.length, sha256: sha256(refunds.buffer), content: refunds.buffer.toString('base64') },
        ]);

        await agentPage.openImportModal();
        await agentPage.getImportDialog().getByTestId('import-agent-file-input').setInputFiles({
            name: 'huge.agent.json',
            mimeType: 'application/json',
            buffer: Buffer.alloc(40 * 1024 * 1024 + 1, ' '),
        });
        await expect(agentPage.getImportDialog()).toContainText('This file is too large. Agent files can be up to 40 MB.');
        await agentPage.chooseImportFile(exportPath);
        const totalSize = sizeLabel(handbook.buffer.length + refunds.buffer.length);
        await expect(agentPage.getImportSummary().getByTestId('import-summary-documents'))
            .toHaveText(`Reference documents: 2 documents (${totalSize}): handbook.txt, Refunds 2026.pdf`);
        await expect(agentPage.getImportModeRadio('update')).toBeChecked();
        await agentPage.getImportModeRadio('create').check();
        await agentPage.getImportUsernameInput().fill('docsimportedagent');
        await agentPage.getImportDisplayNameInput().fill('Docs Imported Agent');
        await agentPage.getImportServiceSelect().selectOption({ label: 'Mock Service' });
        await agentPage.getImportSubmitButton().click();
        await expect(agentPage.getImportDialog()).toBeHidden({ timeout: 15000 });
        await expect(page.getByText('Imported agent Docs Imported Agent.')).toBeVisible({ timeout: 10000 });

        const imported = await findAgentByName(agentApi, token, 'docsimportedagent');
        expect(imported).toBeDefined();
        expect(withoutIds(imported!.documents)).toEqual(withoutIds(source.documents));
        expect(await downloadBytes(agentApi, token, imported!.id, imported!.documents![0].id)).toEqual(handbook.buffer);
        expect(await downloadBytes(agentApi, token, imported!.id, imported!.documents![1].id)).toEqual(refunds.buffer);
        expect((await agentApi.getAgentVersions(token, imported!.id)).versions.map((v) => v.source)).toEqual(['import']);

        // Importing into an existing agent replaces its documents with the file's.
        const glossaryDoc = await agentApi.uploadAgentDocumentOrThrow(token, glossary);
        const target = await createAgent(agentApi, token, {
            displayName: 'Docs Update Target',
            username: 'docsupdatetarget',
            serviceID: mockServiceId,
            documents: [{ id: glossaryDoc.id, name: 'glossary.md' }],
        });
        const updated = await agentApi.importAgent(token, { document: doc, mode: 'update', agentID: target.id });
        expect(updated.status).toBe(200);
        expect(withoutIds(updated.body.documents)).toEqual(withoutIds(source.documents));

        // A schema version 1 file carries no documents, so an update keeps the agent's.
        const { documents: _documents, ...v1Agent } = doc.agent;
        const v1Update = await agentApi.importAgent(token, {
            document: { ...doc, schemaVersion: 1, agent: { ...v1Agent, customInstructions: 'Updated from a v1 file.' } },
            mode: 'update',
            agentID: target.id,
        });
        expect(v1Update.status).toBe(200);
        expect(v1Update.body.customInstructions).toBe('Updated from a v1 file.');
        expect(v1Update.body.documents).toEqual(updated.body.documents);

        // Content that does not match its checksum is rejected and creates nothing.
        const tampered: AgentExportDocument = JSON.parse(JSON.stringify(doc));
        tampered.agent.documents![0].sha256 = '0'.repeat(64);
        const rejected = await agentApi.importAgent(token, {
            document: tampered,
            mode: 'create',
            username: 'docstamperedagent',
            displayName: 'Docs Tampered Agent',
            serviceID: mockServiceId,
        });
        expect(rejected.status).toBe(400);
        expect(rejected.body.error).toContain('sha256');
        expect(await findAgentByName(agentApi, token, 'docstamperedagent')).toBeUndefined();
    });

    test('unusable files are rejected and over-budget documents block Save', async ({ page }) => {
        test.setTimeout(120000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const token = await adminToken();
        const agent = await createAgent(agentApi, token, {
            displayName: 'Docs Limits Agent',
            username: 'docslimitsagent',
            serviceID: mockServiceId,
        });

        const agentPage = await loginToAgents(page);
        await agentPage.openAgentEditor('Docs Limits Agent');
        await agentPage.getDocumentsFileInput().setInputFiles([
            { name: 'setup.exe', mimeType: 'application/octet-stream', buffer: Buffer.from('MZ fake executable') },
            { name: 'scan.pdf', mimeType: 'application/pdf', buffer: buildPDF([]) },
            { name: 'blank.txt', mimeType: 'text/plain', buffer: Buffer.from('  \n\n  \n') },
        ]);
        await expect(agentPage.getDocumentUploadRow('setup.exe')).toContainText('Unsupported file type.');
        await expect(agentPage.getDocumentUploadRow('scan.pdf')).toContainText('no extractable text found in "scan.pdf"', { timeout: 15000 });
        await expect(agentPage.getDocumentUploadRow('blank.txt')).toContainText('no extractable text found in "blank.txt"', { timeout: 15000 });
        await expect(agentPage.getDocumentRows()).toHaveCount(0);
        await agentPage.getDocumentUploadRow('setup.exe').getByRole('button', { name: 'Dismiss error for setup.exe' }).click();
        await expect(agentPage.getDocumentUploadRow('setup.exe')).toHaveCount(0);

        // Each file fits on its own; together they are over the agent's text budget.
        const partA = { name: 'part-a.txt', mimeType: 'text/plain', buffer: Buffer.from('abcdefghij'.repeat(6000)) };
        const partB = { name: 'part-b.txt', mimeType: 'text/plain', buffer: Buffer.from('klmnopqrst'.repeat(6000)) };
        await agentPage.getDocumentsFileInput().setInputFiles([partA, partB]);
        await expect(agentPage.getDocumentRows()).toHaveCount(2, { timeout: 15000 });
        await expect(agentPage.getDocumentsUsage()).toHaveText(usageLabel(120000));
        await agentPage.getModalSaveButton().click();
        await expect(agentPage.getDocumentsSection().getByRole('alert').filter({
            hasText: 'Reference documents contain 120,000 characters of extracted text, over the limit of 100,000. Remove a document to continue.',
        })).toBeVisible();
        await expect(agentPage.getDisplayNameInput()).toBeVisible();
        expect((await agentApi.getAgentVersions(token, agent.id)).currentVersion).toBe(1);

        await agentPage.getDocumentRow('part-b.txt').getByRole('button', { name: 'Remove part-b.txt' }).click();
        await agentPage.getModalSaveButton().click();
        await agentPage.waitForModalClosed();
        const saved = await agentApi.getAgent(token, agent.id);
        expect(saved.documents?.map((d) => [d.name, d.textRunes])).toEqual([['part-a.txt', 60000]]);

        // The server enforces the same limits.
        const partBDoc = await agentApi.uploadAgentDocumentOrThrow(token, partB);
        const overBudget = await agentApi.putAgentRaw(token, agent.id, {
            displayName: saved.displayName,
            username: saved.name,
            serviceID: saved.serviceID,
            autoEnableNewMCPTools: saved.autoEnableNewMCPTools,
            documents: [{ id: saved.documents![0].id, name: 'part-a.txt' }, { id: partBDoc.id, name: 'part-b.txt' }],
        });
        expect(overBudget.status).toBe(400);
        expect(overBudget.body.error).toBe('documents exceed the limit of 100000 characters of extracted text (have 120000)');

        for (const { file, status, error } of [
            { file: { name: 'diagram.png', mimeType: 'image/png', buffer: Buffer.from('\x89PNG\r\n\x1a\n') }, status: 400, error: 'is not a supported document type' },
            { file: { name: 'scan.pdf', mimeType: 'application/pdf', buffer: buildPDF([]) }, status: 400, error: 'no extractable text found in "scan.pdf"' },
            { file: { name: 'huge.txt', mimeType: 'text/plain', buffer: Buffer.from('a'.repeat(maxTextRunes + 1)) }, status: 400, error: 'more than 100000 characters' },
            { file: { name: 'big.txt', mimeType: 'text/plain', buffer: Buffer.alloc(10 * 1024 * 1024 + 1, 'a') }, status: 413, error: 'document is too large (max 10 MiB)' },
        ]) {
            const response = await agentApi.uploadAgentDocument(token, file);
            expect(response.status, file.name).toBe(status);
            expect(response.body.error, file.name).toContain(error);
        }
    });

    test('only managers can see or download documents, and agents can only reference their own uploads', async () => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const routes = mattermostAIPluginRoutes(mattermost.url());
        const token = await adminToken();
        const secretText = 'SECRET-5521: internal escalation contacts.';
        const secret = await agentApi.uploadAgentDocumentOrThrow(token, {
            name: 'contacts.txt',
            mimeType: 'text/plain',
            buffer: Buffer.from(secretText),
        });
        const agent = await createAgent(agentApi, token, {
            displayName: 'Docs Private Agent',
            username: 'docsprivateagent',
            serviceID: mockServiceId,
            userAccessLevel: 0,
            documents: [{ id: secret.id, name: 'contacts.txt' }],
        });

        const regularToken = (await mattermost.getClient(agentRegularUsername, agentRegularPassword)).getToken();
        const asUser = await agentApi.getAgent(regularToken, agent.id);
        expect(asUser.documents).toEqual([]);
        const listed = (await agentApi.getAgents(regularToken)).find((a) => a.id === agent.id);
        expect(listed?.documents).toEqual([]);
        for (const route of [`agents/${agent.id}/documents/${secret.id}`, `agents/${agent.id}/documents/${secret.id}/text`]) {
            const response = await routes.request('GET', route, regularToken);
            expect([403, 404], route).toContain(response.status);
            expect(await response.text()).not.toContain(secretText);
        }

        // A manager cannot attach another user's upload, and nor can that user attach the manager's.
        const mine = await agentApi.uploadAgentDocumentOrThrow(regularToken, {
            name: 'mine.txt',
            mimeType: 'text/plain',
            buffer: Buffer.from('Uploaded by the regular user.'),
        });
        const current = await agentApi.getAgent(token, agent.id);
        const hijack = await agentApi.putAgentRaw(token, agent.id, {
            displayName: current.displayName,
            username: current.name,
            serviceID: current.serviceID,
            autoEnableNewMCPTools: current.autoEnableNewMCPTools,
            userAccessLevel: current.userAccessLevel,
            documents: [{ id: secret.id, name: 'contacts.txt' }, { id: mine.id, name: 'mine.txt' }],
        });
        expect(hijack.status).toBe(400);
        expect(hijack.body.error).toContain('unknown document');
        expect((await agentApi.getAgent(token, agent.id)).documents?.map((d) => d.id)).toEqual([secret.id]);

        const borrowed = await routes.request('POST', 'agents', regularToken, {
            displayName: 'Docs Borrowing Agent',
            username: 'docsborrowingagent',
            serviceID: mockServiceId,
            autoEnableNewMCPTools: true,
            documents: [{ id: secret.id, name: 'contacts.txt' }],
        });
        expect(borrowed.status).toBe(400);
        expect(await borrowed.text()).toContain('unknown document');
        expect(await findAgentByName(agentApi, token, 'docsborrowingagent')).toBeUndefined();

        // A user's own uploads work on their own agent.
        const own = await routes.request('POST', 'agents', regularToken, {
            displayName: 'Docs Own Agent',
            username: 'docsownagent',
            serviceID: mockServiceId,
            autoEnableNewMCPTools: true,
            documents: [{ id: mine.id, name: 'mine.txt' }],
        });
        expect(own.status).toBe(201);
        const ownAgent = await own.json() as AgentResponse;
        expect(ownAgent.documents?.map((d) => d.name)).toEqual(['mine.txt']);
        expect(await downloadBytes(agentApi, regularToken, ownAgent.id, mine.id)).toEqual(Buffer.from('Uploaded by the regular user.'));

        // Another manager of the agent keeps, downloads and exports documents they did not upload.
        const regularUser = await (await mattermost.getClient(agentRegularUsername, agentRegularPassword)).getMe();
        await agentApi.updateAgent(token, agent.id, { adminUserIDs: [regularUser.id] });
        const asManager = await agentApi.getAgent(regularToken, agent.id);
        expect(asManager.documents?.map((d) => d.id)).toEqual([secret.id]);
        expect(await downloadBytes(agentApi, regularToken, agent.id, secret.id)).toEqual(Buffer.from(secretText));
        const managerSave = await agentApi.putAgentRaw(regularToken, agent.id, {
            ...mergeAgentIntoUpdate(asManager, { customInstructions: 'Edited by another manager.' }),
        });
        expect(managerSave.status).toBe(200);
        expect(managerSave.body.documents?.map((d) => d.id)).toEqual([secret.id]);
        const managerExport = await agentApi.exportAgent(regularToken, agent.id);
        expect(managerExport.status).toBe(200);
        expect(managerExport.body.agent.documents?.[0].content).toBe(Buffer.from(secretText).toString('base64'));

        await mattermost.revokeManageOwnAgentFromSystemUser();
        try {
            const unprivilegedToken = (await mattermost.getClient(agentUnprivilegedUsername, agentUnprivilegedPassword)).getToken();
            const upload = await agentApi.uploadAgentDocument(unprivilegedToken, handbook);
            expect(upload.status).toBe(403);
        } finally {
            await mattermost.grantSelfServiceAgentPermissions();
        }
    });
});
