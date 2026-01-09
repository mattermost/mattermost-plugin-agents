// spec: tests/channel-analysis/custom-queries.plan.md
// seed: tests/seed.spec.ts

import { test, expect, Page } from '@playwright/test';
import RunContainer from 'helpers/plugincontainer';
import { RunOpenAIMocks, OpenAIMockContainer, responseTest } from 'helpers/openai-mock';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { LLMBotPostHelper } from 'helpers/llmbot-post';

/**
 * Test Suite: Channel Analysis Custom Queries
 *
 * Tests custom question submission and response functionality through the channel analysis interface.
 * These tests validate various input types including special characters, emoji, long questions,
 * and sequential queries using mocked LLM backends.
 */

const username = 'regularuser';
const password = 'regularuser';

/**
 * Helper class for Channel Analysis Custom Queries test interactions
 */
class CustomQueriesHelper {
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
     * Click the agents button in the channel header to open the channel analysis popover
     */
    async openChannelAnalysisPopover() {
        // The agents button is in the channel header with a styled-components class
        // It's the button with ButtonContainer class that has an SVG icon
        // We use a more specific selector to avoid matching other buttons
        const channelHeader = this.page.locator('.channel-header__top, [class*="channel-header"]');

        // Look for the AI plugin button - it has a specific styled-components class pattern
        // The button is after the channel files button and before the info button
        const agentsButton = channelHeader.locator('button[class*="ButtonContainer"]').first();

        // Wait for the button to be visible and click it
        await agentsButton.waitFor({ state: 'visible', timeout: 10000 });
        await agentsButton.click();

        // Wait for the popover to appear
        await this.page.waitForSelector('.channel-summarize-popover', { timeout: 10000 });
    }

    /**
     * Type a custom query in the channel analysis input and submit
     */
    async submitChannelQuery(query: string) {
        // Find the input with placeholder "Ask Agents about this channel..."
        const input = this.page.getByPlaceholder(/Ask Agents about this channel/);
        await input.waitFor({ state: 'visible', timeout: 10000 });
        await input.fill(query);

        // Press Enter or click the send icon to submit
        await input.press('Enter');
    }

    /**
     * Click a quick action button in the popover
     */
    async clickQuickAction(actionText: string) {
        const menuItem = this.page.locator('.channel-summarize-popover').getByText(actionText);
        await menuItem.click();
    }
}

async function setupTestPage(page: Page) {
    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);
    const llmBotHelper = new LLMBotPostHelper(page);
    const customQueriesHelper = new CustomQueriesHelper(page);
    const botUsername = 'Mock Bot'; // Default bot name from plugincontainer

    return { mmPage, aiPlugin, llmBotHelper, customQueriesHelper, botUsername };
}

test.describe('Channel Analysis Custom Queries', () => {
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

    test('Channel-specific custom question submission and response', async ({ page }) => {
        const { mmPage, llmBotHelper, customQueriesHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await customQueriesHelper.waitForPageReady();

        // 3. Post some test messages to the channel for context
        await mmPage.sendChannelMessage('We discussed the new feature implementation today.');
        await mmPage.sendChannelMessage('The team agreed on the deadline for next sprint.');
        await mmPage.sendChannelMessage('Bug fixes will be prioritized this week.');

        // 4. Open channel analysis popover via agents button in channel header
        await customQueriesHelper.openChannelAnalysisPopover();

        // Mock response
        const mockResponse = `
data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"Based on the discussion, the main topics are the new feature implementation and the deadline for the next sprint."},"finish_reason":null}]}
data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';

        await openAIMock.addCompletionMock(mockResponse);

        // 5. Submit a custom question about the channel content
        await customQueriesHelper.submitChannelQuery('What are the main topics discussed in this channel?');

        // 6. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 7. Verify response is visible and has meaningful content
        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible();
        const content = await postText.textContent();
        expect(content).toBeTruthy();
        expect(content!.length).toBeGreaterThan(20);
        expect(content).toContain('feature');
        expect(content).toContain('deadline');
    });

    test('Channel analysis question with special characters', async ({ page }) => {
        const { mmPage, llmBotHelper, customQueriesHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await customQueriesHelper.waitForPageReady();

        // 3. Post messages with special characters
        await mmPage.sendChannelMessage('@user mentioned feature-x implementation');
        await mmPage.sendChannelMessage('Tracking #bugs in the latest release');

        // 4. Open channel analysis popover via agents button
        await customQueriesHelper.openChannelAnalysisPopover();

        // Mock response
        const mockResponse = `
data: {"id":"chatcmpl-124","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-124","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"The user mentions include @user and the hashtags include #bugs."},"finish_reason":null}]}
data: {"id":"chatcmpl-124","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse);

        // 5. Send question with special characters about channel content
        await customQueriesHelper.submitChannelQuery('What user mentions and hashtags appear in this channel?');

        // 6. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 7. Verify response is visible and handles special characters correctly
        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible();
        const content = await postText.textContent();
        expect(content).toBeTruthy();
        expect(content!.length).toBeGreaterThan(10);
        expect(content).toContain('@user');
        expect(content).toContain('#bugs');
    });

    test('Long channel analysis question (200+ characters)', async ({ page }) => {
        const { mmPage, llmBotHelper, customQueriesHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await customQueriesHelper.waitForPageReady();

        // 3. Post diverse content to the channel
        await mmPage.sendChannelMessage('Action item: Complete API integration by Friday');
        await mmPage.sendChannelMessage('Blocker: Waiting for database schema approval');
        await mmPage.sendChannelMessage('Decision: We will use TypeScript for the frontend');

        // 4. Open channel analysis popover via agents button
        await customQueriesHelper.openChannelAnalysisPopover();

        // Mock response
        const mockResponse = `
data: {"id":"chatcmpl-125","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-125","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"The discussion covered API integration deadlines, database schema blockers, and the decision to use TypeScript."},"finish_reason":null}]}
data: {"id":"chatcmpl-125","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse);

        // 5. Send a long question (200+ characters)
        const longQuestion = 'Can you please provide a detailed analysis of the conversation topics discussed in this channel, including any action items that were mentioned, deadlines that were set, and any concerns or blockers that team members raised during the discussion? Also summarize any decisions that were made.';
        expect(longQuestion.length).toBeGreaterThan(200);
        await customQueriesHelper.submitChannelQuery(longQuestion);

        // 6. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 7. Verify bot provides response
        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible();
        const content = await postText.textContent();
        expect(content).toBeTruthy();
        expect(content!.length).toBeGreaterThan(20);
    });

    test('Multiple channel analysis questions in sequence', async ({ page }) => {
        const { mmPage, llmBotHelper, customQueriesHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await customQueriesHelper.waitForPageReady();

        // 3. Post messages to the channel
        await mmPage.sendChannelMessage('Q: What is our release timeline?');
        await mmPage.sendChannelMessage('A: We target end of month for beta release');
        await mmPage.sendChannelMessage('Q: Who is handling QA testing?');
        await mmPage.sendChannelMessage('A: Sarah from the QA team will coordinate');

        // 4. Open channel analysis popover via agents button
        await customQueriesHelper.openChannelAnalysisPopover();

        // Mock response 1
        const mockResponse1 = `
data: {"id":"chatcmpl-126a","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-126a","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"The main topics are the release timeline and QA testing coordination."},"finish_reason":null}]}
data: {"id":"chatcmpl-126a","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        // Add specific mock for the first question
        await openAIMock.addCompletionMockWithRequestBody(mockResponse1, "main topics");

        // 5. Send first question
        await customQueriesHelper.submitChannelQuery('What are the main topics in this channel?');

        // 6. Wait for first response
        await llmBotHelper.waitForStreamingComplete();

        // 7. Verify first response visible and capture its content
        const firstPostText = llmBotHelper.getPostText();
        await expect(firstPostText).toBeVisible();
        const firstContent = await firstPostText.textContent();
        expect(firstContent).toBeTruthy();
        expect(firstContent).toContain('release timeline');

        // 8. Re-open the popover for second question
        await page.waitForTimeout(2000);
        await customQueriesHelper.openChannelAnalysisPopover();

        // Mock response 2
        const mockResponse2 = `
data: {"id":"chatcmpl-126b","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-126b","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"The questions asked were about the release timeline and who is handling QA testing."},"finish_reason":null}]}
data: {"id":"chatcmpl-126b","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        // Add specific mock for the second question
        await openAIMock.addCompletionMockWithRequestBody(mockResponse2, "questions were asked");

        // 9. Send second question
        await customQueriesHelper.submitChannelQuery('What questions were asked in this channel?');

        // 10. Wait for the response content to change
        await expect(async () => {
            const currentContent = await llmBotHelper.getPostText().textContent();
            expect(currentContent).not.toBe(firstContent);
            expect(currentContent).toBeTruthy();
        }).toPass({ timeout: 60000 });

        // 11. Wait for second response streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 12. Wait a moment for the DOM to update
        await page.waitForTimeout(1000);

        // 13. Verify second response visible and has content
        const secondPostText = llmBotHelper.getPostText();
        await expect(secondPostText).toBeVisible({ timeout: 30000 });
        const secondContent = await secondPostText.textContent();
        expect(secondContent).toBeTruthy();
        expect(secondContent!.length).toBeGreaterThan(10);
        expect(secondContent).toContain('questions asked');
    });

    test('Emoji in channel analysis question', async ({ page }) => {
        const { mmPage, llmBotHelper, customQueriesHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await customQueriesHelper.waitForPageReady();

        // 3. Post positive updates to the channel
        await mmPage.sendChannelMessage('Great news! Our customer satisfaction score increased by 15%');
        await mmPage.sendChannelMessage('Congratulations to the team on the successful launch');

        // 4. Open channel analysis popover via agents button
        await customQueriesHelper.openChannelAnalysisPopover();

        // Mock response
        const mockResponse = `
data: {"id":"chatcmpl-127","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-127","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"Positive updates include increased customer satisfaction score and a successful launch! 😊"},"finish_reason":null}]}
data: {"id":"chatcmpl-127","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse);

        // 5. Send question with emoji about channel content
        await customQueriesHelper.submitChannelQuery('What positive updates were shared in this channel? 😊');

        // 6. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 7. Verify response is visible
        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible();
        const content = await postText.textContent();
        expect(content).toBeTruthy();
        expect(content!.length).toBeGreaterThan(10);
        expect(content).toContain('satisfaction');
    });

    test('Short single-word query about channel', async ({ page }) => {
        const { mmPage, llmBotHelper, customQueriesHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await customQueriesHelper.waitForPageReady();

        // 3. Post some messages for summarization
        await mmPage.sendChannelMessage('Project kickoff scheduled for Monday');
        await mmPage.sendChannelMessage('Budget approved by management');
        await mmPage.sendChannelMessage('Team ready to start implementation');

        // 4. Open channel analysis popover via agents button
        await customQueriesHelper.openChannelAnalysisPopover();

        // Mock response
        const mockResponse = `
data: {"id":"chatcmpl-128","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-128","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"Summary: Project kickoff is Monday, budget is approved, and team is ready to start."},"finish_reason":null}]}
data: {"id":"chatcmpl-128","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse);

        // 5. Send minimal query
        await customQueriesHelper.submitChannelQuery('Summary');

        // 6. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 7. Verify bot interprets and responds
        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible();
        const content = await postText.textContent();
        expect(content).toBeTruthy();
        expect(content!.length).toBeGreaterThan(10);
        expect(content).toContain('kickoff');
    });
});
