// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {test, expect} from '@playwright/test';
import type {Page} from '@playwright/test';
import type {Post} from '@mattermost/types/posts';

import {AIMOCK_BOT_NAME, RunAIMockContainer} from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import {MattermostPage} from 'helpers/mm';
import {AIPlugin} from 'helpers/ai-plugin';
import {
    buildChatCompletionMockRule,
    buildTextResponse,
    OpenAIMockContainer,
    RunOpenAIMocks,
} from 'helpers/openai-mock';
import type {
    OpenAIChatCompletionRequest,
    OpenAIChatMessage,
} from 'helpers/openai-mock';

const username = 'regularuser';
const password = 'regularuser';
const previewUsername = 'admin';
const previewPassword = 'admin';

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.beforeAll(async () => {
    test.setTimeout(180000);
    mattermost = await RunAIMockContainer({
        bot: {
            enableVision: true,
            disableTools: true,
        },
    });
    openAIMock = await RunOpenAIMocks(mattermost.network);
});

test.afterAll(async () => {
    await openAIMock.stop();
    await mattermost.stop();
});

async function setupTestPage(page: Page, loginUsername = username, loginPassword = password) {
    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);
    await mmPage.login(mattermost.url(), loginUsername, loginPassword);
    await aiPlugin.openRHS();
    return {mmPage, aiPlugin};
}

// Synthesizes a file drop on the given element by building a DataTransfer in
// the page context, attaching the file payload, and dispatching the standard
// drag sequence (dragenter, dragover, drop). Browser automation can't perform
// a true OS-level drag, so this is the canonical way to exercise drag-drop
// handlers from Playwright.
async function dispatchFileDrop(page: Page, selector: string, fileName: string, mimeType: string, content: string) {
    const dataTransfer = await page.evaluateHandle(
        ({name, type, body}) => {
            const dt = new DataTransfer();
            dt.items.add(new File([body], name, {type}));
            return dt;
        },
        {name: fileName, type: mimeType, body: content},
    );

    await page.dispatchEvent(selector, 'dragenter', {dataTransfer});
    await page.dispatchEvent(selector, 'dragover', {dataTransfer});
    await page.dispatchEvent(selector, 'drop', {dataTransfer});
}

async function dispatchGeneratedPngDrop(page: Page, selector: string, fileName: string) {
    const dataTransfer = await page.evaluateHandle(async (name) => {
        const canvas = document.createElement('canvas');
        canvas.width = 2;
        canvas.height = 2;
        const context = canvas.getContext('2d');
        if (!context) {
            throw new Error('Canvas 2D context is unavailable');
        }

        context.fillStyle = '#e01e5a';
        context.fillRect(0, 0, 1, 2);
        context.fillStyle = '#3f4350';
        context.fillRect(1, 0, 1, 2);

        const png = await new Promise<Blob>((resolve, reject) => {
            canvas.toBlob((blob) => {
                if (blob) {
                    resolve(blob);
                } else {
                    reject(new Error('Failed to encode generated PNG'));
                }
            }, 'image/png');
        });

        const dt = new DataTransfer();
        dt.items.add(new File([png], name, {type: 'image/png'}));
        return dt;
    }, fileName);

    await page.dispatchEvent(selector, 'dragenter', {dataTransfer});
    await page.dispatchEvent(selector, 'dragover', {dataTransfer});
    await page.dispatchEvent(selector, 'drop', {dataTransfer});
}

function buildProviderRule(responseText: string, bodyMatchers: Record<string, unknown>) {
    const rule = buildChatCompletionMockRule(buildTextResponse(responseText), {times: 1});
    rule.request.body = bodyMatchers;
    return rule;
}

function postsFrom(response: {posts: Record<string, Post>}): Post[] {
    return Object.values(response.posts);
}

function isRecord(value: unknown): value is Record<string, unknown> {
    return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isChatCompletionRequest(value: unknown): value is OpenAIChatCompletionRequest {
    if (!isRecord(value) || !Array.isArray(value.messages)) {
        return false;
    }

    return value.messages.every((message) => (
        isRecord(message) &&
        (
            typeof message.content === 'string' ||
            (
                Array.isArray(message.content) &&
                message.content.every((part) => isRecord(part))
            )
        )
    ));
}

function promptUserMessages(request: OpenAIChatCompletionRequest, prompt: string): OpenAIChatMessage[] {
    return (request.messages ?? []).filter((message) => (
        message.role === 'user' &&
        Array.isArray(message.content) &&
        message.content.some((part) => part.type === 'text' && part.text === prompt)
    ));
}

function assertGeneratedPngDataUrl(imageURL: string) {
    const match = /^data:image\/png;base64,([A-Za-z0-9+/]+={0,2})$/.exec(imageURL);
    expect(match, 'provider image URL must contain base64-encoded PNG data').not.toBeNull();
    if (!match) {
        throw new Error('Provider image URL was not a PNG data URL');
    }

    const encodedPng = match[1];
    const pngBytes = Buffer.from(encodedPng, 'base64');
    expect(pngBytes.length).toBeGreaterThan(24);
    expect(pngBytes.toString('base64')).toBe(encodedPng);
    expect(pngBytes.subarray(0, 8)).toEqual(Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]));
    expect(pngBytes.subarray(12, 16).toString('ascii')).toBe('IHDR');
    expect(pngBytes.readUInt32BE(16)).toBe(2);
    expect(pngBytes.readUInt32BE(20)).toBe(2);
}

test.describe('Agents RHS drag-and-drop file upload', () => {
    test('drops a file onto the RHS new-tab and attaches it to the editor', async ({page}) => {
        const previewClient = await mattermost.getClient(previewUsername, previewPassword);
        const previewUser = await previewClient.getMe();
        const bot = await previewClient.getUserByUsername(AIMOCK_BOT_NAME);
        await previewClient.createDirectChannel([previewUser.id, bot.id]);

        const {aiPlugin} = await setupTestPage(page, previewUsername, previewPassword);

        const rhs = aiPlugin.getRhsContainer();
        await expect(rhs).toBeVisible();

        await dispatchFileDrop(
            page,
            '[data-testid="rhs-file-drop-zone"]',
            'agents-dnd.txt',
            'text/plain',
            'hello from the agents drag-and-drop test',
        );

        const previewFilename = rhs.getByText('agents-dnd.txt', {exact: true});
        await expect(previewFilename).toBeVisible();
        const preview = previewFilename.locator('xpath=ancestor::*[contains(@class, "file-preview")][1]');
        await preview.locator('.file-preview__remove').click();
        await expect(previewFilename).toHaveCount(0);
    });

    test('uploads a dropped image for vision analysis and restores it from history', async ({page}) => {
        test.setTimeout(120000);

        const runID = Date.now();
        const marker = `RHS_VISION_DND_${runID}`;
        const prompt = `Analyze the attached generated image ${marker}`;
        const response = `Vision analysis completed for ${marker}`;
        const missingImageResponse = `FAIL_IMAGE_PAYLOAD_MISSING_${marker}`;
        const title = `Vision upload ${marker}`;
        const fileName = `vision-${runID.toString(36)}.png`;

        await openAIMock.addMocks([
            buildProviderRule(response, {
                'messages[1].content[0].text': prompt,
                'messages[1].content[1].type': 'image_url',
                'messages[1].content[1].image_url.url': {
                    matcher: 'ShouldStartWith',
                    value: 'data:image/png;base64,',
                },
            }),
            buildProviderRule(title, {
                'messages[0].content': {
                    matcher: 'ShouldContainSubstring',
                    value: prompt,
                },
            }),
            // Smocker gives the last registered matching rule priority, so the
            // text-only trap must be registered after the successful rules.
            buildProviderRule(missingImageResponse, {
                'messages[1].content': prompt,
            }),
        ]);

        const userClient = await mattermost.getClient(username, password);
        const user = await userClient.getMe();
        const bot = await userClient.getUserByUsername(AIMOCK_BOT_NAME);
        const dmChannel = await userClient.createDirectChannel([user.id, bot.id]);

        const {aiPlugin} = await setupTestPage(page);
        const rhs = aiPlugin.getRhsContainer();
        await expect(rhs).toBeVisible();

        await dispatchGeneratedPngDrop(page, '[data-testid="rhs-file-drop-zone"]', fileName);
        await expect(rhs.getByText(fileName, {exact: true})).toBeVisible();

        await aiPlugin.rhsPostTextarea.fill(prompt);
        await expect(aiPlugin.rhsSendButton).toBeEnabled({timeout: 30000});
        await aiPlugin.rhsSendButton.click();

        let mainRequests: OpenAIChatCompletionRequest[] = [];
        await expect.poll(async () => {
            const history = await openAIMock.getHistory();
            mainRequests = history.
                map((entry) => entry.request.body).
                filter(isChatCompletionRequest).
                filter((request) => promptUserMessages(request, prompt).length > 0);
            return mainRequests.length;
        }, {
            message: 'Provider history did not contain exactly one main request with the unique user prompt',
            timeout: 30000,
            intervals: [250, 500, 1000],
        }).toBe(1);

        expect(mainRequests).toHaveLength(1);
        const mainUserMessages = promptUserMessages(mainRequests[0], prompt);
        expect(mainUserMessages).toHaveLength(1);
        const mainUserContent = mainUserMessages[0].content;
        if (!Array.isArray(mainUserContent)) {
            throw new Error('Main provider user message did not use multimodal content');
        }
        const imageURLs = mainUserContent.flatMap((part) => (
            part.type === 'image_url' && typeof part.image_url?.url === 'string' ? [part.image_url.url] : []
        ));
        expect(imageURLs).toHaveLength(1);
        assertGeneratedPngDataUrl(imageURLs[0]);

        const expectedResponse = rhs.getByText(response, {exact: true});
        const trapResponse = rhs.getByText(missingImageResponse, {exact: true});
        await expect(expectedResponse.or(trapResponse)).toBeVisible({timeout: 60000});
        if (await trapResponse.isVisible()) {
            throw new Error('The provider request contained the prompt but omitted the PNG image payload');
        }
        await aiPlugin.waitForBotResponse(response);
        await expect(trapResponse).toHaveCount(0);

        const settledMainRequests = (await openAIMock.getHistory()).
            map((entry) => entry.request.body).
            filter(isChatCompletionRequest).
            filter((request) => promptUserMessages(request, prompt).length > 0);
        expect(settledMainRequests).toHaveLength(1);

        let uploadedPost: Post | undefined;
        await expect.poll(async () => {
            const posts = postsFrom(await userClient.getPosts(dmChannel.id, 0, 200));
            uploadedPost = posts.find((post) => post.user_id === user.id && post.message === prompt);
            return uploadedPost?.file_ids?.length ?? 0;
        }, {
            message: 'RHS image post did not persist with exactly one uploaded file',
            timeout: 30000,
            intervals: [250, 500, 1000],
        }).toBe(1);

        if (!uploadedPost) {
            throw new Error('Uploaded RHS image post was not found');
        }
        const fileInfos = await userClient.getFileInfosForPost(uploadedPost.id);
        expect(fileInfos).toHaveLength(1);
        expect(fileInfos[0]).toMatchObject({
            id: uploadedPost.file_ids[0],
            channel_id: dmChannel.id,
            name: fileName,
            mime_type: 'image/png',
            width: 2,
            height: 2,
        });
        expect(fileInfos[0].size).toBeGreaterThan(0);

        await page.reload({waitUntil: 'domcontentloaded'});
        await expect(page.getByTestId('channel_view')).toBeVisible({timeout: 60000});
        await aiPlugin.openRHS();
        await aiPlugin.openChatHistory();

        const historyItem = aiPlugin.threadsListContainer.getByText(title, {exact: true});
        await expect(historyItem).toBeVisible({timeout: 30000});
        await historyItem.click();

        const restoredRhs = aiPlugin.getRhsContainer();
        const restoredUserPost = restoredRhs.locator(`#rhsPost_${uploadedPost.id}`);
        await expect(restoredUserPost.getByText(prompt, {exact: true})).toBeVisible({timeout: 30000});
        await expect(restoredRhs.getByText(response, {exact: true})).toBeVisible({timeout: 30000});

        const restoredImage = restoredUserPost.locator('img[src*="/api/v4/files/"]');
        await expect(restoredImage).toBeVisible({timeout: 30000});
        await expect.poll(async () => restoredImage.evaluate((image: HTMLImageElement) => (
            image.complete && image.naturalWidth > 0
        )), {
            message: 'Persisted PNG did not render after reopening the conversation from history',
            timeout: 30000,
            intervals: [250, 500, 1000],
        }).toBe(true);
    });
});
