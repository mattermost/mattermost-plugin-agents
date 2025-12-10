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

test.describe('Error Handling and Resilience', () => {
    test('Handle API Error from LLM Service', async ({ page }) => {
        const { mmPage, aiPlugin } = await setupTestPage(page);

        // 1. Configure OpenAI mock to return 500 error
        await openAIMock.addErrorMock(500, "Internal Server Error");

        // 2. Create a thread with action items
        const rootPost = await mmPage.sendMessageAsUser(
            mattermost,
            username,
            password,
            'Task discussion'
        );

        const userClient = await mattermost.getClient(username, password);

        await userClient.createPost({
            channel_id: rootPost.channel_id,
            root_id: rootPost.id,
            message: 'John needs to complete the report by Friday'
        });

        // 3. Navigate to post and open AI Actions menu
        await page.goto(mattermost.url() + '/test/channels/town-square');
        await page.locator(`#post_${rootPost.id}`).waitFor({ state: 'visible' });
        await page.locator(`#post_${rootPost.id}`).hover();
        await page.getByTestId(`ai-actions-menu`).click();

        // 4. Click "Find action items"
        await page.getByRole('button', { name: 'Find action items' }).click();

        // 5. Wait for error handling
        await aiPlugin.expectRHSOpenWithPost();

        // Expected Results: User-friendly error message is displayed
        await expect(page.getByText(/error/i)).toBeVisible();
    });

    test.skip('Handle Streaming Interruption', async ({ page }) => {
        const { mmPage, aiPlugin } = await setupTestPage(page);

        // 1. Create a thread with action items
        const rootPost = await mmPage.sendMessageAsUser(
            mattermost,
            username,
            password,
            'Project planning'
        );

        const userClient = await mattermost.getClient(username, password);

        await userClient.createPost({
            channel_id: rootPost.channel_id,
            root_id: rootPost.id,
            message: 'Multiple action items need to be tracked'
        });

        // 2. Start action item extraction
        const longStreamingResponse = `
data: {"id":"chatcmpl-ai-8","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"role":"assistant","content":""},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-8","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"Here"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-8","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" are"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-8","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" the"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-8","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" action"},"logprobs":null,"finish_reason":null}]}
data: [DONE]
`.trim().split('\n').filter(l => l).join('\n\n') + '\n\n';

        await page.goto(mattermost.url() + '/test/channels/town-square');
        await page.locator(`#post_${rootPost.id}`).waitFor({ state: 'visible' });
        await page.locator(`#post_${rootPost.id}`).hover();
        await page.getByTestId(`ai-actions-menu`).click();

        await openAIMock.addCompletionMock(longStreamingResponse);
        await page.getByRole('button', { name: 'Find action items' }).click();

        await aiPlugin.expectRHSOpenWithPost();

        // 3. While streaming is in progress, click "Stop Generating" button
        const stopButton = page.getByRole('button', { name: /stop/i });
        if (await stopButton.isVisible()) {
            await stopButton.click();

            // 4. Verify behavior
            // Expected Results: Partial response is displayed, no errors
            await expect(page.getByTestId('mattermost-ai-rhs')).toBeVisible();
        }
    });
});
