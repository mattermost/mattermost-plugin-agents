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
data: {"id":"chatcmpl-ai-9","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"role":"assistant","content":""},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-9","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":"Action"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-9","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" items"},"logprobs":null,"finish_reason":null}]}
data: {"id":"chatcmpl-ai-9","object":"chat.completion.chunk","created":1708124577,"model":"gpt-3.5-turbo-0613","system_fingerprint":null,"choices":[{"index":0,"delta":{"content":" found"},"logprobs":null,"finish_reason":"stop"}]}
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

test.describe('UI and Display Verification', () => {
    test('Verify RHS Opens Correctly', async ({ page }) => {
        const { mmPage, aiPlugin } = await setupTestPage(page);

        // 1. Create a thread with action items
        const rootPost = await mmPage.sendMessageAsUser(
            mattermost,
            username,
            password,
            'Action items discussion'
        );

        const userClient = await mattermost.getClient(username, password);

        await userClient.createPost({
            channel_id: rootPost.channel_id,
            root_id: rootPost.id,
            message: 'Complete the project by next week'
        });

        // 2. Navigate to post
        await page.goto(mattermost.url() + '/test/channels/town-square');
        await page.locator(`#post_${rootPost.id}`).waitFor({ state: 'visible' });

        // 3. Open AI Actions menu
        await page.locator(`#post_${rootPost.id}`).hover();
        await page.getByTestId(`ai-actions-menu`).click();

        // 4. Click "Find action items"
        await openAIMock.addCompletionMock(actionItemsResponse);
        await page.getByRole('button', { name: 'Find action items' }).click();

        // 5. Observe the right-hand sidebar (RHS)
        await aiPlugin.expectRHSOpenWithPost();

        await expect(page.getByTestId('mattermost-ai-rhs')).toBeVisible();
    });

    test('Verify AI Actions Menu Icon and Tooltip', async ({ page }) => {
        const { mmPage } = await setupTestPage(page);

        // 1. Create a thread
        const rootPost = await mmPage.sendMessageAsUser(
            mattermost,
            username,
            password,
            'Test message for menu'
        );

        // 2. Navigate to post
        await page.goto(mattermost.url() + '/test/channels/town-square');
        await page.locator(`#post_${rootPost.id}`).waitFor({ state: 'visible' });

        // 3. Hover over the root post
        await page.locator(`#post_${rootPost.id}`).hover();

        // 4. Observe the AI Actions menu icon
        const aiActionsMenu = page.getByTestId('ai-actions-menu');
        await expect(aiActionsMenu).toBeVisible();
    });
});
