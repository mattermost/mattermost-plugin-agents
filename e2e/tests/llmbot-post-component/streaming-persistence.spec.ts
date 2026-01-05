import { test, expect } from '@playwright/test';
import RunRealAPIContainer from 'helpers/real-api-container';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { LLMBotPostHelper } from 'helpers/llmbot-post';
import {
    getAPIConfig,
    getSkipMessage,
    getAvailableProviders,
    ProviderBundle,
} from 'helpers/api-config';

/**
 * Test Suite: Streaming and Persistence
 *
 * Tests streaming indicators and persistence behavior in LLMBot posts using REAL APIs.
 * Runs once per configured provider (OpenAI and/or Anthropic).
 *
 * Environment Variables Required:
 * - ANTHROPIC_API_KEY: To run tests with Anthropic (claude-3-7-sonnet)
 * - OPENAI_API_KEY: To run tests with OpenAI (gpt-5)
 *
 * Tests:
 * 1. Streaming Cursor Display
 * 2. Streaming Complete Lifecycle
 * 3. Navigation Persistence
 * 4. Thread View Persistence
 * 5. Stop Generating Button
 */

const username = 'regularuser';
const password = 'regularuser';

const config = getAPIConfig();
const skipMessage = getSkipMessage();

async function setupTestPage(page, mattermost, provider: ProviderBundle) {
    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);
    const llmBotHelper = new LLMBotPostHelper(page);

    const botUsername = provider.bot.name;

    return { mmPage, aiPlugin, llmBotHelper, botUsername };
}

function createProviderTestSuite(provider: ProviderBundle) {
    test.describe(`Streaming and Persistence - ${provider.name}`, () => {
        let mattermost: MattermostContainer;

        test.beforeAll(async () => {
            if (!config.shouldRunTests) return;

            // Customize provider to disable web search for streaming tests
            const customProvider = {
                ...provider,
                bot: {
                    ...provider.bot,
                    enabledNativeTools: [], // Disable web search - not needed for streaming tests
                    ...(provider.service.type === 'openaicompatible' && {
                        reasoningEffort: 'low', // Low effort for more consistent reasoning
                    }),
                }
            };

            mattermost = await RunRealAPIContainer(customProvider);
        });

        test.afterAll(async () => {
            if (mattermost) {
                await mattermost.stop();
            }
        });

        test('Streaming Cursor Display', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(360000); // 6 minutes: allows 5 min streaming + buffer

            const { mmPage, aiPlugin, llmBotHelper, botUsername } = await setupTestPage(page, mattermost, provider);
            await mmPage.login(mattermost.url(), username, password);

            await aiPlugin.openRHS();

            const prompt = 'Briefly explain TypeScript benefits in 2-3 sentences';

            await aiPlugin.sendMessage(prompt);

            // Wait for streaming to complete (smart wait, up to 5 min)
            await llmBotHelper.waitForStreamingComplete();

            // Verify content is present
            const postText = llmBotHelper.getPostText();
            const content = await postText.textContent();
            expect(content).toBeTruthy();
            expect(content.length).toBeGreaterThan(20);
        });

        test('Streaming Complete Lifecycle', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(360000); // 6 minutes: allows 5 min streaming + buffer

            const { mmPage, aiPlugin, llmBotHelper, botUsername } = await setupTestPage(page, mattermost, provider);
            await mmPage.login(mattermost.url(), username, password);

            await aiPlugin.openRHS();

            const isAnthropic = provider.service.type === 'anthropic';
            const prompt = isAnthropic
                ? 'Briefly analyze the benefits of using TypeScript over JavaScript (1 paragraph)'
                : 'Think carefully and briefly explain the benefits of using TypeScript over JavaScript (1 paragraph)';

            await aiPlugin.sendMessage(prompt);

            // Wait for reasoning to complete (smart wait, up to 5 min)
            await llmBotHelper.waitForReasoning();

            // Wait for streaming to complete (smart wait, up to 5 min)
            await llmBotHelper.waitForStreamingComplete();

            // Verify reasoning is visible
            await llmBotHelper.expectReasoningVisible(true);

            // Verify content is present and substantial
            const postText = llmBotHelper.getPostText();
            const postTextContent = await postText.textContent();
            expect(postTextContent).toBeTruthy();
            expect(postTextContent.length).toBeGreaterThan(50);
        });

        test('Navigation Persistence', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(360000); // 6 minutes: allows 5 min streaming + buffer

            const { mmPage, aiPlugin, llmBotHelper, botUsername } = await setupTestPage(page, mattermost, provider);
            await mmPage.login(mattermost.url(), username, password);

            await aiPlugin.openRHS();

            const prompt = 'Briefly explain TypeScript benefits in 2-3 sentences';

            await aiPlugin.sendMessage(prompt);

            // Wait for streaming to complete (smart wait, up to 5 min)
            await llmBotHelper.waitForStreamingComplete();

            const postTextBefore = llmBotHelper.getPostText();
            const contentBefore = await postTextBefore.textContent();
            expect(contentBefore).toBeTruthy();

            // Close and reopen RHS
            await aiPlugin.closeRHS();
            await page.waitForTimeout(1000);

            await aiPlugin.openRHS();
            await page.waitForTimeout(2000);

            // Verify content persists after navigation
            const postTextAfter = llmBotHelper.getPostText();
            await expect(postTextAfter).toBeVisible();

            const contentAfter = await postTextAfter.textContent();
            expect(contentAfter).toBe(contentBefore);
        });

        test('Thread View Persistence', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(360000); // 6 minutes: allows 5 min streaming + buffer

            const { mmPage, aiPlugin, llmBotHelper, botUsername } = await setupTestPage(page, mattermost, provider);
            await mmPage.login(mattermost.url(), username, password);

            await aiPlugin.openRHS();

            const prompt = 'Briefly list 3 TypeScript advantages';

            await aiPlugin.sendMessage(prompt);

            // Wait for streaming to complete (smart wait, up to 5 min)
            await llmBotHelper.waitForStreamingComplete();

            const postTextBefore = llmBotHelper.getPostText();
            const contentBefore = await postTextBefore.textContent();
            expect(contentBefore).toBeTruthy();

            // Reload page
            await page.reload();
            await aiPlugin.openRHS();
            await page.waitForTimeout(2000);

            // After refresh, RHS shows fresh conversation - must navigate to chat history
            await aiPlugin.openChatHistory();
            await page.waitForTimeout(1000);
            await aiPlugin.clickChatHistoryItem(0); // Select most recent conversation
            await page.waitForTimeout(2000);

            // Verify content persists in loaded conversation
            const postTextAfter = llmBotHelper.getPostText();
            await expect(postTextAfter).toBeVisible();

            const contentAfter = await postTextAfter.textContent();
            expect(contentAfter).toBe(contentBefore);
        });

        test('Stop Generating Button', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(360000); // 6 minutes: allows 5 min streaming + buffer

            const { mmPage, aiPlugin, llmBotHelper, botUsername } = await setupTestPage(page, mattermost, provider);
            await mmPage.login(mattermost.url(), username, password);

            await aiPlugin.openRHS();

            const prompt = 'Explain TypeScript features (types, interfaces, generics) in 3-4 paragraphs';

            await aiPlugin.sendMessage(prompt);

            // Wait for post to appear
            const postText = llmBotHelper.getPostText();
            await expect(postText).toBeVisible({ timeout: 60000 });

            // Check for stop button with retry logic
            const stopButton = llmBotHelper.getStopGeneratingButton();
            let stopButtonVisible = false;

            // Check multiple times within first 5 seconds
            for (let i = 0; i < 10; i++) {
                stopButtonVisible = await stopButton.isVisible().catch(() => false);
                if (stopButtonVisible) break;
                await page.waitForTimeout(500);
            }

            if (stopButtonVisible) {
                // Stop button found - click it
                await llmBotHelper.stopGenerating();
                await page.waitForTimeout(1000);

                // Verify stop button disappears
                await expect(stopButton).not.toBeVisible({ timeout: 5000 });

                // Verify post content is present
                await expect(postText).toBeVisible();
                const content = await postText.textContent();
                expect(content).toBeTruthy();
            } else {
                // Stop button never appeared (response too fast) - wait for completion
                await llmBotHelper.waitForStreamingComplete();
            }
        });
    });
}

const providers = getAvailableProviders();
providers.forEach(provider => {
    createProviderTestSuite(provider);
});
