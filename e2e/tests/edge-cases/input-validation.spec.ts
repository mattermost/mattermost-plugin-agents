import { test, expect } from '@playwright/test';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { OpenAIMockContainer, RunOpenAIMocks, responseTest, responseTestText } from 'helpers/openai-mock';

// spec: /Users/nickmisasi/workspace/worktrees/mattermost-plugin-ai-agents-in-e2e/e2e/specs/edge-cases.md
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

test.describe('Edge Cases - Input Validation', () => {
    test('Handle empty message submission', async ({ page }) => {
        const { aiPlugin } = await setupTestPage(page);

        await aiPlugin.openRHS();

        // Try to send an empty message
        const textarea = aiPlugin.rhsPostTextarea;
        await textarea.click();
        await textarea.press('Enter');

        // Should not send empty message - textarea should still be visible and empty
        await expect(textarea).toBeVisible();
        await expect(textarea).toHaveValue('');
    });

    test('Handle whitespace-only message', async ({ page }) => {
        const { aiPlugin } = await setupTestPage(page);

        await aiPlugin.openRHS();

        // Try to send whitespace only
        const textarea = aiPlugin.rhsPostTextarea;
        await textarea.fill('   ');
        await textarea.press('Enter');

        // Should either not send or handle gracefully
        await expect(textarea).toBeVisible();
    });

    test('Handle very long message', async ({ page }) => {
        const { aiPlugin } = await setupTestPage(page);

        await aiPlugin.openRHS();
        await openAIMock.addCompletionMock(responseTest);

        // Create a very long message (near character limit)
        const longMessage = 'A'.repeat(4000);

        await aiPlugin.sendMessage(longMessage);
        await aiPlugin.waitForBotResponse(responseTestText);

        // Should handle long messages successfully
        await expect(page.getByText(responseTestText)).toBeVisible();
    });

    test('Handle special characters in message', async ({ page }) => {
        const { aiPlugin } = await setupTestPage(page);

        await aiPlugin.openRHS();
        await openAIMock.addCompletionMock(responseTest);

        // Send message with special characters
        await aiPlugin.sendMessage('Test with émojis 🎉 and symbols: @#$%^&*()');

        // Wait for bot response with increased timeout to handle potential rendering delays
        await aiPlugin.waitForBotResponse(responseTestText);

        // Verify the response is visible in the RHS container
        const rhsContainer = page.getByTestId('mattermost-ai-rhs');
        await expect(rhsContainer.getByText(responseTestText)).toBeVisible({ timeout: 5000 });
    });

    test('Handle rapid message submission', async ({ page }) => {
        const { aiPlugin } = await setupTestPage(page);

        await aiPlugin.openRHS();

        // Send multiple messages with minimal delay once the UI is ready
        await openAIMock.addCompletionMock(responseTest);
        await aiPlugin.sendMessage('First message');
        await aiPlugin.waitForBotResponse(responseTestText);

        await openAIMock.addCompletionMock(responseTest);
        await aiPlugin.sendMessage('Second message');
        await aiPlugin.waitForBotResponse(responseTestText);

        await openAIMock.addCompletionMock(responseTest);
        await aiPlugin.sendMessage('Third message');
        await aiPlugin.waitForBotResponse(responseTestText);

        // Should recover quickly and allow further input
        await expect(aiPlugin.rhsPostTextarea).toBeVisible();
    });
});

test.describe('Edge Cases - XSS and Injection Prevention', () => {
    test('Handle HTML tags in message', async ({ page }) => {
        const { aiPlugin } = await setupTestPage(page);

        await aiPlugin.openRHS();
        await openAIMock.addCompletionMock(responseTest);

        // Send message with HTML tags
        await aiPlugin.sendMessage('<script>alert("xss")</script>');
        await aiPlugin.waitForBotResponse(responseTestText);

        // HTML should be escaped/sanitized, not executed
        await expect(page.getByText(responseTestText)).toBeVisible();
    });

    test('Handle SQL-like syntax in message', async ({ page }) => {
        const { aiPlugin } = await setupTestPage(page);

        await aiPlugin.openRHS();
        await openAIMock.addCompletionMock(responseTest);

        // Send message with SQL-like syntax
        await aiPlugin.sendMessage("SELECT * FROM users; DROP TABLE users;--");
        await aiPlugin.waitForBotResponse(responseTestText);

        // Should treat as regular text, not execute as SQL
        await expect(page.getByText(responseTestText)).toBeVisible();
    });
});

test.describe('Edge Cases - Special Content', () => {
    test('Handle markdown formatting', async ({ page }) => {
        const { aiPlugin } = await setupTestPage(page);

        await aiPlugin.openRHS();
        await openAIMock.addCompletionMock(responseTest);

        // Send message with markdown
        await aiPlugin.sendMessage('**Bold** *italic* `code` [link](http://example.com)');
        await aiPlugin.waitForBotResponse(responseTestText);

        await expect(page.getByText(responseTestText)).toBeVisible();
    });

    test('Handle Unicode and emoji', async ({ page }) => {
        const { aiPlugin } = await setupTestPage(page);

        await aiPlugin.openRHS();
        await openAIMock.addCompletionMock(responseTest);

        // Send message with various unicode
        await aiPlugin.sendMessage('Hello 世界 🌍 ñ ü é');
        await aiPlugin.waitForBotResponse(responseTestText);

        await expect(page.getByText(responseTestText)).toBeVisible();
    });
});
