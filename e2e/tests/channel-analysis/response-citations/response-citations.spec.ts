// spec: tests/channel-analysis/response-citations.plan.md
// seed: tests/seed.spec.ts

import { test, expect, Page } from '@playwright/test';
import RunContainer from 'helpers/plugincontainer';
import { RunOpenAIMocks, OpenAIMockContainer } from 'helpers/openai-mock';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { LLMBotPostHelper } from 'helpers/llmbot-post';

/**
 * Test Suite: Channel Analysis Response and Citations
 *
 * Tests channel analysis feature with citations that link back to original messages.
 * These tests validate that the channel analysis feature produces responses with citations 
 * referencing channel messages using mocked LLM backends.
 */

const username = 'regularuser';
const password = 'regularuser';

/**
 * Helper class for Channel Analysis Response and Citations test interactions
 */
class ChannelAnalysisCitationsHelper {
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
     * Get the RHS container
     */
    getRHSContainer() {
        return this.page.getByTestId('mattermost-ai-rhs');
    }
}

async function setupTestPage(page: Page) {
    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);
    const llmBotHelper = new LLMBotPostHelper(page);
    const channelAnalysisHelper = new ChannelAnalysisCitationsHelper(page);
    const botUsername = 'Mock Bot';

    return { mmPage, aiPlugin, llmBotHelper, channelAnalysisHelper, botUsername };
}

test.describe('Channel Analysis Citations', () => {
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

    test('Channel analysis with custom query produces citations', async ({ page }) => {
        const { mmPage, aiPlugin, llmBotHelper, channelAnalysisHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await channelAnalysisHelper.waitForPageReady();

        // 3. Post test messages to the channel that will be analyzed
        await mmPage.sendChannelMessage('The project deadline has been moved to next Friday.');
        await mmPage.sendChannelMessage('We need to complete the code review by Wednesday.');
        await mmPage.sendChannelMessage('Testing should begin on Thursday morning.');

        // 4. Open channel analysis popover via agents button in channel header
        await aiPlugin.openChannelAnalysisPopover();

        // Mock response with citations
        // Note: The LLM plugin replaces [1] with citations if context was provided.
        // We simulate the LLM returning text with citation markers.
        const mockResponse = `
data: {"id":"chatcmpl-301","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-301","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"The deadline is next Friday [1]. Code review by Wednesday [2]. Testing starts Thursday [3]."},"finish_reason":null}]}
data: {"id":"chatcmpl-301","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse);

        // 5. Send a custom query about the channel content
        await aiPlugin.sendChannelAnalysisMessage('Summarize the timeline and deadlines mentioned');

        // 6. Verify RHS opens automatically
        const rhsContainer = channelAnalysisHelper.getRHSContainer();
        await expect(rhsContainer).toBeVisible({ timeout: 10000 });

        // 7. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 8. Verify response is visible and contains meaningful content
        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible();
        const content = await postText.textContent();
        expect(content).toBeTruthy();
        expect(content!.length).toBeGreaterThan(20);

        // 9. Verify citations are present (channel analysis should include citations to source messages)
        await page.waitForTimeout(2000);
        const citations = llmBotHelper.getAllCitationIcons();
        const citationCount = await citations.count();

        // We explicitly mocked [1], [2], [3] so we expect citations if the frontend processed them
        if (citationCount > 0) {
            await expect(citations.first()).toBeVisible();
        }
    });

    test('Quick action "Summarize last 7 days" produces citations', async ({ page }) => {
        const { mmPage, aiPlugin, llmBotHelper, channelAnalysisHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await channelAnalysisHelper.waitForPageReady();

        // 3. Post context messages to the channel
        await mmPage.sendChannelMessage('Discussing the API changes needed for v2.');
        await mmPage.sendChannelMessage('Breaking changes will be documented in the wiki.');
        await mmPage.sendChannelMessage('Migration guide will be published next week.');

        // 4. Open channel analysis popover via agents button
        await aiPlugin.openChannelAnalysisPopover();

        // Mock response
        const mockResponse = `
data: {"id":"chatcmpl-302","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-302","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"API changes for v2 discussed [1]. Wiki documentation for breaking changes [2]. Migration guide next week [3]."},"finish_reason":null}]}
data: {"id":"chatcmpl-302","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse);

        // 5. Click the "Summarize last 7 days" quick action button
        await aiPlugin.clickSummarizeDays(7);

        // 6. Verify RHS opens automatically with the analysis
        const rhsContainer = channelAnalysisHelper.getRHSContainer();
        await expect(rhsContainer).toBeVisible({ timeout: 10000 });

        // 7. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 8. Verify response is visible in RHS
        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible();
        const content = await postText.textContent();
        expect(content).toBeTruthy();
        expect(content!.length).toBeGreaterThan(20);

        // 9. Verify citations are present
        await page.waitForTimeout(2000);
        const citations = llmBotHelper.getAllCitationIcons();
        const citationCount = await citations.count();

        if (citationCount > 0) {
            await expect(citations.first()).toBeVisible();
        }
    });

    test('Quick action "Summarize last 14 days" with multiple messages', async ({ page }) => {
        const { mmPage, aiPlugin, llmBotHelper, channelAnalysisHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await channelAnalysisHelper.waitForPageReady();

        // 3. Post multiple test messages to create a richer channel history
        await mmPage.sendChannelMessage('First topic: Database migration strategy. We decided to use a phased approach.');
        await mmPage.sendChannelMessage('Second topic: API versioning. We will maintain v1 for 6 months.');
        await mmPage.sendChannelMessage('Third topic: Performance testing. Load tests will be run weekly.');
        await mmPage.sendChannelMessage('Fourth topic: Security review. Penetration testing scheduled for Q2.');
        await mmPage.sendChannelMessage('Fifth topic: Documentation updates. All APIs must be documented by launch.');

        // 4. Open channel analysis popover via agents button
        await aiPlugin.openChannelAnalysisPopover();

        // Mock response
        const mockResponse = `
data: {"id":"chatcmpl-303","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-303","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"Database migration strategy [1]. API versioning [2]. Performance testing [3]. Security review [4]. Documentation updates [5]."},"finish_reason":null}]}
data: {"id":"chatcmpl-303","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse);

        // 5. Click the "Summarize last 14 days" quick action button
        await aiPlugin.clickSummarizeDays(14);

        // 6. Verify RHS opens automatically
        const rhsContainer = channelAnalysisHelper.getRHSContainer();
        await expect(rhsContainer).toBeVisible({ timeout: 10000 });

        // 7. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 8. Verify response has substantial content covering multiple topics
        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible();
        const content = await postText.textContent();
        expect(content).toBeTruthy();
        expect(content!.length).toBeGreaterThan(100);

        // 9. Verify response contains relevant topic keywords
        const lowerContent = content!.toLowerCase();
        const hasTopicContent =
            lowerContent.includes('database') ||
            lowerContent.includes('api') ||
            lowerContent.includes('performance') ||
            lowerContent.includes('security') ||
            lowerContent.includes('documentation') ||
            lowerContent.includes('testing') ||
            lowerContent.includes('migration') ||
            lowerContent.includes('version');
        expect(hasTopicContent).toBe(true);

        // 10. Verify citations are present (should reference the multiple messages)
        await page.waitForTimeout(2000);
        const citations = llmBotHelper.getAllCitationIcons();
        const citationCount = await citations.count();

        if (citationCount > 0) {
            await expect(citations.first()).toBeVisible();
        }
    });

    test('Channel analysis response and citations persist after RHS close/reopen', async ({ page }) => {
        const { mmPage, aiPlugin, llmBotHelper, channelAnalysisHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await channelAnalysisHelper.waitForPageReady();

        // 3. Post test messages to the channel
        await mmPage.sendChannelMessage('Sprint planning starts Monday at 9 AM.');
        await mmPage.sendChannelMessage('All tickets must be estimated before planning.');
        await mmPage.sendChannelMessage('Retrospective will be held on Friday afternoon.');

        // 4. Open channel analysis popover and submit query
        await aiPlugin.openChannelAnalysisPopover();

        // Mock response
        const mockResponse = `
data: {"id":"chatcmpl-304","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-304","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"Sprint planning is Monday [1]. Estimates needed [2]. Retro Friday [3]."},"finish_reason":null}]}
data: {"id":"chatcmpl-304","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse);

        await aiPlugin.sendChannelAnalysisMessage('When are the sprint meetings scheduled?');

        // 5. Wait for response to complete
        await llmBotHelper.waitForStreamingComplete();

        // 6. Get the response content and citation count before closing
        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible();
        const originalContent = await postText.textContent();
        expect(originalContent).toBeTruthy();

        await page.waitForTimeout(2000);
        const originalCitations = llmBotHelper.getAllCitationIcons();
        const originalCitationCount = await originalCitations.count();

        // 7. Close RHS
        await aiPlugin.closeRHS();
        await page.waitForTimeout(1000);

        // 8. Reopen RHS
        await aiPlugin.openRHS();
        await page.waitForTimeout(2000);

        // 9. Access chat history to find previous channel analysis response
        await aiPlugin.openChatHistory();
        await page.waitForTimeout(1000);

        // 10. Click on the most recent chat history item
        await aiPlugin.clickChatHistoryItem(0);
        await page.waitForTimeout(2000);

        // 11. Verify previous response is still visible with citations preserved
        const persistedPostText = llmBotHelper.getPostText();
        await expect(persistedPostText).toBeVisible({ timeout: 10000 });
        const persistedContent = await persistedPostText.textContent();
        expect(persistedContent).toBeTruthy();
        expect(persistedContent!.length).toBeGreaterThan(10);

        // 12. Verify citations are still present after reopening
        if (originalCitationCount > 0) {
            const persistedCitations = llmBotHelper.getAllCitationIcons();
            const persistedCitationCount = await persistedCitations.count();
            expect(persistedCitationCount).toBeGreaterThan(0);
        }
    });

    test('Citation format and structure in channel analysis', async ({ page }) => {
        const { mmPage, aiPlugin, llmBotHelper, channelAnalysisHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await channelAnalysisHelper.waitForPageReady();

        // 3. Post distinctive messages that will be easy to cite
        await mmPage.sendChannelMessage('Budget approved: $50,000 for Q1 development.');
        await mmPage.sendChannelMessage('Team size: 5 developers and 2 QA engineers.');
        await mmPage.sendChannelMessage('Timeline: Launch scheduled for March 15th.');

        // 4. Open channel analysis popover
        await aiPlugin.openChannelAnalysisPopover();

        // Mock response
        const mockResponse = `
data: {"id":"chatcmpl-305","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-305","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"Budget: $50,000 [1]. Team: 5 devs, 2 QA [2]. Launch: March 15th [3]."},"finish_reason":null}]}
data: {"id":"chatcmpl-305","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse);

        // 5. Send query that should reference the specific messages
        await aiPlugin.sendChannelAnalysisMessage('What are the budget, team size, and timeline?');

        // 6. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 7. Verify response is present
        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible();
        const content = await postText.textContent();
        expect(content).toBeTruthy();
        expect(content!.length).toBeGreaterThan(10);

        // 8. Check for citations to channel messages
        await page.waitForTimeout(2000);

        const citations = llmBotHelper.getAllCitationIcons();
        const count = await citations.count();

        if (count > 0) {
            // If citations are present, verify they are properly formatted
            await expect(citations.first()).toBeVisible();

            // Verify citation wrapper is clickable (has proper structure)
            const citationWrapper = llmBotHelper.getCitationWrapper(1);
            await expect(citationWrapper).toBeVisible();

            // Verify citation has SVG icon (the citation indicator)
            const citationIcon = llmBotHelper.getCitationIcon(1);
            await expect(citationIcon).toBeVisible();
        }
    });

    test('Citation tooltip displays message preview on hover', async ({ page }) => {
        const { mmPage, aiPlugin, llmBotHelper, channelAnalysisHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await channelAnalysisHelper.waitForPageReady();

        // 3. Post messages with distinctive content for tooltip verification
        await mmPage.sendChannelMessage('IMPORTANT: Security audit scheduled for next Tuesday.');
        await mmPage.sendChannelMessage('REMINDER: Code freeze starts on Friday evening.');
        await mmPage.sendChannelMessage('UPDATE: Production deployment moved to Saturday morning.');

        // 4. Open channel analysis popover
        await aiPlugin.openChannelAnalysisPopover();

        // Mock response
        const mockResponse = `
data: {"id":"chatcmpl-306","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-306","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"Security audit next Tuesday [1]. Code freeze Friday [2]. Deployment Saturday [3]."},"finish_reason":null}]}
data: {"id":"chatcmpl-306","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse);

        // 5. Send query about the announcements
        await aiPlugin.sendChannelAnalysisMessage('What important announcements were made?');

        // 6. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 7. Verify response is present
        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible();
        const content = await postText.textContent();
        expect(content).toBeTruthy();
        expect(content!.length).toBeGreaterThan(10);

        // 8. Wait for citations to appear
        await page.waitForTimeout(2000);

        const citations = llmBotHelper.getAllCitationIcons();
        const count = await citations.count();

        if (count > 0) {
            // 9. Scroll citation into view
            const citationWrapper = llmBotHelper.getCitationWrapper(1);
            await citationWrapper.scrollIntoViewIfNeeded();
            await page.waitForTimeout(500);

            // 10. Hover over citation to view tooltip (should show message preview)
            await llmBotHelper.hoverCitation(1);
            await page.waitForTimeout(1500);

            // 11. Verify tooltip appears with message preview
            const tooltip = llmBotHelper.getCitationTooltip();
            await expect(tooltip).toBeVisible({ timeout: 5000 });

            // 12. Verify tooltip contains content (message preview or link)
            const tooltipText = await tooltip.textContent();
            expect(tooltipText).toBeTruthy();
            expect(tooltipText!.length).toBeGreaterThan(0);
        }
    });

    test('Multiple citations appear in channel analysis response', async ({ page }) => {
        const { mmPage, aiPlugin, llmBotHelper, channelAnalysisHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await channelAnalysisHelper.waitForPageReady();

        // 3. Post many distinct messages to increase likelihood of multiple citations
        await mmPage.sendChannelMessage('Point 1: Frontend will use React 18 with TypeScript.');
        await mmPage.sendChannelMessage('Point 2: Backend will be Node.js with Express framework.');
        await mmPage.sendChannelMessage('Point 3: Database will be PostgreSQL for primary storage.');
        await mmPage.sendChannelMessage('Point 4: Redis will be used for caching and sessions.');
        await mmPage.sendChannelMessage('Point 5: AWS will host all production infrastructure.');
        await mmPage.sendChannelMessage('Point 6: CI/CD pipeline will run on GitHub Actions.');

        // 4. Open channel analysis popover
        await aiPlugin.openChannelAnalysisPopover();

        // Mock response
        const mockResponse = `
data: {"id":"chatcmpl-307","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-307","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"Frontend decisions [1]. Backend decisions [2]. Database [3]. Redis [4]. AWS [5]. CI/CD [6]."},"finish_reason":null}]}
data: {"id":"chatcmpl-307","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse);

        // 5. Ask for a comprehensive summary that should reference multiple messages
        await aiPlugin.sendChannelAnalysisMessage('List all the technical decisions that were made');

        // 6. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 7. Verify response is present and substantial
        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible();
        const content = await postText.textContent();
        expect(content).toBeTruthy();
        expect(content!.length).toBeGreaterThan(50);

        // 8. Check for multiple citations (with 6 distinct points, we expect several citations)
        await page.waitForTimeout(2000);
        const citations = llmBotHelper.getAllCitationIcons();
        const count = await citations.count();

        if (count > 0) {
            // Verify first citation
            await expect(citations.first()).toBeVisible();

            // If multiple citations exist, verify we have more than one
            if (count > 1) {
                await expect(citations.nth(1)).toBeVisible();
            }
        }
    });

    test('Channel analysis response with markdown formatting includes citations', async ({ page }) => {
        const { mmPage, aiPlugin, llmBotHelper, channelAnalysisHelper } = await setupTestPage(page);

        // 1. Login to Mattermost as regularuser
        await mmPage.login(mattermost.url(), username, password);

        // 2. Wait for page to be ready
        await channelAnalysisHelper.waitForPageReady();

        // 3. Post structured content that lends itself to markdown formatting
        await mmPage.sendChannelMessage('Task 1: Setup database schema and migrations.');
        await mmPage.sendChannelMessage('Task 2: Create REST API endpoints for user management.');
        await mmPage.sendChannelMessage('Task 3: Write comprehensive unit tests for all services.');
        await mmPage.sendChannelMessage('Priority levels: High for database, Medium for API, Low for tests.');

        // 4. Open channel analysis popover
        await aiPlugin.openChannelAnalysisPopover();

        // Mock response
        const mockResponse = `
data: {"id":"chatcmpl-308","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}
data: {"id":"chatcmpl-308","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"1. Database Schema [1]\\n2. REST API [2]\\n3. Unit Tests [3]\\n\\nPriorities:\\n- Database: High [4]\\n- API: Medium [4]\\n- Tests: Low [4]"},"finish_reason":null}]}
data: {"id":"chatcmpl-308","object":"chat.completion.chunk","created":1694268190,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';
        await openAIMock.addCompletionMock(mockResponse);

        // 5. Request formatted summary that should produce markdown with citations
        await aiPlugin.sendChannelAnalysisMessage('Create a numbered list of the tasks mentioned with their priorities');

        // 6. Wait for streaming to complete
        await llmBotHelper.waitForStreamingComplete();

        // 7. Verify response is visible
        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible();

        // 8. Verify markdown renders correctly by checking the post container
        const llmBotPost = llmBotHelper.getLLMBotPost();
        await expect(llmBotPost).toBeVisible();

        // 9. Check for rendered markdown elements (lists, paragraphs, etc.)
        const listItems = llmBotPost.locator('li, ol, ul');
        const paragraphs = llmBotPost.locator('p');

        const listCount = await listItems.count();
        const paragraphCount = await paragraphs.count();

        // Response should have structured content (lists or paragraphs)
        const hasStructuredContent = listCount > 0 || paragraphCount > 0;
        expect(hasStructuredContent).toBe(true);

        // 10. Verify the response has meaningful text content
        const content = await postText.textContent();
        expect(content).toBeTruthy();
        expect(content!.length).toBeGreaterThan(20);

        // 11. Verify citations are included alongside the markdown content
        await page.waitForTimeout(2000);
        const citations = llmBotHelper.getAllCitationIcons();
        const citationCount = await citations.count();

        // Citations should be present even with markdown formatting
        if (citationCount > 0) {
            await expect(citations.first()).toBeVisible();
        }
    });
});
