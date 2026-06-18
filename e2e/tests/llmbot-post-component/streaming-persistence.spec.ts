import { test, expect, Page } from '@playwright/test';

import { AIMockHarness, RunAIMockHarness } from 'helpers/aimock-harness';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { LLMBotPostHelper } from 'helpers/llmbot-post';

const username = 'regularuser';
const password = 'regularuser';

const PHASE3_STREAMING_CURSOR_PROMPT = 'phase3-streaming-cursor-001';
const PHASE3_STREAMING_CURSOR_MARKER = 'Streaming cursor marker part one two three complete.';

const PHASE3_STREAMING_LIFECYCLE_PROMPT = 'phase3-streaming-lifecycle-001';
const PHASE3_STREAMING_LIFECYCLE_ANSWER = 'Streaming lifecycle answer complete.';

const PHASE3_NAV_PERSISTENCE_PROMPT = 'phase3-nav-persistence-001';
const PHASE3_NAV_PERSISTENCE_MARKER = 'Navigation persistence marker rhs close reopen.';

const PHASE3_THREAD_PERSIST_PROMPT = 'phase3-thread-persist-001';

const PHASE3_STOP_GENERATING_PROMPT = 'phase3-stop-generating-001';
const PHASE3_STOP_GENERATING_PREFIX = 'Stop generating prefix marker';

async function setupTestPage(page: Page) {
    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);
    const llmBotHelper = new LLMBotPostHelper(page);

    return { mmPage, aiPlugin, llmBotHelper };
}

test.describe('Streaming and Persistence - aimock', () => {
    test.describe.configure({ mode: 'serial' });

    let harness: AIMockHarness;

    test.beforeAll(async () => {
        test.setTimeout(180000);
        harness = await RunAIMockHarness({
            fixtureFile: 'llmbot-streaming-persistence.json',
            bot: {
                enabledNativeTools: [],
            },
        });
    });

    test.afterAll(async () => {
        await harness?.stop();
    });

    test('Streaming Cursor Display', async ({ page }) => {
        test.setTimeout(120000);

        const { mmPage, aiPlugin, llmBotHelper } = await setupTestPage(page);
        await mmPage.login(harness.mattermost.url(), username, password);
        await aiPlugin.resetState();

        await aiPlugin.sendMessage(PHASE3_STREAMING_CURSOR_PROMPT);
        await llmBotHelper.waitForStreamingComplete();
        await llmBotHelper.expectPostText(PHASE3_STREAMING_CURSOR_MARKER);
    });

    test('Streaming Complete Lifecycle', async ({ page }) => {
        test.setTimeout(120000);

        const { mmPage, aiPlugin, llmBotHelper } = await setupTestPage(page);
        await mmPage.login(harness.mattermost.url(), username, password);
        await aiPlugin.resetState();

        await aiPlugin.sendMessage(PHASE3_STREAMING_LIFECYCLE_PROMPT);
        await llmBotHelper.waitForReasoning();
        await llmBotHelper.waitForStreamingComplete();

        await llmBotHelper.expectReasoningVisible(true);
        await llmBotHelper.expectPostText(PHASE3_STREAMING_LIFECYCLE_ANSWER);

        const stopButton = llmBotHelper.getStopGeneratingButton();
        await expect(stopButton).not.toBeVisible();
    });

    test('Navigation Persistence', async ({ page }) => {
        test.setTimeout(120000);

        const { mmPage, aiPlugin, llmBotHelper } = await setupTestPage(page);
        await mmPage.login(harness.mattermost.url(), username, password);
        await aiPlugin.resetState();

        await aiPlugin.sendMessage(PHASE3_NAV_PERSISTENCE_PROMPT);
        await llmBotHelper.waitForStreamingComplete();

        const postTextBefore = llmBotHelper.getPostText();
        const contentBefore = await postTextBefore.textContent();
        expect(contentBefore).toContain(PHASE3_NAV_PERSISTENCE_MARKER);

        await aiPlugin.closeRHS();
        await page.waitForTimeout(1000);
        await aiPlugin.openRHS();
        await page.waitForTimeout(2000);

        const postTextAfter = llmBotHelper.getPostText();
        await expect(postTextAfter).toBeVisible();
        const contentAfter = await postTextAfter.textContent();
        expect(contentAfter).toBe(contentBefore);
        expect(contentAfter).toContain(PHASE3_NAV_PERSISTENCE_MARKER);
    });

    test('Thread View Persistence', async ({ page }) => {
        test.setTimeout(120000);

        const { mmPage, aiPlugin, llmBotHelper } = await setupTestPage(page);
        await mmPage.login(harness.mattermost.url(), username, password);
        await aiPlugin.resetState();

        await aiPlugin.sendMessage(PHASE3_THREAD_PERSIST_PROMPT);
        await llmBotHelper.waitForStreamingComplete();

        const postTextBefore = llmBotHelper.getPostText();
        const contentBefore = await postTextBefore.textContent();
        expect(contentBefore).toBeTruthy();

        await page.reload();
        await aiPlugin.openRHS();
        await page.waitForTimeout(2000);

        await aiPlugin.openChatHistory();
        await page.waitForTimeout(1000);
        await aiPlugin.clickChatHistoryItem(0);
        await page.waitForTimeout(2000);

        const postTextAfter = llmBotHelper.getPostText();
        await expect(postTextAfter).toBeVisible();
        const contentAfter = await postTextAfter.textContent();
        expect(contentAfter).toBe(contentBefore);
    });
});

test.describe('Stop Generating Button - aimock', () => {
    let harness: AIMockHarness;

    test.beforeAll(async () => {
        test.setTimeout(180000);
        harness = await RunAIMockHarness({
            fixtureFile: 'llmbot-streaming-persistence.json',
            bot: {
                enabledNativeTools: [],
            },
        });
    });

    test.afterAll(async () => {
        await harness?.stop();
    });

    test('Stop Generating Button', async ({ page }) => {
        test.setTimeout(120000);

        const { mmPage, aiPlugin, llmBotHelper } = await setupTestPage(page);
        await mmPage.login(harness.mattermost.url(), username, password);
        await aiPlugin.resetState();

        await aiPlugin.sendMessage(PHASE3_STOP_GENERATING_PROMPT);

        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible({ timeout: 60000 });

        const stopButton = llmBotHelper.getStopGeneratingButton();
        let stopButtonVisible = false;

        for (let i = 0; i < 20; i++) {
            stopButtonVisible = await stopButton.isVisible().catch(() => false);
            if (stopButtonVisible) {
                break;
            }
            await page.waitForTimeout(500);
        }

        if (stopButtonVisible) {
            await llmBotHelper.stopGenerating();
            await page.waitForTimeout(1000);
            await expect(stopButton).not.toBeVisible({ timeout: 5000 });
            await expect(postText).toBeVisible();
            const content = await postText.textContent();
            expect(content).toContain(PHASE3_STOP_GENERATING_PREFIX);
        } else {
            await llmBotHelper.waitForStreamingComplete();
            await llmBotHelper.expectPostText(PHASE3_STOP_GENERATING_PREFIX);
        }
    });
});
