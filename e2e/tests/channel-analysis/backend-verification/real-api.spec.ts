// spec: tests/channel-analysis/integration.plan.md
// seed: tests/seed.spec.ts

import { test, expect, Page } from '@playwright/test';
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
 * Test Suite: Channel Analysis Real API Verification
 *
 * A streamlined test suite that verifies the backend pipeline works correctly with real LLMs.
 * These tests ensure that context fetching, prompt construction, and LLM communication
 * are functioning as expected without testing exhaustive UI edge cases.
 *
 * Environment Variables Required:
 * - ANTHROPIC_API_KEY: To run tests with Anthropic (claude-3-7-sonnet)
 * - OPENAI_API_KEY: To run tests with OpenAI (gpt-5)
 */

const username = 'regularuser';
const password = 'regularuser';

const config = getAPIConfig();
const skipMessage = getSkipMessage();

/**
 * Helper class for Integration test interactions
 */
class RealAPIHelper {
    constructor(private page: Page) {}

    /**
     * Wait for the page to be fully loaded after login
     */
    async waitForPageReady() {
        await this.page.waitForSelector('[class*="channel-header"], #channelHeaderInfo', { timeout: 30000 });
        // Wait for plugin to initialize
        await this.page.waitForTimeout(2000);
    }

    /**
     * Navigate to a specific channel
     */
    async navigateToChannel(mattermost: MattermostContainer, channelName: string) {
        await this.page.goto(mattermost.url() + `/test/channels/${channelName}`);
        await this.waitForPageReady();
    }

    /**
     * Open the channel agents popover by clicking the agents button in channel header
     */
    async openChannelAgentsPopover() {
        // The agents button is in the channel header with a styled-components class
        // It's the button with ButtonContainer class that has an SVG icon
        const channelHeader = this.page.locator('.channel-header__top, [class*="channel-header"]');

        // Look for the AI plugin button
        const agentsButton = channelHeader.locator('button[class*="ButtonContainer"]').first();

        // Wait for the button to be visible and click it
        await agentsButton.waitFor({ state: 'visible', timeout: 10000 });
        await agentsButton.click();

        // Wait for the popover to appear
        await this.page.waitForSelector('.channel-summarize-popover', { timeout: 10000 });
    }

    /**
     * Type and submit a custom query in the channel agents input
     */
    async submitChannelQuery(query: string) {
        const input = this.page.locator('.channel-summarize-popover input[type="text"]');
        await expect(input).toBeVisible();
        await input.fill(query);
        await input.press('Enter');
    }
}

async function setupTestPage(page: Page, mattermost: MattermostContainer, provider: ProviderBundle) {
    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);
    const llmBotHelper = new LLMBotPostHelper(page);
    const apiHelper = new RealAPIHelper(page);
    const botUsername = provider.bot.name;

    return { mmPage, aiPlugin, llmBotHelper, apiHelper, botUsername };
}

function createProviderTestSuite(provider: ProviderBundle) {
    test.describe(`Channel Analysis Real API - ${provider.name}`, () => {
        let mattermost: MattermostContainer;

        test.beforeAll(async () => {
            if (!config.shouldRunTests) return;

            const customProvider = {
                ...provider,
                bot: {
                    ...provider.bot,
                    reasoningEnabled: true,
                    enabledNativeTools: [],
                }
            };

            mattermost = await RunRealAPIContainer(customProvider);
        });

        test.afterAll(async () => {
            if (mattermost) {
                await mattermost.stop();
            }
        });

        test('Sanity check: Channel analysis produces valid summary', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(360000);

            const { mmPage, llmBotHelper, apiHelper } = await setupTestPage(page, mattermost, provider);

            await mmPage.login(mattermost.url(), username, password);
            await apiHelper.waitForPageReady();

            await mmPage.sendChannelMessage('Feature discussion: We need to implement SSO.');
            await mmPage.sendChannelMessage('Deadline: Next Friday.');

            await apiHelper.openChannelAgentsPopover();
            await apiHelper.submitChannelQuery('What feature and deadline were discussed?');

            await llmBotHelper.waitForStreamingComplete();

            const postText = llmBotHelper.getPostText();
            await expect(postText).toBeVisible();
            const content = await postText.textContent();
            expect(content).toBeTruthy();
            expect(content!.toLowerCase()).toMatch(/sso|feature/);
            expect(content!.toLowerCase()).toMatch(/friday|deadline/);
        });

        test('Context isolation: Analysis reflects correct channel after switching', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(480000);

            const { mmPage, llmBotHelper, apiHelper } = await setupTestPage(page, mattermost, provider);

            await mmPage.login(mattermost.url(), username, password);
            await apiHelper.waitForPageReady();

            // Channel 1: Town Square
            await mmPage.sendChannelMessage('Town square topic: Company picnic.');

            // Channel 2: Off-Topic
            await apiHelper.navigateToChannel(mattermost, 'off-topic');
            await mmPage.sendChannelMessage('Off-topic discussion: Best sci-fi movies.');

            // Analyze Channel 2
            await apiHelper.openChannelAgentsPopover();
            await apiHelper.submitChannelQuery('What is the discussion topic?');

            await llmBotHelper.waitForStreamingComplete();

            const postText = llmBotHelper.getPostText();
            const content = await postText.textContent();
            expect(content).toBeTruthy();

            // Should mention sci-fi/movies (Channel 2), NOT picnic (Channel 1)
            expect(content!.toLowerCase()).toMatch(/sci-fi|movie/);
            expect(content!.toLowerCase()).not.toContain('picnic');
        });
    });
}

const providers = getAvailableProviders();
providers.forEach(provider => {
    createProviderTestSuite(provider);
});

