// spec: system-console-additional-scenarios.plan.md - AI Functions Panel
// seed: e2e/tests/seed.spec.ts

import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { SystemConsoleHelper } from 'helpers/system-console';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';
import RunSystemConsoleContainer, { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Test Suite: AI Functions Panel
 *
 * Tests configuration options in the AI Functions panel of the system console.
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.describe.serial('AI Functions Panel', () => {
    test('should configure default bot dropdown', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with two pre-configured services and three bots
        mattermost = await RunSystemConsoleContainer({
            defaultBotName: 'primaryassistant',
            services: [
                {
                    id: 'openai-service',
                    name: 'OpenAI Service',
                    type: 'openai',
                    apiKey: 'test-key-openai',
                    orgId: '',
                    defaultModel: 'gpt-4',
                    tokenLimit: 16384,
                    streamingTimeoutSeconds: 30,
                    sendUserId: false,
                    outputTokenLimit: 4096,
                    useResponsesAPI: false,
                },
                {
                    id: 'anthropic-service',
                    name: 'Anthropic Service',
                    type: 'anthropic',
                    apiKey: 'test-key-anthropic',
                    defaultModel: 'claude-3-opus',
                    tokenLimit: 16384,
                    outputTokenLimit: 4096,
                }
            ],
            bots: [
                {
                    id: 'bot-1',
                    name: 'primaryassistant',
                    displayName: 'Primary Assistant',
                    serviceID: 'openai-service',
                    customInstructions: 'You are a helpful assistant',
                    enableVision: false,
                    enableTools: false,
                },
                {
                    id: 'bot-2',
                    name: 'secondarybot',
                    displayName: 'Secondary Bot',
                    serviceID: 'anthropic-service',
                    customInstructions: 'You are a secondary bot',
                    enableVision: false,
                    enableTools: false,
                },
                {
                    id: 'bot-3',
                    name: 'testagent',
                    displayName: 'Test Agent',
                    serviceID: 'openai-service',
                    customInstructions: 'You are a test agent',
                    enableVision: false,
                    enableTools: false,
                }
            ],
        });

        openAIMock = await RunOpenAIMocks(mattermost.network);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // Login as sysadmin user
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // Navigate to system console AI plugin configuration page
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // Scroll to the AI Functions panel
        const functionsPanel = systemConsole.getFunctionsPanel();
        await functionsPanel.scrollIntoViewIfNeeded();

        // Locate the 'Default agent' dropdown field by looking for combobox near "Default agent" text
        // This is the first combobox on the page (in the AI Functions section)
        const defaultBotDropdown = page.getByRole('combobox').first();

        // Verify the dropdown is visible and enabled
        await expect(defaultBotDropdown).toBeVisible();
        await expect(defaultBotDropdown).toBeEnabled();

        // Verify the dropdown shows 'Primary Assistant' as the current selection
        await expect(defaultBotDropdown).toHaveValue(/primaryassistant|Primary Assistant/i);

        // Select 'Secondary Bot' from the dropdown
        await defaultBotDropdown.selectOption({ label: 'Secondary Bot' });

        // Click the Save button at the bottom of the page
        const saveButton = systemConsole.getSaveButton();
        await saveButton.click();

        // Wait for save operation to complete
        await page.waitForTimeout(1000);

        // Reload the page
        await page.reload();

        // Verify the 'Default agent' dropdown now shows 'Secondary Bot' as selected
        const reloadedDropdown = page.getByRole('combobox').first();
        await expect(reloadedDropdown).toHaveValue(/secondarybot|Secondary Bot/i);

        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should configure allowed upstream hostnames', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with one service and one bot
        mattermost = await RunSystemConsoleContainer({
            allowedUpstreamHostnames: '',
            services: [
                {
                    id: 'test-service',
                    name: 'Test Service',
                    type: 'openai',
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
            bots: [
                {
                    id: 'bot-1',
                    name: 'testbot',
                    displayName: 'Test Bot',
                    serviceID: 'test-service',
                    customInstructions: 'You are a helpful assistant',
                    enableVision: false,
                    enableTools: false,
                }
            ],
        });

        openAIMock = await RunOpenAIMocks(mattermost.network);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // Login as sysadmin user
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // Navigate to system console AI plugin configuration page
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // Scroll to the AI Functions panel
        const functionsPanel = systemConsole.getFunctionsPanel();
        await functionsPanel.scrollIntoViewIfNeeded();

        // Locate the 'Allowed Upstream Hostnames (csv)' text input field
        const hostnamesField = page.getByLabel(/allowed upstream hostnames/i).or(
            page.locator('text=Allowed Upstream Hostnames').locator('..').getByRole('textbox')
        );

        // Verify the field is visible
        await expect(hostnamesField).toBeVisible();

        // Verify the field is currently empty
        await expect(hostnamesField).toHaveValue('');

        // Click on the text field to focus it
        await hostnamesField.click();

        // Type hostnames into the field
        await hostnamesField.fill('api.example.com, *.mydomain.com, mattermost.atlassian.net');

        // Verify the text appears in the field as typed
        await expect(hostnamesField).toHaveValue('api.example.com, *.mydomain.com, mattermost.atlassian.net');

        // Click the Save button
        const saveButton = systemConsole.getSaveButton();
        await saveButton.click();

        // Wait for save to complete
        await page.waitForTimeout(1000);

        // Reload the page
        await page.reload();

        // Verify the field contains the entered hostnames
        const reloadedField = page.getByLabel(/allowed upstream hostnames/i).or(
            page.locator('text=Allowed Upstream Hostnames').locator('..').getByRole('textbox')
        );
        await expect(reloadedField).toHaveValue('api.example.com, *.mydomain.com, mattermost.atlassian.net');

        // Clear the field completely
        await reloadedField.click();
        await reloadedField.fill('');

        // Click Save button again
        await saveButton.click();

        // Reload the page
        await page.reload();

        // Verify the field is empty after clearing and saving
        const finalField = page.getByLabel(/allowed upstream hostnames/i).or(
            page.locator('text=Allowed Upstream Hostnames').locator('..').getByRole('textbox')
        );
        await expect(finalField).toHaveValue('');

        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should toggle render AI-generated links', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with allowUnsafeLinks set to false
        mattermost = await RunSystemConsoleContainer({
            allowUnsafeLinks: false,
            services: [
                {
                    id: 'test-service',
                    name: 'Test Service',
                    type: 'openai',
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
            bots: [
                {
                    id: 'bot-1',
                    name: 'testbot',
                    displayName: 'Test Bot',
                    serviceID: 'test-service',
                    customInstructions: 'You are a helpful assistant',
                    enableVision: false,
                    enableTools: false,
                }
            ],
        });

        openAIMock = await RunOpenAIMocks(mattermost.network);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // Login as sysadmin user
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // Navigate to system console AI plugin configuration page
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // Scroll to the AI Functions panel
        const functionsPanel = systemConsole.getFunctionsPanel();
        await functionsPanel.scrollIntoViewIfNeeded();

        // Locate the 'Render AI-generated links' toggle/checkbox
        const renderLinksToggle = page.getByLabel(/render ai-generated links/i).or(
            page.locator('text=Render AI-generated links').locator('..').getByRole('checkbox')
        );

        // Verify the toggle is visible
        await expect(renderLinksToggle).toBeVisible();

        // Verify the toggle is currently OFF (unchecked)
        await expect(renderLinksToggle).not.toBeChecked();

        // Click on the toggle to enable it
        await renderLinksToggle.click();

        // Verify the toggle changes to ON (checked) state
        await expect(renderLinksToggle).toBeChecked();

        // Click the Save button
        const saveButton = systemConsole.getSaveButton();
        await saveButton.click();

        // Wait for save to complete
        await page.waitForTimeout(1000);

        // Reload the page
        await page.reload();

        // Verify the toggle is still ON after reload
        const reloadedToggle = page.getByLabel(/render ai-generated links/i).or(
            page.locator('text=Render AI-generated links').locator('..').getByRole('checkbox')
        );
        await expect(reloadedToggle).toBeChecked();

        // Click the toggle again to disable it
        await reloadedToggle.click();

        // Verify the toggle changes to OFF (unchecked) state
        await expect(reloadedToggle).not.toBeChecked();

        // Click Save button
        await saveButton.click();

        // Reload the page
        await page.reload();

        // Verify the toggle is OFF after reload
        const finalToggle = page.getByLabel(/render ai-generated links/i).or(
            page.locator('text=Render AI-generated links').locator('..').getByRole('checkbox')
        );
        await expect(finalToggle).not.toBeChecked();

        await openAIMock.stop();
        await mattermost.stop();
    });
});
