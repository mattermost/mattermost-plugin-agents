// spec: tests/channel-analysis/integration.plan.md
// seed: tests/seed.spec.ts

import { test, expect, Page } from '@playwright/test';
import RunContainer from 'helpers/plugincontainer';
import { RunOpenAIMocks, OpenAIMockContainer } from 'helpers/openai-mock';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { LLMBotPostHelper } from 'helpers/llmbot-post';

/**
 * Test Suite: Channel Analysis Integration Tests (Mocked)
 *
 * Tests integration of channel analysis with other Mattermost/AI plugin features using mocked backends.
 * These tests validate that channel analysis works harmoniously with:
 * - Regular RHS chat conversations
 * - Chat history navigation
 * - Large response rendering
 * - Concurrent operations
 * - RHS state management
 */

const username = 'regularuser';
const password = 'regularuser';

/**
 * Helper class for Integration test interactions
 */
class IntegrationHelper {
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
     * Generate multiple test messages
     */
    generateTestMessages(count: number): string[] {
        const topics = [
            'We discussed the new API implementation today.',
            'The team agreed on using TypeScript for the frontend.',
            'Database migrations need to be completed by Friday.',
            'Code review process will be updated next week.',
            'Performance testing showed 20% improvement.',
            'Security audit scheduled for next month.',
            'Documentation needs to be updated for v2.0.',
            'Bug fixes will be prioritized in the next sprint.',
            'New feature requests were reviewed and prioritized.',
            'Integration tests are passing on all environments.',
        ];
        const messages: string[] = [];
        for (let i = 0; i < count; i++) {
            messages.push(topics[i % topics.length] + ` (Message ${i + 1})`);
        }
        return messages;
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
     * Click a quick action button in the channel agents popover
     */
    async clickQuickAction(action: 'unreads' | 'last7days' | 'last14days' | 'daterange') {
        const actionMap = {
            'unreads': 'Summarize unreads',
            'last7days': 'Summarize last 7 days',
            'last14days': 'Summarize last 14 days',
            'daterange': 'Select date range to summarize',
        };

        const menuItem = this.page.getByText(actionMap[action]);
        await expect(menuItem).toBeVisible();
        await menuItem.click();
    }

    /**
     * Type and submit a custom query in the channel agents input
     */
    async submitChannelQuery(query: string) {
        const input = this.page.locator('.channel-summarize-popover input[type="text"]');
        await expect(input).toBeVisible();
        await input.fill(query);

        // Click the send icon or press Enter
        await input.press('Enter');
    }

    /**
     * Verify the channel agents popover is visible
     */
    async expectChannelAgentsPopoverVisible() {
        const popover = this.page.locator('.channel-summarize-popover');
        await expect(popover).toBeVisible();

        // Verify key elements are present
        await expect(this.page.getByPlaceholder(/Ask Agents about this channel/)).toBeVisible();
        await expect(this.page.getByText('Summarize unreads')).toBeVisible();
        await expect(this.page.getByText('GENERATE WITH:')).toBeVisible();
    }
}

async function setupTestPage(page: Page) {
    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);
    const llmBotHelper = new LLMBotPostHelper(page);
    const integrationHelper = new IntegrationHelper(page);
    const botUsername = 'Mock Bot';

    return { mmPage, aiPlugin, llmBotHelper, integrationHelper, botUsername };
}

test.describe('Channel Analysis Integration (Mocked)', () => {
    let mattermost: MattermostContainer;
    let openAIMock: OpenAIMockContainer;

    test.beforeAll(async () => {
        test.setTimeout(120000);
        mattermost = await RunContainer();
        openAIMock = await RunOpenAIMocks(mattermost.network);
    });

    test.beforeEach(async () => {
        await openAIMock.resetMocks();
    });

    test.afterAll(async () => {
        if (openAIMock) {
            await openAIMock.stop();
        }
        if (mattermost) {
            await mattermost.stop();
        }
    });

    test('Channel analysis after regular RHS chat', async ({ page }) => {
        const { mmPage, aiPlugin, llmBotHelper, integrationHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await integrationHelper.waitForPageReady();

        // 3. Post some test messages to the channel for context
        await mmPage.sendChannelMessage('We discussed the new feature implementation today.');
        await mmPage.sendChannelMessage('The team agreed on the deadline for next sprint.');

        // 4. Open RHS via app bar icon for regular chat first
        await aiPlugin.openRHS();

        // Mock regular chat response
        const mockResponse1 = `
data: {"id":"chatcmpl-401a","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-401a","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"I am a bot, nice to meet you."},"finish_reason":null}]}
data: {"id":"chatcmpl-401a","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse1);

        // 5. Have a regular conversation with the bot first
        await aiPlugin.sendMessage('Hello, how are you today?');

        // 6. Wait for first response (regular chat)
        await llmBotHelper.waitForStreamingComplete();

        // 7. Verify first response is visible
        const firstPostText = llmBotHelper.getPostText();
        await expect(firstPostText).toBeVisible();
        const firstContent = await firstPostText.textContent();
        expect(firstContent).toBeTruthy();
        expect(firstContent!.length).toBeGreaterThan(10);

        // Mock channel analysis response
        const mockResponse2 = `
data: {"id":"chatcmpl-401b","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-401b","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"The discussion was about the new feature implementation and the sprint deadline."},"finish_reason":null}]}
data: {"id":"chatcmpl-401b","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse2);

        // 8. Now use channel analysis feature via agents button
        await integrationHelper.openChannelAgentsPopover();

        // 9. Verify the popover opened
        await integrationHelper.expectChannelAgentsPopoverVisible();

        // 10. Submit a custom channel analysis query
        await integrationHelper.submitChannelQuery('What was discussed in the channel today?');

        // 11. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 12. Wait for the DOM to update with the new post
        await page.waitForTimeout(1000);

        // 13. Verify channel analysis response is visible and contains channel-specific content
        const secondPostText = llmBotHelper.getPostText();
        await expect(secondPostText).toBeVisible();
        const secondContent = await secondPostText.textContent();
        expect(secondContent).toBeTruthy();
        expect(secondContent!.length).toBeGreaterThan(20);
        // Response should mention the feature implementation or deadline
        expect(secondContent!.toLowerCase()).toMatch(/feature|deadline|sprint/);
    });

    test('Regular chat after channel analysis', async ({ page }) => {
        const { mmPage, aiPlugin, llmBotHelper, integrationHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await integrationHelper.waitForPageReady();

        // 3. Post some test messages to the channel for context
        await mmPage.sendChannelMessage('Project kickoff meeting scheduled for Monday.');
        await mmPage.sendChannelMessage('Budget approval received from management.');

        // 4. Use channel analysis feature via agents button
        await integrationHelper.openChannelAgentsPopover();

        // Mock channel analysis response
        const mockResponse1 = `
data: {"id":"chatcmpl-402a","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-402a","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"The channel discussed the project kickoff meeting and budget approval."},"finish_reason":null}]}
data: {"id":"chatcmpl-402a","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse1);

        // 5. Submit channel analysis query using quick action
        await integrationHelper.clickQuickAction('last7days');

        // 6. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 7. Verify channel analysis response contains channel-specific content
        const analysisPostText = llmBotHelper.getPostText();
        await expect(analysisPostText).toBeVisible();
        const analysisContent = await analysisPostText.textContent();
        expect(analysisContent).toBeTruthy();
        // Should mention the meeting or budget
        expect(analysisContent!.toLowerCase()).toMatch(/meeting|budget|kickoff|approval/);

        // Mock follow-up response
        const mockResponse2 = `
data: {"id":"chatcmpl-402b","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-402b","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"The first point was about the project kickoff meeting."},"finish_reason":null}]}
data: {"id":"chatcmpl-402b","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse2);

        // 8. Now open regular RHS and send a follow-up question
        await aiPlugin.openRHS();
        await aiPlugin.sendMessage('Can you tell me more about the first point?');

        // 9. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 10. Wait for the DOM to update with the new post
        await page.waitForTimeout(1000);

        // 11. Verify follow-up response is visible
        const followUpPostText = llmBotHelper.getPostText();
        await expect(followUpPostText).toBeVisible();
        const followUpContent = await followUpPostText.textContent();
        expect(followUpContent).toBeTruthy();
        expect(followUpContent!.length).toBeGreaterThan(10);
    });

    test('Chat history navigation with channel analysis', async ({ page }) => {
        const { mmPage, aiPlugin, llmBotHelper, integrationHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await integrationHelper.waitForPageReady();

        // 3. Post some test messages
        await mmPage.sendChannelMessage('First topic: API design patterns');
        await mmPage.sendChannelMessage('Second topic: Database optimization');

        // 4. Use channel analysis for first query
        await integrationHelper.openChannelAgentsPopover();

        // Mock response 1
        const mockResponse1 = `
data: {"id":"chatcmpl-403a","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-403a","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"The first topic is API design patterns."},"finish_reason":null}]}
data: {"id":"chatcmpl-403a","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse1);

        await integrationHelper.submitChannelQuery('What is the first topic mentioned?');

        // 5. Wait for first response
        await llmBotHelper.waitForStreamingComplete();

        // 6. Verify first response mentions API design
        const firstPostText = llmBotHelper.getPostText();
        await expect(firstPostText).toBeVisible();
        const firstContent = await firstPostText.textContent();
        expect(firstContent!.toLowerCase()).toContain('api');

        // 7. Use channel analysis for second query
        await integrationHelper.openChannelAgentsPopover();

        // Mock response 2
        const mockResponse2 = `
data: {"id":"chatcmpl-403b","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-403b","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"The second topic is Database optimization."},"finish_reason":null}]}
data: {"id":"chatcmpl-403b","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse2);

        await integrationHelper.submitChannelQuery('What is the second topic mentioned?');

        // 8. Wait for second response
        await llmBotHelper.waitForStreamingComplete();

        // 9. Wait for the DOM to update
        await page.waitForTimeout(1000);

        // 10. Verify second response mentions database
        const secondPostText = llmBotHelper.getPostText();
        await expect(secondPostText).toBeVisible();
        const secondContent = await secondPostText.textContent();
        expect(secondContent!.toLowerCase()).toContain('database');

        // 11. Open chat history to verify both queries are saved
        await aiPlugin.openRHS();
        await aiPlugin.openChatHistory();

        // 12. Verify chat history is visible
        await aiPlugin.expectChatHistoryVisible();

        // 13. Verify history contains items (previous conversations accessible)
        const threadsListContainer = aiPlugin.threadsListContainer;
        await expect(threadsListContainer).toBeVisible();
    });

    test('Large response rendering with comprehensive channel analysis', async ({ page }) => {
        const { mmPage, aiPlugin, llmBotHelper, integrationHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await integrationHelper.waitForPageReady();

        // 3. Create many test messages for comprehensive analysis
        const testMessages = integrationHelper.generateTestMessages(10);
        for (const message of testMessages) {
            await mmPage.sendChannelMessage(message);
        }

        // 4. Use channel analysis with a comprehensive query
        await integrationHelper.openChannelAgentsPopover();

        // Mock large response
        const largeContent = "This is a detailed analysis of the channel topics: 1. API implementation was discussed. 2. TypeScript usage was agreed upon for frontend. 3. Database migrations deadline is Friday. 4. Code review process updates. 5. Performance testing improvements. 6. Security audit scheduling. 7. Documentation updates for v2.0. 8. Bug fix prioritization. 9. New feature requests. 10. Integration tests status. All these topics were covered in detail.";
        const mockResponse = `
data: {"id":"chatcmpl-405","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-405","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"${largeContent}"},"finish_reason":null}]}
data: {"id":"chatcmpl-405","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse);

        await integrationHelper.submitChannelQuery('Provide a detailed analysis of all topics discussed in this channel, including a summary of each point and any action items.');

        // 5. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 6. Verify response is visible and has substantial content
        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible();
        const content = await postText.textContent();
        expect(content).toBeTruthy();
        // Expect a longer response for comprehensive analysis
        expect(content!.length).toBeGreaterThan(100);
        // Should mention some of the topics from test messages
        expect(content!.toLowerCase()).toMatch(/api|typescript|database|testing|documentation/);
    });

    test('Multiple quick actions in sequence', async ({ page }) => {
        const { mmPage, aiPlugin, llmBotHelper, integrationHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await integrationHelper.waitForPageReady();

        // 3. Post some test messages
        await mmPage.sendChannelMessage('Sprint planning session for Q2 roadmap');
        await mmPage.sendChannelMessage('Release candidate testing in progress');

        // 4. Use first quick action - summarize last 7 days
        await integrationHelper.openChannelAgentsPopover();

        // Mock response 1
        const mockResponse1 = `
data: {"id":"chatcmpl-406a","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-406a","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"Sprint planning for Q2 roadmap and release candidate testing."},"finish_reason":null}]}
data: {"id":"chatcmpl-406a","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse1);

        await integrationHelper.clickQuickAction('last7days');

        // 5. Wait for first response
        await llmBotHelper.waitForStreamingComplete();

        // 6. Verify first response
        const firstPostText = llmBotHelper.getPostText();
        await expect(firstPostText).toBeVisible();
        const firstContent = await firstPostText.textContent();
        expect(firstContent).toBeTruthy();
        expect(firstContent!.toLowerCase()).toMatch(/sprint|planning|testing|release/);

        // 7. Wait a moment before second action
        await page.waitForTimeout(2000);

        // 8. Use second quick action - custom query
        await integrationHelper.openChannelAgentsPopover();

        // Mock response 2
        const mockResponse2 = `
data: {"id":"chatcmpl-406b","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-406b","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"Priorities: Q2 roadmap and testing."},"finish_reason":null}]}
data: {"id":"chatcmpl-406b","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse2);

        await integrationHelper.submitChannelQuery('What are the main priorities mentioned?');

        // 9. Wait for second response
        await llmBotHelper.waitForStreamingComplete();

        // 10. Wait for the DOM to update
        await page.waitForTimeout(1000);

        // 11. Verify second response is visible
        const secondPostText = llmBotHelper.getPostText();
        await expect(secondPostText).toBeVisible();
        const secondContent = await secondPostText.textContent();
        expect(secondContent).toBeTruthy();
        expect(secondContent!.toLowerCase()).toMatch(/roadmap|testing|q2/);
    });

    test('RHS state management with channel analysis integration', async ({ page }) => {
        const { mmPage, aiPlugin, llmBotHelper, integrationHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await integrationHelper.waitForPageReady();

        // 3. Post some test messages
        await mmPage.sendChannelMessage('Infrastructure update: New servers deployed');
        await mmPage.sendChannelMessage('Monitoring dashboard is now live');

        // 4. Use channel analysis first
        await integrationHelper.openChannelAgentsPopover();

        // Mock response 1
        const mockResponse1 = `
data: {"id":"chatcmpl-407a","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-407a","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"Infrastructure updates: New servers deployed."},"finish_reason":null}]}
data: {"id":"chatcmpl-407a","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse1);

        await integrationHelper.submitChannelQuery('What infrastructure updates were made?');

        // 5. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 6. Verify RHS is now open with channel analysis response
        const rhsContainer = page.getByTestId('mattermost-ai-rhs');
        await expect(rhsContainer).toBeVisible();

        // 7. Verify initial response mentions infrastructure
        const firstPostText = llmBotHelper.getPostText();
        await expect(firstPostText).toBeVisible();
        const firstContent = await firstPostText.textContent();
        expect(firstContent).toBeTruthy();
        expect(firstContent!.toLowerCase()).toMatch(/infrastructure|server|deploy/);

        // 8. Use channel analysis again while RHS is already open
        await integrationHelper.openChannelAgentsPopover();

        // Mock response 2
        const mockResponse2 = `
data: {"id":"chatcmpl-407b","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-407b","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"Monitoring: Dashboard is live."},"finish_reason":null}]}
data: {"id":"chatcmpl-407b","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse2);

        await integrationHelper.submitChannelQuery('Is there any monitoring information?');

        // 9. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 10. Wait for the DOM to update
        await page.waitForTimeout(1000);

        // 11. Verify new response appears correctly and mentions monitoring
        const secondPostText = llmBotHelper.getPostText();
        await expect(secondPostText).toBeVisible();
        const secondContent = await secondPostText.textContent();
        expect(secondContent).toBeTruthy();
        expect(secondContent!.toLowerCase()).toMatch(/monitoring|dashboard/);

        // 12. Verify RHS remains open and functional
        await expect(rhsContainer).toBeVisible();
        await expect(aiPlugin.rhsPostTextarea).toBeVisible();
        await expect(aiPlugin.rhsSendButton).toBeVisible();
    });
});
