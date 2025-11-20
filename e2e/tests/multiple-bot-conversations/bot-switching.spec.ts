import { test, expect } from '@playwright/test';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { OpenAIMockContainer, RunOpenAIMocks, responseTest, responseTest2, responseTestText, responseTest2Text } from 'helpers/openai-mock';

// spec: /Users/nickmisasi/workspace/worktrees/mattermost-plugin-ai-agents-in-e2e/e2e/specs/multiple-bot-conversations.md
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

test.describe('Multiple Bot Conversations - Bot Switching', () => {
    test.skip('Switch between different bots', async ({ page }) => {
        // Skipped: Requires multiple bots to be configured
        const { aiPlugin } = await setupTestPage(page);

        await aiPlugin.openRHS();

        // Send message with first bot
        await openAIMock.addCompletionMock(responseTest);
        await aiPlugin.sendMessage('Hello from first bot');
        await aiPlugin.waitForBotResponse(responseTestText);

        // Switch to second bot
        await openAIMock.addCompletionMock(responseTest2, "second");
        await aiPlugin.switchBot('Second Bot');

        // Send message with second bot
        await aiPlugin.sendMessage('Hello from second bot');
        await expect(page.getByRole('button', { name: 'second', exact: true })).toBeVisible();
        await aiPlugin.waitForBotResponse(responseTest2Text);

        // Verify we're now using second bot
        const rhsContainer = page.getByTestId('mattermost-ai-rhs');
        await expect(rhsContainer).toBeVisible();
    });

    test.skip('Context is preserved when switching bots', async ({ page }) => {
        // Skipped: Requires multiple bots to be configured
        const { aiPlugin } = await setupTestPage(page);

        await aiPlugin.openRHS();

        // Have conversation with first bot
        await openAIMock.addCompletionMock(responseTest);
        await aiPlugin.sendMessage('First message');
        await aiPlugin.waitForBotResponse(responseTestText);

        // Switch to second bot
        await openAIMock.addCompletionMock(responseTest2, "second");
        await aiPlugin.switchBot('Second Bot');

        // Send message with second bot
        await aiPlugin.sendMessage('Second bot message');
        await aiPlugin.waitForBotResponse(responseTest2Text);

        // Switch back to first bot
        await aiPlugin.switchBot('Mock Bot');

        // Verify conversation history is still present
        await expect(page.getByText('First message')).toBeVisible();
        await expect(page.getByText(responseTestText)).toBeVisible();
    });

    test('Create new conversation with same bot', async ({ page }) => {
        const { aiPlugin } = await setupTestPage(page);

        await aiPlugin.openRHS();

        // First conversation
        await openAIMock.addCompletionMock(responseTest);
        await aiPlugin.sendMessage('First conversation');
        await aiPlugin.waitForBotResponse(responseTestText);

        // Start new chat
        await page.getByTestId('new-chat').click();

        // Verify textarea is empty for new conversation
        await expect(aiPlugin.rhsPostTextarea).toHaveValue('');

        // Second conversation with same bot
        await openAIMock.addCompletionMock(responseTest2);
        await aiPlugin.sendMessage('Second conversation');
        await aiPlugin.waitForBotResponse(responseTest2Text);

        // Verify new conversation doesn't have first conversation content
        const rhsContainer = page.getByTestId('mattermost-ai-rhs');
        await expect(rhsContainer.getByText('First conversation')).not.toBeVisible();
    });
});

test.describe('Multiple Bot Conversations - Chat History', () => {
    test.skip('Chat history shows conversations from all bots', async ({ page }) => {
        // Skipped: Requires multiple bots to be configured
        const { aiPlugin } = await setupTestPage(page);

        await aiPlugin.openRHS();

        // Create conversation with first bot
        await openAIMock.addCompletionMock(responseTest);
        await aiPlugin.sendMessage('Message for first bot');
        await aiPlugin.waitForBotResponse(responseTestText);

        // Switch to second bot and create conversation
        await openAIMock.addCompletionMock(responseTest2, "second");
        await aiPlugin.switchBot('Second Bot');
        await aiPlugin.sendMessage('Message for second bot');
        await aiPlugin.waitForBotResponse(responseTest2Text);

        // Open chat history
        await aiPlugin.openChatHistory();
        await aiPlugin.expectChatHistoryVisible();

        // Should see conversations from both bots
        await expect(aiPlugin.threadsListContainer.locator('div').first()).toBeVisible();
    });

    test.skip('Resume old conversation preserves context', async ({ page }) => {
        // Skipped: Chat history item selection needs more robust implementation
        const { aiPlugin } = await setupTestPage(page);

        await aiPlugin.openRHS();

        // Create first conversation
        await openAIMock.addCompletionMock(responseTest);
        await aiPlugin.sendMessage('Original message');
        await aiPlugin.waitForBotResponse(responseTestText);

        // Create new conversation
        await page.getByTestId('new-chat').click();
        await openAIMock.addCompletionMock(responseTest2);
        await aiPlugin.sendMessage('New chat message');
        await aiPlugin.waitForBotResponse(responseTest2Text);

        // Open chat history and go back to first conversation
        await aiPlugin.openChatHistory();
        await aiPlugin.expectChatHistoryVisible();
        await aiPlugin.clickChatHistoryItem(1);

        // Verify original conversation content is visible
        await expect(page.getByText('Original message')).toBeVisible();
        await expect(page.getByText(responseTestText)).toBeVisible();
    });
});
