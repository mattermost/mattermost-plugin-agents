import { test, expect } from '@playwright/test';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { OpenAIMockContainer, RunOpenAIMocks, responseTest, responseTestText } from 'helpers/openai-mock';
import { createBotConfigHelper } from 'helpers/bot-config';

// Test configuration
const username = 'regularuser';
const password = 'regularuser';

function createTestSuite() {
    let mattermost: MattermostContainer;
    let openAIMock: OpenAIMockContainer;

    // Common test setup
    async function setupTestPage(page) {
        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);
        const url = mattermost.url();

        await mmPage.login(url, username, password);

        return { mmPage, aiPlugin };
    }

    test.describe('Bot Identity Changes', () => {
    // Setup for all tests in the file
    test.beforeAll(async () => {
        mattermost = await RunContainer();
        openAIMock = await RunOpenAIMocks(mattermost.network);
    });

    // Cleanup after all tests
    test.afterAll(async () => {
        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should update bot display name and reflect in UI', async ({ page }) => {
        const { aiPlugin } = await setupTestPage(page);
        const botConfig = await createBotConfigHelper(mattermost);

        // Get the original bot configuration
        const originalBot = await botConfig.getBotByName('mock');
        expect(originalBot).toBeDefined();
        expect(originalBot?.displayName).toBe('Mock Bot');

        // Change the display name
        await botConfig.updateBot(originalBot!.id, {
            displayName: 'AI Assistant'
        });

        // Verify the change was saved
        const updatedBot = await botConfig.getBot(originalBot!.id);
        expect(updatedBot?.displayName).toBe('AI Assistant');

        // Wait for configuration to propagate
        await page.waitForTimeout(1000);

        // Reload page to pick up configuration changes
        await page.reload();
        await page.waitForLoadState('domcontentloaded');

        // Verify in UI: Open RHS and check bot selector
        await aiPlugin.openRHS();

        // The bot selector should show the new display name
        const botSelector = page.getByTestId('bot-selector-rhs');
        await expect(botSelector).toContainText('AI Assistant');

        // Restore original name for other tests
        await botConfig.updateBot(originalBot!.id, {
            displayName: 'Mock Bot'
        });
    });

    test('should display updated display name in bot selector after multiple changes', async ({ page }) => {
        const { aiPlugin } = await setupTestPage(page);
        const botConfig = await createBotConfigHelper(mattermost);

        // Get the original bot configuration
        const originalBot = await botConfig.getBotByName('mock');
        expect(originalBot).toBeDefined();

        // First change
        await botConfig.updateBot(originalBot!.id, {
            displayName: 'Assistant v1'
        });

        await page.reload();
        await page.waitForLoadState('domcontentloaded');

        await aiPlugin.openRHS();
        let botSelector = page.getByTestId('bot-selector-rhs');
        await expect(botSelector).toContainText('Assistant v1');

        // Second change
        await botConfig.updateBot(originalBot!.id, {
            displayName: 'Assistant v2'
        });

        // Refresh the page to see the new name
        await page.reload();
        await page.waitForLoadState('domcontentloaded');

        // Need to open RHS again after page reload
        await aiPlugin.openRHS();

        botSelector = page.getByTestId('bot-selector-rhs');
        await expect(botSelector).toContainText('Assistant v2');

        // Restore original name
        await botConfig.updateBot(originalBot!.id, {
            displayName: 'Mock Bot'
        });
    });

    test.skip('should persist display name change across bot interactions', async ({ page }) => {
        const { aiPlugin } = await setupTestPage(page);
        const botConfig = await createBotConfigHelper(mattermost);

        // Get the original bot configuration
        const originalBot = await botConfig.getBotByName('mock');
        expect(originalBot).toBeDefined();

        // Change the display name
        await botConfig.updateBot(originalBot!.id, {
            displayName: 'Persistent Bot'
        });

        // Wait for configuration to propagate
        await page.waitForTimeout(1000);

        // Reload page to pick up configuration changes
        await page.reload();
        await page.waitForLoadState('domcontentloaded');

        // Open RHS and send a message
        await aiPlugin.openRHS();
        await expect(page.getByTestId('bot-selector-rhs')).toContainText('Persistent Bot');

        await openAIMock.addCompletionMock(responseTest);
        await aiPlugin.sendMessage('Test persistence');
        await aiPlugin.waitForBotResponse(responseTestText);

        // Display name should still be visible after interaction
        await expect(page.getByTestId('bot-selector-rhs')).toContainText('Persistent Bot');

        // Restore original name
        await botConfig.updateBot(originalBot!.id, {
            displayName: 'Mock Bot'
        });
    });
    });
}

createTestSuite();
