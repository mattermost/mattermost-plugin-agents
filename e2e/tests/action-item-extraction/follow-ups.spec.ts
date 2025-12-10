import { test, expect } from '@playwright/test';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';

// spec: /Users/nickmisasi/workspace/worktrees/mattermost-plugin-ai-agents-in-e2e/e2e/specs/action-item-extraction.md
// seed: /Users/nickmisasi/workspace/worktrees/mattermost-plugin-ai-agents-in-e2e/seed.spec.ts

const username = 'regularuser';
const password = 'regularuser';

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

const actionItemsResponse = `
data: {"id":"chatcmpl-ai-5","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"role":"assistant","content":""},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-5","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"Action"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-5","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" items"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-5","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":":"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-5","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" Complete"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-5","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" quarterly"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-5","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" report"},"logprobs":null,"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';

const followUpResponse = `
data: {"id":"chatcmpl-ai-6","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"role":"assistant","content":""},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-6","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"The"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-6","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" deadline"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-6","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" is"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-6","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" end"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-6","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" of"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-6","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" month"},"logprobs":null,"finish_reason":"stop"}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';

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

test.describe('Follow-Up Interactions After Extraction', () => {
    test('Ask Clarifying Questions About Action Items', async ({ page }) => {
        const { mmPage, aiPlugin } = await setupTestPage(page);

        // 1. Create a thread with action items
        const rootPost = await mmPage.sendMessageAsUser(
            mattermost,
            username,
            password,
            'Project tasks for this quarter'
        );

        const userClient = await mattermost.getClient(username, password);

        await userClient.createPost({
            channel_id: rootPost.channel_id,
            root_id: rootPost.id,
            message: 'Complete the quarterly report by end of month'
        });

        await userClient.createPost({
            channel_id: rootPost.channel_id,
            root_id: rootPost.id,
            message: 'Schedule team meeting for project kickoff'
        });

        await userClient.createPost({
            channel_id: rootPost.channel_id,
            root_id: rootPost.id,
            message: 'Review and approve budget allocation'
        });

        // 2. Extract action items using AI Actions menu
        await page.goto(mattermost.url() + '/test/channels/town-square');
        await page.locator(`#post_${rootPost.id}`).waitFor({ state: 'visible' });
        await page.locator(`#post_${rootPost.id}`).hover();
        await page.getByTestId(`ai-actions-menu`).click();

        await openAIMock.addCompletionMock(actionItemsResponse);
        await page.getByRole('button', { name: 'Find action items' }).click();

        // 3. Wait for the initial response with action items
        await aiPlugin.expectRHSOpenWithPost();
        await expect(page.getByText(/action items/i)).toBeVisible();

        // 4. In the RHS textarea, type follow-up question
        await openAIMock.addCompletionMock(followUpResponse);

        // 5. Send follow-up question
        await aiPlugin.sendMessage("What's the deadline for the budget approval?");

        // 6. Wait for follow-up response
        const rhsContainer = page.getByTestId('mattermost-ai-rhs');
        await expect(rhsContainer.getByText(/deadline/i).first()).toBeVisible();
    });
});
