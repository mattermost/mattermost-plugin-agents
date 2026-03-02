import { test, expect, Page } from '@playwright/test';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';
import { LLMBotPostHelper } from 'helpers/llmbot-post';

const username = 'regularuser';
const password = 'regularuser';

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

// Mock response with citation markers that the backend will process into post citations
// The !!CITE1!! and !!CITE2!! markers get replaced by the backend with citation UI elements
const searchResponseWithCitations = `
data: {"id":"chatcmpl-citations-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"role":"assistant","content":""},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-citations-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"Based"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-citations-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" on"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-citations-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" the"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-citations-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" discussion"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-citations-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" !!CITE1!!"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-citations-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" the"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-citations-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" budget"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-citations-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" has"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-citations-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" been"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-citations-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" approved"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-citations-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" !!CITE2!!"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-citations-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"."},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-citations-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{},"logprobs":null,"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';

const searchResponseText = "Based on the discussion";

test.beforeAll(async () => {
    mattermost = await RunContainer();
    openAIMock = await RunOpenAIMocks(mattermost.network);
});

test.beforeEach(async () => {
    // Reset mocks before each test to prevent cross-contamination
    await openAIMock.resetMocks();
});

test.afterAll(async () => {
    await openAIMock.stop();
    await mattermost.stop();
});

async function setupTestPage(page: Page) {
    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);
    const llmBotHelper = new LLMBotPostHelper(page);
    const url = mattermost.url();

    await mmPage.login(url, username, password);

    return { mmPage, aiPlugin, llmBotHelper };
}

test.describe('Post Citations Display', () => {
    test('Post citations display with tooltips and navigation', async ({ page }) => {
        const { mmPage, aiPlugin, llmBotHelper } = await setupTestPage(page);

        // Create posts with searchable content that will be cited
        await mmPage.sendMessageAsUser(
            mattermost,
            username,
            password,
            'We need to discuss the Q4 budget allocation for the marketing department'
        );

        await mmPage.sendMessageAsUser(
            mattermost,
            username,
            password,
            'The budget for the new project has been approved by leadership'
        );

        // Wait for posts to be indexed by the embedding search
        await page.waitForTimeout(2000);

        // Set up the mock response with citation markers
        await openAIMock.addCompletionMock(searchResponseWithCitations);

        // Wait for plugin to be fully initialized (app bar icon indicates plugin is ready)
        await aiPlugin.openRHS();
        await expect(aiPlugin.rhsPostTextarea).toBeEnabled({ timeout: 30000 });
        await aiPlugin.closeRHS();

        // Trigger embedding search via the search bar
        await aiPlugin.triggerEmbeddingSearch('budget discussion');

        // Wait for bot response to appear
        await aiPlugin.waitForBotResponse(searchResponseText);

        // Wait for post citations to be rendered
        // The backend processes !!CITE1!! and !!CITE2!! markers and emits websocket events
        // with post_citation annotations that the frontend renders as citation icons
        await llmBotHelper.waitForPostCitation(1);

        // Verify citation icons are rendered (small circular icons with message icon)
        await llmBotHelper.expectPostCitationCount(2);

        // Verify both citation wrappers are visible
        const firstCitation = llmBotHelper.getPostCitationWrapper(1);
        const secondCitation = llmBotHelper.getPostCitationWrapper(2);
        await expect(firstCitation).toBeVisible();
        await expect(secondCitation).toBeVisible();

        // Hover over the first citation icon
        await llmBotHelper.hoverPostCitation(1);

        // Verify tooltip appears showing @username and #channelname
        const tooltip = llmBotHelper.getPostCitationTooltip();
        await expect(tooltip).toBeVisible({ timeout: 5000 });
        await expect(tooltip).toContainText('@');
        await expect(tooltip).toContainText('#');

        // Move mouse away to dismiss tooltip
        await page.mouse.move(0, 0);
        await page.waitForTimeout(500);

        // Verify we can hover over the second citation
        await llmBotHelper.hoverPostCitation(2);
        await expect(tooltip).toBeVisible({ timeout: 5000 });
        await expect(tooltip).toContainText('@');

        // Move mouse away before clicking
        await page.mouse.move(0, 0);
        await page.waitForTimeout(500);

        // Set up navigation listener before clicking the citation
        // Post citations navigate to /_redirect/pl/{postId} or /pl/{postId} URLs
        const navigationPromise = page.waitForURL(
            (url) => url.pathname.includes('/_redirect/pl/') || url.pathname.includes('/pl/'),
            { timeout: 10000 },
        );

        // Click the first citation icon
        await llmBotHelper.clickPostCitation(1);

        // Verify navigation to the source post
        await navigationPromise;
    });
});
