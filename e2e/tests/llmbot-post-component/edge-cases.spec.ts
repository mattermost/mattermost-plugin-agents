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
 * Test Suite: Edge Cases
 *
 * Tests edge case scenarios and error handling in LLMBot posts using REAL APIs.
 * Runs once per configured provider (OpenAI and/or Anthropic).
 *
 * Environment Variables Required:
 * - ANTHROPIC_API_KEY: To run tests with Anthropic (claude-3-7-sonnet)
 * - OPENAI_API_KEY: To run tests with OpenAI (gpt-5)
 *
 * Tests:
 * 1. Empty Reasoning Response
 * 2. Very Long Reasoning Content
 * 3. Rapid Reasoning Toggle
 * 4. Special Characters in Response
 * 5. Concurrent Posts
 * 6. Empty Post Content
 * 7. Unicode Content
 * 8. Large Post with Reasoning
 * 9. Network Error Handling
 * 10. Multiple Rapid Messages
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
    test.describe(`Edge Cases - ${provider.name}`, () => {
        let mattermost: MattermostContainer;

        test.beforeAll(async () => {
            if (!config.shouldRunTests) return;

            // Customize provider for edge case tests
            const customProvider = {
                ...provider,
                bot: {
                    ...provider.bot,
                    ...(provider.service.type === 'openaicompatible' && {
                        reasoningEffort: 'low', // Low effort for edge case testing
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

        test('Empty Reasoning Response', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(360000); // 6 minutes: allows 5 min streaming + buffer

            const { mmPage, aiPlugin, llmBotHelper, botUsername } = await setupTestPage(page, mattermost, provider);
            await mmPage.login(mattermost.url(), username, password);

            await aiPlugin.openRHS();

            const prompt = 'What is 2+2?';

            await aiPlugin.sendMessage(prompt);

            // Wait for streaming to complete (smart wait, up to 5 min)
            await llmBotHelper.waitForStreamingComplete();

            const postText = llmBotHelper.getPostText();
            await expect(postText).toContainText('4');

            const reasoning = llmBotHelper.getReasoningDisplay();
            const reasoningVisible = await reasoning.isVisible().catch(() => false);

            if (!reasoningVisible) {
                console.log('No reasoning for simple response (expected)');
            }
        });

        test('Very Long Reasoning Content', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(360000); // 6 minutes: allows 5 min streaming + buffer

            const { mmPage, aiPlugin, llmBotHelper, botUsername } = await setupTestPage(page, mattermost, provider);
            await mmPage.login(mattermost.url(), username, password);

            await aiPlugin.openRHS();

            const isAnthropic = provider.service.type === 'anthropic';
            const prompt = isAnthropic
                ? 'Analyze in extreme detail all aspects of TypeScript: history, features, type system, interfaces, generics, decorators, compilation, tooling, ecosystem, and future. Think through each aspect carefully'
                : 'Think very carefully and thoroughly about all aspects of TypeScript including its history, features, type system, interfaces, generics, decorators, compilation process, tooling ecosystem, and future direction. Consider each aspect in great detail';

            await aiPlugin.sendMessage(prompt);

            // Wait for reasoning to complete (smart wait, up to 5 min)
            await llmBotHelper.waitForReasoning();

            // Wait for streaming to complete (smart wait, up to 5 min)
            await llmBotHelper.waitForStreamingComplete();

            await llmBotHelper.expectReasoningVisible(true);
            await llmBotHelper.clickReasoningToggle();
            await llmBotHelper.expectReasoningExpanded(true);

            const reasoningContent = llmBotHelper.getReasoningContent();
            const content = await reasoningContent.textContent();

            expect(content).toBeTruthy();
            expect(content.length).toBeGreaterThan(100);

            await llmBotHelper.clickReasoningToggle();
            await llmBotHelper.expectReasoningExpanded(false);
        });

        test('Rapid Reasoning Toggle', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(360000); // 6 minutes: allows 5 min streaming + buffer

            const { mmPage, aiPlugin, llmBotHelper, botUsername } = await setupTestPage(page, mattermost, provider);
            await mmPage.login(mattermost.url(), username, password);

            await aiPlugin.openRHS();

            const prompt = 'Analyze TypeScript benefits carefully';

            await aiPlugin.sendMessage(prompt);

            // Wait for reasoning to complete (smart wait, up to 5 min)
            await llmBotHelper.waitForReasoning();

            // Wait for streaming to complete (smart wait, up to 5 min)
            await llmBotHelper.waitForStreamingComplete();

            await llmBotHelper.expectReasoningVisible(true);

            for (let i = 0; i < 5; i++) {
                await llmBotHelper.clickReasoningToggle();
                await page.waitForTimeout(100);
                await llmBotHelper.clickReasoningToggle();
                await page.waitForTimeout(100);
            }

            await llmBotHelper.expectReasoningVisible(true);
        });

        test('Special Characters in Response', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(360000); // 6 minutes: allows 5 min streaming + buffer

            const { mmPage, aiPlugin, llmBotHelper, botUsername } = await setupTestPage(page, mattermost, provider);
            await mmPage.login(mattermost.url(), username, password);

            await aiPlugin.openRHS();

            const prompt = 'Briefly explain TypeScript with 1-2 examples using <>, &, | characters (3-4 sentences)';

            await aiPlugin.sendMessage(prompt);

            // Wait for streaming to complete (smart wait, up to 5 min)
            await llmBotHelper.waitForStreamingComplete();

            const postText = llmBotHelper.getPostText();
            const content = await postText.textContent();
            expect(content).toBeTruthy();
            expect(content.length).toBeGreaterThan(50);
        });

        test('Concurrent Posts', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(900000); // 15 minutes: allows 5 min per message + buffer

            const { mmPage, aiPlugin, llmBotHelper, botUsername } = await setupTestPage(page, mattermost, provider);
            await mmPage.login(mattermost.url(), username, password);

            await aiPlugin.openRHS();

            await aiPlugin.sendMessage('Briefly explain TypeScript (2 sentences)');
            await page.waitForTimeout(2000);

            await aiPlugin.sendMessage('Briefly explain JavaScript (2 sentences)');
            await page.waitForTimeout(2000);

            await aiPlugin.sendMessage('Briefly compare both (2 sentences)');

            // Smart poll for all three posts to appear
            const allPosts = page.locator('[data-testid="posttext"]');
            const startTime = Date.now();
            const maxTimeout = 600000; // 10 minutes for all three posts

            while (Date.now() - startTime < maxTimeout) {
                const count = await allPosts.count();
                if (count >= 3) {
                    // All three posts appeared
                    await page.waitForTimeout(1000);
                    break;
                }
                await page.waitForTimeout(500);
            }

            // Verify we have at least 3 posts
            const finalCount = await allPosts.count();
            expect(finalCount).toBeGreaterThanOrEqual(3);

            await expect(allPosts.first()).toBeVisible();
            await expect(allPosts.nth(1)).toBeVisible();
            await expect(allPosts.nth(2)).toBeVisible();
        });

        test('Empty Post Content', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(360000); // 6 minutes: allows 5 min streaming + buffer

            const { mmPage, aiPlugin, llmBotHelper, botUsername } = await setupTestPage(page, mattermost, provider);
            await mmPage.login(mattermost.url(), username, password);

            await aiPlugin.openRHS();

            const prompt = 'What is the answer to everything?';

            await aiPlugin.sendMessage(prompt);

            // Wait for streaming to complete (smart wait, up to 5 min)
            await llmBotHelper.waitForStreamingComplete();

            const postText = llmBotHelper.getPostText();
            const content = await postText.textContent();
            expect(content).toBeTruthy();
            expect(content.length).toBeGreaterThan(0);
        });

        test('Unicode Content', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(360000); // 6 minutes: allows 5 min streaming + buffer

            const { mmPage, aiPlugin, llmBotHelper, botUsername } = await setupTestPage(page, mattermost, provider);
            await mmPage.login(mattermost.url(), username, password);

            await aiPlugin.openRHS();

            const prompt = 'Briefly explain TypeScript with emoji examples: 🚀 💡 ⚡ (2-3 sentences)';

            await aiPlugin.sendMessage(prompt);

            // Wait for streaming to complete (smart wait, up to 5 min)
            await llmBotHelper.waitForStreamingComplete();

            const postText = llmBotHelper.getPostText();
            const content = await postText.textContent();
            expect(content).toBeTruthy();
        });

        test('Large Post with Reasoning', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(360000); // 6 minutes: allows 5 min streaming + buffer

            const { mmPage, aiPlugin, llmBotHelper, botUsername } = await setupTestPage(page, mattermost, provider);
            await mmPage.login(mattermost.url(), username, password);

            await aiPlugin.openRHS();

            const isAnthropic = provider.service.type === 'anthropic';
            const prompt = isAnthropic
                ? 'Write a comprehensive guide to TypeScript covering all major features in detail. Think through the structure carefully'
                : 'Think carefully about how to structure a comprehensive guide to TypeScript. Write detailed explanations of all major features';

            await aiPlugin.sendMessage(prompt);

            // Wait for reasoning to complete (smart wait, up to 5 min)
            await llmBotHelper.waitForReasoning();

            // Wait for streaming to complete (smart wait, up to 5 min)
            await llmBotHelper.waitForStreamingComplete();

            const postText = llmBotHelper.getPostText();
            const content = await postText.textContent();
            expect(content).toBeTruthy();
            expect(content.length).toBeGreaterThan(200);

            await llmBotHelper.expectReasoningVisible(true);
            await llmBotHelper.clickReasoningToggle();
            await llmBotHelper.expectReasoningExpanded(true);
        });

        test('Network Error Handling', async ({ page, context }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(360000); // 6 minutes: allows 5 min streaming + buffer

            const { mmPage, aiPlugin, llmBotHelper, botUsername } = await setupTestPage(page, mattermost, provider);
            await mmPage.login(mattermost.url(), username, password);

            await aiPlugin.openRHS();

            const prompt = 'Briefly explain TypeScript in 2 sentences';

            await context.setOffline(true);
            await aiPlugin.sendMessage(prompt);
            await page.waitForTimeout(3000);

            await context.setOffline(false);
            await page.waitForTimeout(3000);

            await aiPlugin.sendMessage('What is TypeScript?');

            // Wait for streaming to complete (smart wait, up to 5 min)
            await llmBotHelper.waitForStreamingComplete();

            const postText = llmBotHelper.getPostText();
            await expect(postText).toBeVisible();
        });

        test('Multiple Rapid Messages', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(900000); // 15 minutes: allows 5 min per message + buffer

            const { mmPage, aiPlugin, llmBotHelper, botUsername } = await setupTestPage(page, mattermost, provider);
            await mmPage.login(mattermost.url(), username, password);

            await aiPlugin.openRHS();

            const messages = [
                'What is TypeScript?',
                'What is JavaScript?',
                'What is Python?',
            ];

            for (const message of messages) {
                await aiPlugin.sendMessage(message);
                await page.waitForTimeout(1000);
            }

            // Smart poll for all three posts to appear
            const allPosts = page.locator('[data-testid="posttext"]');
            const startTime = Date.now();
            const maxTimeout = 600000; // 10 minutes for all three posts

            while (Date.now() - startTime < maxTimeout) {
                const count = await allPosts.count();
                if (count >= 3) {
                    // All three posts appeared
                    await page.waitForTimeout(1000);
                    break;
                }
                await page.waitForTimeout(500);
            }

            // Verify we have at least 3 posts
            const count = await allPosts.count();
            expect(count).toBeGreaterThanOrEqual(3);

            for (let i = 0; i < Math.min(count, 3); i++) {
                await expect(allPosts.nth(i)).toBeVisible();
            }
        });
    });
}

const providers = getAvailableProviders();
providers.forEach(provider => {
    createProviderTestSuite(provider);
});
