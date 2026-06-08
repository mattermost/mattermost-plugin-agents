import { test, expect, Page } from '@playwright/test';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';
import { mattermostAIPluginRoutes } from 'helpers/plugin-http';

const username = 'regularuser';
const password = 'regularuser';

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

const searchResponseWithSources = `
data: {"id":"chatcmpl-search-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"role":"assistant","content":""},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-search-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"Based"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-search-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" on"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-search-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" the"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-search-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" search"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-search-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" results"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-search-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":","},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-search-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" here"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-search-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" are"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-search-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" the"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-search-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" findings"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-search-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" about"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-search-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" budget"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-search-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"."},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-search-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{},"logprobs":null,"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';

const searchResponseWithSourcesText = "Based on the search results, here are the findings about budget.";
const searchResponseNoResultsText = "I couldn't find any relevant messages for your query. Please try a different search term.";

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
    const url = mattermost.url();

    await mmPage.login(url, username, password);

    return { mmPage, aiPlugin };
}

test.describe('Search Sources Display', () => {
    test('Search response text displays in RHS', async ({ page }) => {
        const { mmPage, aiPlugin } = await setupTestPage(page);

        // Create posts with searchable content about budget
        await mmPage.sendMessageAsUser(
            mattermost,
            username,
            password,
            'The Q4 budget report shows a 15% increase in marketing spend'
        );

        await mmPage.sendMessageAsUser(
            mattermost,
            username,
            password,
            'Budget allocation for engineering has been approved for next quarter'
        );

        await mmPage.sendMessageAsUser(
            mattermost,
            username,
            password,
            'We need to finalize the budget review meeting notes'
        );

        // Wait for posts to be indexed by the embedding search
        await page.waitForTimeout(2000);

        // Set up mock response for the LLM
        await openAIMock.addCompletionMock(searchResponseWithSources);

        // Wait for plugin to be fully initialized (app bar icon indicates plugin is ready)
        await aiPlugin.openRHS();
        await expect(aiPlugin.rhsPostTextarea).toBeEnabled({ timeout: 30000 });
        await aiPlugin.closeRHS();

        // Trigger embedding search via the search bar
        await aiPlugin.triggerEmbeddingSearch('budget');

        // Wait for bot response in RHS
        await aiPlugin.waitForBotResponse(searchResponseWithSourcesText);

        // Verify RHS is still visible with search response text
        await expect(page.getByTestId('mattermost-ai-rhs')).toBeVisible();
        await expect(page.getByText(searchResponseWithSourcesText)).toBeVisible();
    });

    test('Search query with no channel results returns appropriate message', async ({ page }) => {
        await setupTestPage(page);

        const userClient = await mattermost.getClient(username, password);
        const currentUser = await userClient.getMe();
        const secondUser = await userClient.getUserByUsername('seconduser');
        const emptyDMChannel = await userClient.createDirectChannel([currentUser.id, secondUser.id]);

        const routes = mattermostAIPluginRoutes(mattermost.url());
        const payload = await routes.postJson('search', userClient.getToken(), {
            query: 'xyznonexistent12345',
            channelId: emptyDMChannel.id,
        }) as {
            answer: string;
            results: unknown[];
        };
        expect(payload.answer).toBe(searchResponseNoResultsText);
        expect(payload.results).toEqual([]);
    });
});
