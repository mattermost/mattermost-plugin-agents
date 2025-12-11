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
 * Test Suite: Combined Features
 *
 * Tests multiple features working together in LLMBot posts using REAL APIs.
 * Runs once per configured provider (OpenAI and/or Anthropic).
 *
 * Environment Variables Required:
 * - ANTHROPIC_API_KEY: To run tests with Anthropic (claude-3-7-sonnet)
 * - OPENAI_API_KEY: To run tests with OpenAI (gpt-5)
 *
 * Tests:
 * 1. Reasoning and Citations Together
 * 2. Regenerate Functionality
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
    test.describe(`Combined Features - ${provider.name}`, () => {
        let mattermost: MattermostContainer;

        test.beforeAll(async () => {
            if (!config.shouldRunTests) return;

            // Customize configuration based on provider
            const isAnthropic = provider.service.type === 'anthropic';
            const customProvider = {
                ...provider,
                service: {
                    ...provider.service,
                    tokenLimit: 8192,
                    outputTokenLimit: 8192,
                    ...(provider.service.type === 'openaicompatible' && {
                        useResponsesAPI: true,
                    })
                },
                bot: {
                    ...provider.bot,
                    ...(isAnthropic && {
                        thinkingBudget: 1024,
                    }),
                    ...(provider.service.type === 'openaicompatible' && {
                        reasoningEffort: 'low',
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

        test('Reasoning and Citations Together', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(150000);

            const { mmPage, aiPlugin, llmBotHelper, botUsername } = await setupTestPage(page, mattermost, provider);
            await mmPage.login(mattermost.url(), username, password);

            // Navigate to DM with bot (required for web_search native tool)
            await mmPage.createAndNavigateToDMWithBot(mattermost, username, password, botUsername);

            await aiPlugin.openRHS();

            const isAnthropic = provider.service.type === 'anthropic';
            const prompt = isAnthropic
                ? 'Search the web for TypeScript docs and briefly analyze 2-3 key features with citations (1 paragraph)'
                : 'Use web search to find TypeScript docs and briefly list 2-3 benefits with citations (1 paragraph)';

            await aiPlugin.sendMessage(prompt);

            await llmBotHelper.waitForReasoning(undefined, 35000);
            // Wait for streaming to complete (smart wait, 5min safety timeout)
            await llmBotHelper.waitForStreamingComplete();

            await llmBotHelper.expectReasoningVisible(true);
            await expect(page.getByText('Thinking')).toBeVisible();

            const citations = llmBotHelper.getAllCitationIcons();
            const citationCount = await citations.count();

            // Web search in DM context MUST produce citations
            expect(citationCount).toBeGreaterThan(0);
            await expect(citations.first()).toBeVisible();

            await llmBotHelper.clickReasoningToggle();
            await llmBotHelper.expectReasoningExpanded(true);

            // Wait for scroll animation to complete after expanding reasoning
            await page.waitForTimeout(1000);

            // Scroll citation back into view and hover
            const citationWrapper = llmBotHelper.getCitationWrapper(1);
            await citationWrapper.scrollIntoViewIfNeeded();
            await page.waitForTimeout(500);

            await llmBotHelper.hoverCitation(1);
            await page.waitForTimeout(1500);
            const tooltip = llmBotHelper.getCitationTooltip();
            await expect(tooltip).toBeVisible({ timeout: 5000 });
        });

        test('Regenerate Functionality with Reasoning and Citations', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(720000); // 12 minutes: allows 5 min per operation + buffer

            const { mmPage, aiPlugin, llmBotHelper, botUsername } = await setupTestPage(page, mattermost, provider);
            await mmPage.login(mattermost.url(), username, password);

            // Navigate to DM with bot (required for web_search native tool)
            await mmPage.createAndNavigateToDMWithBot(mattermost, username, password, botUsername);

            await aiPlugin.openRHS();

            const isAnthropic = provider.service.type === 'anthropic';
            const prompt = isAnthropic
                ? 'Search the web for TypeScript benefits and briefly analyze 2-3 key points with citations (1 paragraph)'
                : 'Use web search to find TypeScript benefits and briefly explain 2-3 points with citations (1 paragraph)';

            await aiPlugin.sendMessage(prompt);

            // Wait for reasoning and streaming to complete on first response (up to 5 min each)
            await llmBotHelper.waitForReasoning(); // Uses default 300s timeout
            await llmBotHelper.waitForStreamingComplete(); // Uses default 300s timeout

            // Verify first response has reasoning
            await llmBotHelper.expectReasoningVisible(true);
            await expect(page.getByText('Thinking')).toBeVisible();

            // Verify first response has content
            const postTextBefore = llmBotHelper.getPostText();
            await expect(postTextBefore).toBeVisible();
            const contentBefore = await postTextBefore.textContent();
            expect(contentBefore).toBeTruthy();

            // Check for citations in first response
            const citationsBefore = llmBotHelper.getAllCitationIcons();
            const citationCountBefore = await citationsBefore.count();
            expect(citationCountBefore).toBeGreaterThan(0);

            // Hover over post to show regenerate button
            const llmBotPost = llmBotHelper.getLLMBotPost();
            await llmBotPost.hover();
            await page.waitForTimeout(500);

            const regenerateButton = llmBotHelper.getRegenerateButton();
            const isVisible = await regenerateButton.isVisible().catch(() => false);

            if (isVisible) {
                await llmBotHelper.regenerateResponse();

                // Wait for reasoning and streaming to complete on regenerated response (up to 5 min each)
                await llmBotHelper.waitForReasoning(); // Uses default 300s timeout
                await llmBotHelper.waitForStreamingComplete(); // Uses default 300s timeout

                // Verify regenerated response ALSO has reasoning
                await llmBotHelper.expectReasoningVisible(true);
                await expect(page.getByText('Thinking')).toBeVisible();

                // Verify regenerated response has content
                const postTextAfter = llmBotHelper.getPostText();
                await expect(postTextAfter).toBeVisible();
                const contentAfter = await postTextAfter.textContent();
                expect(contentAfter).toBeTruthy();

                // Verify regenerated response ALSO has citations
                const citationsAfter = llmBotHelper.getAllCitationIcons();
                const citationCountAfter = await citationsAfter.count();
                expect(citationCountAfter).toBeGreaterThan(0);
            } else {
                console.log('Regenerate button not visible, skipping regeneration test');
            }
        });
    });
}

const providers = getAvailableProviders();
providers.forEach(provider => {
    createProviderTestSuite(provider);
});
