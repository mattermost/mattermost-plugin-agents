// spec: tests/channel-analysis/integration.plan.md
// seed: tests/seed.spec.ts

import { test, expect, Page } from '@playwright/test';
import RunRealAPIContainer from 'helpers/real-api-container';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { LLMBotPostHelper } from 'helpers/llmbot-post';
import {
    getAPIConfig,
    getSkipMessage,
    getAvailableProviders,
    ProviderBundle,
} from 'helpers/api-config';
import { attachAPIErrorContext } from 'helpers/log-scanner';

/**
 * Test Suite: Channel Analysis Real API Verification
 *
 * A streamlined test suite that verifies the backend pipeline works correctly with real LLMs.
 * These tests ensure that context fetching, prompt construction, and LLM communication
 * are functioning as expected without testing exhaustive UI edge cases.
 *
 * Environment Variables Required:
 * - ANTHROPIC_API_KEY: To run tests with Anthropic
 * - OPENAI_API_KEY: To run tests with OpenAI
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
     * Trigger channel analysis directly through the plugin API.
     * This keeps the test focused on backend behavior even when the header button is absent.
     */
    async analyzeChannel(
        mattermost: MattermostContainer,
        channelName: string,
        botUsername: string,
        query: string,
    ): Promise<{postid: string; channelid: string}> {
        const userClient = await mattermost.getClient(username, password);
        const team = (await userClient.getMyTeams())[0];
        const channel = await userClient.getChannelByName(team.id, channelName);

        return await (userClient as any).doFetch(
            `${mattermost.url()}/plugins/mattermost-ai/channel/${channel.id}/analyze?botUsername=${botUsername}`,
            {
                method: 'post',
                body: JSON.stringify({
                    analysis_type: 'summarize_channel',
                    prompt: query,
                    team_id: team.id,
                }),
            },
        );
    }
}

async function setupTestPage(page: Page, mattermost: MattermostContainer, provider: ProviderBundle) {
    const mmPage = new MattermostPage(page);
    const llmBotHelper = new LLMBotPostHelper(page);
    const apiHelper = new RealAPIHelper(page);
    const botUsername = provider.bot.name;

    return { mmPage, llmBotHelper, apiHelper, botUsername };
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

        test.afterEach(async ({}, testInfo) => {
            await attachAPIErrorContext(testInfo);
        });

        test('Sanity check: Channel analysis produces valid summary', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(360000);

            const { mmPage, llmBotHelper, apiHelper, botUsername } = await setupTestPage(page, mattermost, provider);

            await mmPage.login(mattermost.url(), username, password);
            await apiHelper.waitForPageReady();

            await mmPage.sendChannelMessage('Feature discussion: We need to implement SSO.');
            await mmPage.sendChannelMessage('Deadline: Next Friday.');

            const result = await apiHelper.analyzeChannel(
                mattermost,
                'town-square',
                botUsername,
                'What feature and deadline were discussed?',
            );

            await mmPage.createAndNavigateToDMWithBot(mattermost, username, password, botUsername);

            const postText = llmBotHelper.getPostText(result.postid);
            await expect(postText).toBeVisible({ timeout: 300000 });
            await expect.poll(async () => await postText.textContent(), { timeout: 300000 }).toMatch(/sso|feature/i);
            await expect.poll(async () => await postText.textContent(), { timeout: 300000 }).toMatch(/friday|deadline/i);
            const content = await postText.textContent();
            expect(content).toBeTruthy();
            expect(content!.toLowerCase()).toMatch(/sso|feature/);
            expect(content!.toLowerCase()).toMatch(/friday|deadline/);
        });

        test('Context isolation: Analysis reflects correct channel after switching', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(480000);

            const { mmPage, llmBotHelper, apiHelper, botUsername } = await setupTestPage(page, mattermost, provider);

            await mmPage.login(mattermost.url(), username, password);
            await apiHelper.waitForPageReady();

            // Channel 1: Town Square
            await mmPage.sendChannelMessage('Town square topic: Company picnic.');

            // Channel 2: Off-Topic
            await apiHelper.navigateToChannel(mattermost, 'off-topic');
            await mmPage.sendChannelMessage('Off-topic discussion: Best sci-fi movies.');

            const result = await apiHelper.analyzeChannel(
                mattermost,
                'off-topic',
                botUsername,
                'What is the discussion topic?',
            );

            await mmPage.createAndNavigateToDMWithBot(mattermost, username, password, botUsername);

            const postText = llmBotHelper.getPostText(result.postid);
            await expect(postText).toBeVisible({ timeout: 300000 });
            await expect.poll(async () => await postText.textContent(), { timeout: 300000 }).toMatch(/sci-fi|movie/i);
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

