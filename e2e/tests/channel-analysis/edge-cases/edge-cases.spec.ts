// spec: tests/channel-analysis/edge-cases.plan.md
// seed: tests/seed.spec.ts

import { test, expect, Page } from '@playwright/test';
import RunContainer from 'helpers/plugincontainer';
import { RunOpenAIMocks, OpenAIMockContainer } from 'helpers/openai-mock';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { LLMBotPostHelper } from 'helpers/llmbot-post';

/**
 * Test Suite: Channel Analysis Edge Cases
 *
 * Tests edge case scenarios and robustness for channel analysis functionality.
 * These tests validate the "Ask Agents about this channel" feature accessed via
 * the agents button in the channel header using mocked LLM backends.
 */

const username = 'regularuser';
const password = 'regularuser';

/**
 * Helper class for Edge Cases test interactions
 */
class EdgeCasesHelper {
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
     * Generate a long message with specified character count
     */
    generateLongMessage(charCount: number): string {
        const baseText = 'This is a detailed message about our project discussion. ';
        let result = '';
        while (result.length < charCount) {
            result += baseText;
        }
        return result.substring(0, charCount);
    }
}

async function setupTestPage(page: Page) {
    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);
    const llmBotHelper = new LLMBotPostHelper(page);
    const edgeCasesHelper = new EdgeCasesHelper(page);
    const botUsername = 'Mock Bot'; // Default bot name

    return { mmPage, aiPlugin, llmBotHelper, edgeCasesHelper, botUsername };
}

test.describe('Channel Analysis Edge Cases', () => {
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

    test('Channel with minimal content', async ({ page }) => {
        const { mmPage, aiPlugin, llmBotHelper, edgeCasesHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await edgeCasesHelper.waitForPageReady();

        // 3. Post a single minimal message to the channel
        await mmPage.sendChannelMessage('This is the only message in the channel.');

        // 4. Open channel analysis popover via agents button in channel header
        await aiPlugin.openChannelAnalysisPopover();

        // Mock response
        const mockResponse = `
data: {"id":"chatcmpl-101","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-101","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"This channel contains a single message noting it is the only message."},"finish_reason":null}]}
data: {"id":"chatcmpl-101","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse);

        // 5. Ask about the channel content using the channel-specific input
        await aiPlugin.sendChannelAnalysisMessage('Summarize the channel discussion');

        // 6. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 7. Verify response is visible and handles minimal content gracefully
        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible();
        const content = await postText.textContent();
        expect(content).toBeTruthy();
        expect(content!.length).toBeGreaterThan(10);
    });

    test('Unicode and international characters', async ({ page }) => {
        const { mmPage, aiPlugin, llmBotHelper, edgeCasesHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await edgeCasesHelper.waitForPageReady();

        // 3. Post messages with unicode and international characters
        await mmPage.sendChannelMessage('Test emoji message 😀🎉🚀💡⚡');
        await mmPage.sendChannelMessage('Chinese: 你好世界，这是一个测试');
        await mmPage.sendChannelMessage('Arabic: مرحبا بالعالم');
        await mmPage.sendChannelMessage('Japanese: こんにちは世界');
        await mmPage.sendChannelMessage('Special chars: ñ ü é ö ß');

        // 4. Open channel analysis popover via agents button in channel header
        await aiPlugin.openChannelAnalysisPopover();

        // Mock response
        const mockResponse = `
data: {"id":"chatcmpl-102","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-102","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"The channel contains emoji like 😀, Chinese (你好世界), Arabic (مرحبا), Japanese (こんにちは), and special characters like ñ and ü."},"finish_reason":null}]}
data: {"id":"chatcmpl-102","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse);

        // 5. Ask about the international content using the channel-specific input
        await aiPlugin.sendChannelAnalysisMessage('What languages and characters are present in the channel messages?');

        // 6. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 7. Verify response processes unicode correctly
        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible();
        const content = await postText.textContent();
        expect(content).toBeTruthy();
        expect(content!.length).toBeGreaterThan(20);
        expect(content).toContain('你好世界');
    });

    test('Long message in channel', async ({ page }) => {
        const { mmPage, aiPlugin, llmBotHelper, edgeCasesHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await edgeCasesHelper.waitForPageReady();

        // 3. Post a very long message (2000+ characters)
        const longMessage = edgeCasesHelper.generateLongMessage(2100);
        expect(longMessage.length).toBeGreaterThan(2000);
        await mmPage.sendChannelMessage(longMessage);

        // 4. Open channel analysis popover via agents button in channel header
        await aiPlugin.openChannelAnalysisPopover();

        // Mock response
        const mockResponse = `
data: {"id":"chatcmpl-103","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-103","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"The channel contains a very long detailed message about the project discussion."},"finish_reason":null}]}
data: {"id":"chatcmpl-103","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse);

        // 5. Ask to summarize the long content using the channel-specific input
        await aiPlugin.sendChannelAnalysisMessage('Summarize the long message in the channel');

        // 6. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 7. Verify bot handles long content and provides summarization
        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible();
        const content = await postText.textContent();
        expect(content).toBeTruthy();
        expect(content!.length).toBeGreaterThan(20);
    });

    test('Multiple questions sequentially', async ({ page }) => {
        const { mmPage, aiPlugin, llmBotHelper, edgeCasesHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await edgeCasesHelper.waitForPageReady();

        // 3. Post some context messages
        await mmPage.sendChannelMessage('Project status: Development phase');
        await mmPage.sendChannelMessage('Timeline: Q1 deadline');
        await mmPage.sendChannelMessage('Team size: 5 developers');

        // 4. Open channel analysis popover and send first question
        await aiPlugin.openChannelAnalysisPopover();

        // Mock response 1
        const mockResponse1 = `
data: {"id":"chatcmpl-104a","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-104a","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"The project status is currently in the Development phase."},"finish_reason":null}]}
data: {"id":"chatcmpl-104a","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMockWithRequestBody(mockResponse1, "project status");

        await aiPlugin.sendChannelAnalysisMessage('What is the project status?');

        // 5. Wait for first response to complete
        await llmBotHelper.waitForStreamingComplete();

        // 6. Verify first response is visible
        const firstPostText = llmBotHelper.getPostText();
        await expect(firstPostText).toBeVisible();
        const firstContent = await firstPostText.textContent();
        expect(firstContent).toBeTruthy();
        expect(firstContent!.length).toBeGreaterThan(10);
        expect(firstContent).toContain('Development');

        // Mock response 2
        const mockResponse2 = `
data: {"id":"chatcmpl-104b","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-104b","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"The timeline is set for a Q1 deadline."},"finish_reason":null}]}
data: {"id":"chatcmpl-104b","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMockWithRequestBody(mockResponse2, "timeline");

        // 7. Send second question in the same RHS conversation
        await aiPlugin.sendMessage('What is the timeline?');

        // 8. Wait for second response to complete
        await llmBotHelper.waitForStreamingComplete();

        // 9. Verify second response is visible
        const secondPostText = llmBotHelper.getPostText();
        await expect(secondPostText).toBeVisible();
        const secondContent = await secondPostText.textContent();
        expect(secondContent).toBeTruthy();
        expect(secondContent!.length).toBeGreaterThan(10);
        expect(secondContent).toContain('Q1');

        // Mock response 3
        const mockResponse3 = `
data: {"id":"chatcmpl-104c","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-104c","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"The team consists of 5 developers."},"finish_reason":null}]}
data: {"id":"chatcmpl-104c","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMockWithRequestBody(mockResponse3, "many developers");

        // 10. Send third question in the same RHS conversation
        await aiPlugin.sendMessage('How many developers?');

        // 11. Wait for third response to complete
        await llmBotHelper.waitForStreamingComplete();

        // 12. Verify third response is visible and system handled multiple questions without crashes
        const thirdPostText = llmBotHelper.getPostText();
        await expect(thirdPostText).toBeVisible();
        const thirdContent = await thirdPostText.textContent();
        expect(thirdContent).toBeTruthy();
        expect(thirdContent!.length).toBeGreaterThan(10);
        expect(thirdContent).toContain('5 developers');
    });

    test('Channel analysis while RHS already open', async ({ page }) => {
        const { mmPage, aiPlugin, llmBotHelper, edgeCasesHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await edgeCasesHelper.waitForPageReady();

        // 3. Post some context messages
        await mmPage.sendChannelMessage('Discussion about the new API design');

        // 4. Open general AI RHS first (via app bar)
        await aiPlugin.openRHS();

        // 5. Verify RHS is open
        const rhsContainer = page.getByTestId('mattermost-ai-rhs');
        await expect(rhsContainer).toBeVisible();

        // 6. Now open channel analysis while RHS is already open
        await aiPlugin.openChannelAnalysisPopover();

        // Mock response
        const mockResponse = `
data: {"id":"chatcmpl-105","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-105","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"The discussion was about the new API design."},"finish_reason":null}]}
data: {"id":"chatcmpl-105","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse);

        // 7. Send a channel-specific query
        await aiPlugin.sendChannelAnalysisMessage('What topics were discussed?');

        // 8. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 9. Verify response appears correctly in RHS
        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible();
        const content = await postText.textContent();
        expect(content).toBeTruthy();
        expect(content!.length).toBeGreaterThan(10);
    });

    test('Refresh page and check persistence', async ({ page }) => {
        const { mmPage, aiPlugin, llmBotHelper, edgeCasesHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await edgeCasesHelper.waitForPageReady();

        // 3. Post a distinctive message
        await mmPage.sendChannelMessage('Important meeting scheduled for Friday');

        // 4. Open channel analysis popover and send a query
        await aiPlugin.openChannelAnalysisPopover();

        // Mock response
        const mockResponse = `
data: {"id":"chatcmpl-106","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-106","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"The meeting is scheduled for Friday."},"finish_reason":null}]}
data: {"id":"chatcmpl-106","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse);

        await aiPlugin.sendChannelAnalysisMessage('When is the meeting?');

        // 5. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 6. Verify initial response
        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible();
        const initialContent = await postText.textContent();
        expect(initialContent).toBeTruthy();

        // 7. Refresh the page
        await page.reload({ waitUntil: 'domcontentloaded' });

        // 8. Wait for page to be ready after refresh
        await edgeCasesHelper.waitForPageReady();

        // 9. Open RHS again
        await aiPlugin.openRHS();

        // 10. Open chat history to find previous conversation
        await aiPlugin.openChatHistory();

        // 11. Verify chat history is visible (previous response can be accessed)
        await aiPlugin.expectChatHistoryVisible();
    });

    test('Special characters in queries', async ({ page }) => {
        const { mmPage, aiPlugin, llmBotHelper, edgeCasesHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await edgeCasesHelper.waitForPageReady();

        // 3. Post some context
        await mmPage.sendChannelMessage('The @admin mentioned #bug-123 in the *urgent* issue');

        // 4. Open channel analysis popover via agents button in channel header
        await aiPlugin.openChannelAnalysisPopover();

        // Mock response
        const mockResponse = `
data: {"id":"chatcmpl-107","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-107","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"The user @admin mentioned #bug-123 regarding an *urgent* issue."},"finish_reason":null}]}
data: {"id":"chatcmpl-107","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse);

        // 5. Send query with special characters using the channel-specific input
        await aiPlugin.sendChannelAnalysisMessage('What @mentions and #tags were discussed? Any *important* issues?');

        // 6. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 7. Verify response handles special characters properly
        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible();
        const content = await postText.textContent();
        expect(content).toBeTruthy();
        expect(content!.length).toBeGreaterThan(10);
    });

    test('Very short query', async ({ page }) => {
        const { mmPage, aiPlugin, llmBotHelper, edgeCasesHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await edgeCasesHelper.waitForPageReady();

        // 3. Post some context
        await mmPage.sendChannelMessage('The team discussed various features and improvements');

        // 4. Open channel analysis popover via agents button in channel header
        await aiPlugin.openChannelAnalysisPopover();

        // Mock response
        const mockResponse = `
data: {"id":"chatcmpl-108","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-108","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"Summary: The team discussed various features and improvements."},"finish_reason":null}]}
data: {"id":"chatcmpl-108","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse);

        // 5. Send a very short one-word query using the channel-specific input
        await aiPlugin.sendChannelAnalysisMessage('Summary');

        // 6. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 7. Verify bot interprets minimal input and provides reasonable response
        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible();
        const content = await postText.textContent();
        expect(content).toBeTruthy();
        expect(content!.length).toBeGreaterThan(10);
    });
});
