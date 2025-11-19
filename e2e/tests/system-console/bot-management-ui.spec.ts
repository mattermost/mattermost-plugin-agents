// spec: Bot Management UI - should add first bot through UI with service selection
// seed: e2e/tests/seed.spec.ts

import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { SystemConsoleHelper } from 'helpers/system-console';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';
import RunSystemConsoleContainer, { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Test Suite: Bot Management UI
 *
 * Tests UI-based bot management operations in the system console.
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.describe('Bot Management UI', () => {
    test.beforeAll(async () => {
        // Start container with one pre-configured service but no bots
        mattermost = await RunSystemConsoleContainer({
            services: [
                {
                    id: 'test-service-id',
                    name: 'Test Service',
                    type: 'openaicompatible',
                    apiURL: 'http://openai-mock:11434',
                    apiKey: 'test-key',
                    orgId: '',
                    defaultModel: 'gpt-4',
                    tokenLimit: 16384,
                    streamingTimeoutSeconds: 30,
                    sendUserId: false,
                    outputTokenLimit: 4096,
                    useResponsesAPI: false,
                }
            ],
            bots: [],
        });

        openAIMock = await RunOpenAIMocks(mattermost.network);
    });

    test.afterAll(async () => {
        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should add and configure bot through card-based UI', async ({ page }) => {
        test.setTimeout(60000);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // Login as admin
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // Navigate to system console
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // Count existing bot cards before adding a new one
        const existingBotCards = page.locator('[class*="BotContainer"]');
        const initialBotCount = await existingBotCards.count();

        // Click "Add an AI Agent" button - this creates a new collapsed bot card
        const addBotButton = systemConsole.getAddBotButton();
        await expect(addBotButton).toBeVisible();
        await addBotButton.click();
        await page.waitForTimeout(1000);

        // Verify a new bot card was added
        await expect(existingBotCards).toHaveCount(initialBotCount + 1);

        // Get the newly added bot card (last one) and click on it to expand the form
        const botCard = page.locator('[class*="BotContainer"]').last();
        await expect(botCard).toBeVisible();

        // Click on the bot card to expand it
        await botCard.click();
        await page.waitForTimeout(500);

        // Fill in the form fields that are now visible
        // Display Name
        const displayNameInput = botCard.getByPlaceholder(/display name/i).or(botCard.getByRole('textbox').first());
        await displayNameInput.fill('Test Assistant');

        // Agent Username
        const usernameInput = botCard.getByPlaceholder(/username/i).or(botCard.getByRole('textbox').nth(1));
        await usernameInput.fill('testassistant');

        // Select AI Service - use the Test Service that was configured
        const serviceSelect = botCard.getByRole('combobox');
        await serviceSelect.selectOption({ label: 'Test Service' });

        // Custom Instructions
        const customInstructionsInput = botCard.getByPlaceholder(/how would you like/i).or(botCard.getByRole('textbox').last());
        await customInstructionsInput.fill('You are a helpful assistant');

        // Click main Save button at bottom of page
        const saveButton = systemConsole.getSaveButton();
        await expect(saveButton).toBeVisible();
        await saveButton.click();

        // Wait for save to complete
        await page.waitForTimeout(2000);

        // Verify bot was saved - reload and check
        await page.reload();
        await page.waitForTimeout(1000);

        // Verify bot appears with configured values in the bots list
        const botsListSection = page.locator('[class*="BotsList"]');
        await expect(botsListSection.getByText('Test Assistant')).toBeVisible();

        // Verify the bot also appears in the default agent dropdown
        const defaultAgentDropdown = page.getByLabel(/default agent/i).or(page.locator('text=Default agent').locator('..').getByRole('combobox'));
        await expect(defaultAgentDropdown).toContainText('Test Assistant');
    });
});
