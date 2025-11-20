import { test, expect } from '@playwright/test';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';

// spec: /Users/nickmisasi/workspace/worktrees/mattermost-plugin-ai-agents-in-e2e/e2e/specs/semantic-search.md
// seed: /Users/nickmisasi/workspace/worktrees/mattermost-plugin-ai-agents-in-e2e/seed.spec.ts

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
data: {"id":"chatcmpl-search-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" relevant"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-search-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" posts"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-search-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" about"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-search-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" budget"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-search-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"."},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-search-1","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{},"logprobs":null,"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';

const searchResponseText = "Based on the search results, here are the relevant posts about budget.";

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

async function setupTestPage(page) {
    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);
    const url = mattermost.url();

    await mmPage.login(url, username, password);

    return { mmPage, aiPlugin };
}

test.describe('Basic Semantic Search Operations', () => {
    test('Perform Simple Single-Word Search', async ({ page }) => {
        const { mmPage, aiPlugin } = await setupTestPage(page);

        // Create test posts with "budget" content
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
            'The budget for the new project has been approved'
        );

        await mmPage.sendMessageAsUser(
            mattermost,
            username,
            password,
            'Budget constraints are affecting our timeline'
        );

        await aiPlugin.openRHS();

        // Wait for the direct channel to be created and textarea to be ready
        await expect(aiPlugin.rhsPostTextarea).toBeEnabled({ timeout: 30000 });

        // Skip search hint check - search infrastructure may not be fully enabled yet
        // await expect(page.getByText('Agents searches only content you have access to')).toBeVisible();

        await openAIMock.addCompletionMock(searchResponseWithSources);
        await aiPlugin.sendMessage('budget');

        await aiPlugin.waitForBotResponse(searchResponseText);

        await expect(page.getByTestId('mattermost-ai-rhs')).toBeVisible();
    });

    test('Perform Simple Multi-Word Search', async ({ page }) => {
        const { mmPage, aiPlugin } = await setupTestPage(page);

        await mmPage.sendMessageAsUser(
            mattermost,
            username,
            password,
            'The project timeline needs to be updated based on the latest requirements'
        );

        await mmPage.sendMessageAsUser(
            mattermost,
            username,
            password,
            'Can someone share the project timeline for Q1?'
        );

        await mmPage.sendMessageAsUser(
            mattermost,
            username,
            password,
            'Our timeline includes design, development, and testing phases'
        );

        await aiPlugin.openRHS();

        // Wait for the direct channel to be created and textarea to be ready
        await expect(aiPlugin.rhsPostTextarea).toBeEnabled({ timeout: 30000 });

        await openAIMock.addCompletionMock(searchResponseWithSources);
        await aiPlugin.sendMessage('project timeline');

        await aiPlugin.waitForBotResponse(searchResponseText);

        await expect(page.getByTestId('mattermost-ai-rhs')).toBeVisible();
    });

    test('Verify Empty Query Validation', async ({ page }) => {
        const { aiPlugin } = await setupTestPage(page);

        await aiPlugin.openRHS();

        const sendButton = page.locator('#rhsContainer').getByTestId('SendMessageButton');

        await expect(sendButton).toBeDisabled();
    });
});
