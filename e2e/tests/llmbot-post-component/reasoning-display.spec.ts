import { test, expect } from '@playwright/test';
import RunRealAPIContainer from 'helpers/real-api-container';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { LLMBotPostHelper } from 'helpers/llmbot-post';
import {
    getAPIConfig,
    getSkipMessage,
    logAPIConfig,
    getAvailableProviders,
    ProviderBundle,
} from 'helpers/api-config';

/**
 * Test Suite: Reasoning Display
 *
 * Tests the reasoning display functionality in LLMBot posts using REAL APIs.
 * Runs once per configured provider (OpenAI and/or Anthropic).
 *
 * Environment Variables Required:
 * - ANTHROPIC_API_KEY: To run tests with Anthropic (claude-3-7-sonnet)
 * - OPENAI_API_KEY: To run tests with OpenAI (gpt-5)
 *
 * Tests:
 * 1. Reasoning Display - Renders from Real API
 * 2. Reasoning Toggle - Expand and Collapse
 * 3. Reasoning Persistence After Refresh
 * 4. Reasoning States - Complete State
 * 5. Multiple Posts with Reasoning
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
    test.describe(`Reasoning Display - ${provider.name}`, () => {
        let mattermost: MattermostContainer;

        test.beforeAll(async () => {
            if (!config.shouldRunTests) return;

            // Customize provider to optimize for reasoning tests
            const isAnthropic = provider.service.type === 'anthropic';
            const customProvider = {
                ...provider,
                bot: {
                    ...provider.bot,
                    enabledNativeTools: [], // Disable web search for pure reasoning tests
                    reasoningEnabled: true,
                    ...(isAnthropic && {
                        thinkingBudget: 4096, // Higher budget for robust reasoning
                    }),
                    ...(provider.service.type === 'openaicompatible' && {
                        reasoningEffort: 'medium', // Medium effort for reliable reasoning
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

        test('Reasoning Display - Renders from Real API', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(360000); // 6 minutes: allows 5 min reasoning + buffer

            const { mmPage, aiPlugin, llmBotHelper, botUsername } = await setupTestPage(page, mattermost, provider);
            await mmPage.login(mattermost.url(), username, password);

            await aiPlugin.openRHS();

            const isAnthropic = provider.service.type === 'anthropic';
            const prompt = isAnthropic
                ? 'What letter is missing from the following sequence: A, C, E, G, I, K, M, O, Q, S, U, W, Y, ?. Think HARD.'
                : 'Think step by step about the benefits of TypeScript compared to JavaScript. Consider multiple angles. Keep response brief (1-2 paragraphs).';

            await aiPlugin.sendMessage(prompt);

            // Wait for reasoning to complete (smart wait, up to 5 min)
            await llmBotHelper.waitForReasoning();

            // Wait for streaming to complete (smart wait, up to 5 min)
            await llmBotHelper.waitForStreamingComplete();

            await llmBotHelper.expectReasoningVisible(true);
            await expect(page.getByText('Thinking')).toBeVisible();
            await llmBotHelper.expectReasoningExpanded(false);
        });

        test('Reasoning Toggle - Expand and Collapse', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(360000); // 6 minutes: allows 5 min reasoning + buffer

            const { mmPage, aiPlugin, llmBotHelper, botUsername } = await setupTestPage(page, mattermost, provider);
            await mmPage.login(mattermost.url(), username, password);

            await aiPlugin.openRHS();

            const isAnthropic = provider.service.type === 'anthropic';
            const prompt = isAnthropic
                ? 'Compare TypeScript and JavaScript for code quality. What are the key differences and trade-offs? (2-3 sentences). Think HARD.'
                : 'Think carefully: compare TypeScript and JavaScript for code quality. What are the key differences? (2-3 sentences)';

            await aiPlugin.sendMessage(prompt);

            // Wait for reasoning to complete (smart wait, up to 5 min)
            await llmBotHelper.waitForReasoning();

            // Wait for streaming to complete (smart wait, up to 5 min)
            await llmBotHelper.waitForStreamingComplete();

            await llmBotHelper.expectReasoningVisible(true);
            await llmBotHelper.expectReasoningExpanded(false);

            await llmBotHelper.clickReasoningToggle();
            await llmBotHelper.expectReasoningExpanded(true);

            await llmBotHelper.clickReasoningToggle();
            await llmBotHelper.expectReasoningExpanded(false);
            await expect(page.getByText('Thinking')).toBeVisible();
        });

        test('Reasoning Persistence After Refresh', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(360000); // 6 minutes: allows 5 min reasoning + buffer

            const { mmPage, aiPlugin, llmBotHelper, botUsername } = await setupTestPage(page, mattermost, provider);
            await mmPage.login(mattermost.url(), username, password);

            await aiPlugin.openRHS();

            const prompt = 'Analyze: What are the main advantages of TypeScript over JavaScript? Consider developer experience, maintainability, and type safety. Think carefully about your response and anticipate all angles. (1 paragraph)';

            await aiPlugin.sendMessage(prompt);

            // Wait for reasoning to complete (smart wait, up to 5 min)
            await llmBotHelper.waitForReasoning();

            // Wait for streaming to complete (smart wait, up to 5 min)
            await llmBotHelper.waitForStreamingComplete();

            await llmBotHelper.expectReasoningVisible(true);
            await llmBotHelper.expectReasoningExpanded(false);

            await llmBotHelper.clickReasoningToggle();
            await llmBotHelper.expectReasoningExpanded(true);

            await page.reload();
            await aiPlugin.openRHS();
            await page.waitForTimeout(2000);

            // After refresh, RHS shows fresh conversation - must navigate to chat history
            await aiPlugin.openChatHistory();
            await page.waitForTimeout(1000);
            await aiPlugin.clickChatHistoryItem(0); // Select most recent conversation
            await page.waitForTimeout(2000);

            // Verify reasoning persists in loaded conversation
            await llmBotHelper.expectReasoningVisible(true);
            await llmBotHelper.expectReasoningExpanded(false);

            await llmBotHelper.clickReasoningToggle();
            await llmBotHelper.expectReasoningExpanded(true);
        });

        test('Reasoning States - Complete State', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(360000); // 6 minutes: allows 5 min reasoning + buffer

            const { mmPage, aiPlugin, llmBotHelper, botUsername } = await setupTestPage(page, mattermost, provider);
            await mmPage.login(mattermost.url(), username, password);

            await aiPlugin.openRHS();

            await aiPlugin.sendMessage('Evaluate the benefits of TypeScript from multiple perspectives: developer productivity, code maintainability, and team collaboration. (3-4 sentences)');

            // Wait for reasoning to complete (smart wait, up to 5 min)
            await llmBotHelper.waitForReasoning();

            // Wait for streaming to complete (smart wait, up to 5 min)
            await llmBotHelper.waitForStreamingComplete();

            await expect(page.getByText('Thinking')).toBeVisible();

            await llmBotHelper.clickReasoningToggle();
            await llmBotHelper.expectReasoningExpanded(true);

            await llmBotHelper.clickReasoningToggle();
            await llmBotHelper.expectReasoningExpanded(false);
            await llmBotHelper.expectReasoningVisible(true);
        });

        test('Multiple Posts with Reasoning', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(720000); // 12 minutes: allows 5 min per message + buffer

            const { mmPage, aiPlugin, llmBotHelper, botUsername } = await setupTestPage(page, mattermost, provider);
            await mmPage.login(mattermost.url(), username, password);

            await aiPlugin.openRHS();

            // First message with reasoning
            await aiPlugin.sendMessage('Compare and analyze: TypeScript vs JavaScript for large projects. What are the trade-offs?');

            // Wait for first message to complete (smart wait, up to 5 min each)
            await llmBotHelper.waitForReasoning();
            await llmBotHelper.waitForStreamingComplete();

            // Verify first reasoning is visible
            await llmBotHelper.expectReasoningVisible(true);
            await expect(page.getByText('Thinking')).toBeVisible();

            // Second message with reasoning
            await aiPlugin.sendMessage('Evaluate JavaScript limitations when it comes to type safety and refactoring. How do these impact development?');

            // Smart poll for second reasoning display to appear
            const allReasoningDisplays = page.locator('div:has-text("Thinking")');
            const startTime = Date.now();
            const maxTimeout = 300000; // 5 minutes

            while (Date.now() - startTime < maxTimeout) {
                const count = await allReasoningDisplays.count();
                if (count >= 2) {
                    // Second reasoning appeared, wait for its streaming to complete
                    await page.waitForTimeout(1000);
                    break;
                }
                await page.waitForTimeout(500);
            }

            // Verify we have 2 reasoning displays
            const finalCount = await allReasoningDisplays.count();
            expect(finalCount).toBeGreaterThanOrEqual(2);

            // Verify both reasoning displays are interactive
            const firstReasoning = allReasoningDisplays.first();
            await firstReasoning.scrollIntoViewIfNeeded();
            await page.waitForTimeout(500);
            await firstReasoning.click();

            const secondReasoning = allReasoningDisplays.nth(1);
            await secondReasoning.scrollIntoViewIfNeeded();
            await page.waitForTimeout(500);
            await secondReasoning.click();

            // Verify both are still present after interactions
            const countAfterClicks = await allReasoningDisplays.count();
            expect(countAfterClicks).toBeGreaterThanOrEqual(2);
        });
    });
}

const providers = getAvailableProviders();
providers.forEach(provider => {
    createProviderTestSuite(provider);
});
