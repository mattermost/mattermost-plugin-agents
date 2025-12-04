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

const noActionItemsResponse = `
data: {"id":"chatcmpl-ai-3","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"role":"assistant","content":""},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-3","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"There"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-3","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" are"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-3","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" no"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-3","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" action"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-3","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" items"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-3","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" in"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-3","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" this"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-3","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" conversation"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-3","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"."},"logprobs":null,"finish_reason":"stop"}]}
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

async function gotoTownSquare(page) {
    const target = mattermost.url() + '/test/channels/town-square';

    for (let attempt = 0; attempt < 3; attempt++) {
        try {
            await page.goto(target);
            return;
        } catch (error) {
            if (attempt === 2) {
                throw error;
            }
            await page.waitForTimeout(1000);
        }
    }
}

test.describe('Edge Cases and Boundary Conditions', () => {
    test('Thread with No Action Items', async ({ page }) => {
        const { mmPage, aiPlugin } = await setupTestPage(page);

        // 1. Send a channel message: "Casual conversation"
        const rootPost = await mmPage.sendMessageAsUser(
            mattermost,
            username,
            password,
            'Casual conversation'
        );

        // 2. Create thread replies with no action items
        const userClient = await mattermost.getClient(username, password);

        // Reply 1: "How was everyone's weekend?"
        await userClient.createPost({
            channel_id: rootPost.channel_id,
            root_id: rootPost.id,
            message: "How was everyone's weekend?"
        });

        // Reply 2: "Mine was great, went hiking!"
        await userClient.createPost({
            channel_id: rootPost.channel_id,
            root_id: rootPost.id,
            message: 'Mine was great, went hiking!'
        });

        // Reply 3: "I watched a movie, it was really good"
        await userClient.createPost({
            channel_id: rootPost.channel_id,
            root_id: rootPost.id,
            message: 'I watched a movie, it was really good'
        });

        // Reply 4: "Looking forward to the holiday next month"
        await userClient.createPost({
            channel_id: rootPost.channel_id,
            root_id: rootPost.id,
            message: 'Looking forward to the holiday next month'
        });

        // 3. Navigate to the post
        await gotoTownSquare(page);
        await page.locator(`#post_${rootPost.id}`).waitFor({ state: 'visible' });

        // 4. Open AI Actions menu
        await page.locator(`#post_${rootPost.id}`).hover();
        await page.getByTestId(`ai-actions-menu`).click();

        // 5. Click "Find action items"
        await openAIMock.addCompletionMock(noActionItemsResponse);
        await page.getByRole('button', { name: 'Find action items' }).click();

        // 6. Wait for response
        await aiPlugin.expectRHSOpenWithPost();

        // Expected Results: AI responds with message indicating no action items
        const rhsContainer = page.getByTestId('mattermost-ai-rhs');
        await expect(rhsContainer.getByText(/no action items/i)).toBeVisible();
    });
});
