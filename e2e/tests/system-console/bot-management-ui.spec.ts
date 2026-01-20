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

        // Wait for the bots panel to be fully loaded
        await systemConsole.waitForBotsPanel();

        // The "Add an AI Agent" button should be visible when there are no bots
        const addBotButton = systemConsole.getAddBotButton();
        await expect(addBotButton).toBeVisible({ timeout: 10000 });

        // Count existing bot cards before adding a new one
        const existingBotCards = page.locator('[class*="BotContainer"]');
        const initialBotCount = await existingBotCards.count();

        // Click "Add an AI Agent" button - this creates a new collapsed bot card
        await addBotButton.click();

        // Wait for the new bot card to appear in the DOM
        await expect(existingBotCards).toHaveCount(initialBotCount + 1, { timeout: 5000 });

        // Get the newly added bot card (last one) and click on it to expand the form
        const botCard = page.locator('[class*="BotContainer"]').last();
        await expect(botCard).toBeVisible();

        // Click on the bot card to expand it
        await botCard.click();

        // Wait for the form fields to become visible after expansion
        // Display Name
        const displayNameInput = botCard.getByPlaceholder(/display name/i).or(botCard.getByRole('textbox').first());
        await expect(displayNameInput).toBeVisible({ timeout: 3000 });
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

        // Wait for the save operation to complete
        // The button might become disabled briefly or a success message might appear
        await page.waitForTimeout(2000);

        // Verify bot was saved - reload and check
        await page.reload();

        // Wait for the page to load and bots list to be visible
        const botsListSection = page.locator('[class*="BotsList"]');
        await expect(botsListSection).toBeVisible({ timeout: 10000 });

        // Verify bot appears with configured values in the bots list
        await expect(botsListSection.getByText('Test Assistant')).toBeVisible();

        // Verify the bot also appears in the default bot dropdown
        const defaultBotDropdown = page.getByText('Default bot').locator('..').getByRole('combobox');
        await defaultBotDropdown.scrollIntoViewIfNeeded();
        await expect(defaultBotDropdown).toContainText('Test Assistant');
    });
});
