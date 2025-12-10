// spec: system-console-additional-scenarios.plan.md - Bot Native Tools
// seed: e2e/tests/seed.spec.ts

import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { SystemConsoleHelper } from 'helpers/system-console';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';
import RunSystemConsoleContainer, { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Test Suite: Bot Native Tools
 *
 * Tests native tools configuration that is conditionally visible based on service type.
 * - Shows "Native Claude Tools" for Anthropic services
 * - Shows "Native OpenAI Tools" for OpenAI-based services with ResponsesAPI
 * - Hidden for other service types or OpenAI without ResponsesAPI
 * - Web Search checkbox with provider-specific help text
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.describe.serial('Bot Native Tools', () => {
    test('should show Native Claude Tools for Anthropic service', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with Anthropic service
        mattermost = await RunSystemConsoleContainer({
            services: [
                {
                    id: 'anthropic-service',
                    name: 'Anthropic Service',
                    type: 'anthropic',
                    apiKey: 'test-key-anthropic',
                    defaultModel: 'claude-3-opus',
                    tokenLimit: 16384,
                    outputTokenLimit: 8192,
                }
            ],
            bots: [
                {
                    id: 'bot-1',
                    name: 'testbot',
                    displayName: 'Test Bot',
                    serviceID: 'anthropic-service',
                    customInstructions: 'You are a helpful assistant',
                    enableVision: false,
                    enableTools: false,
                    enabledNativeTools: [],
                }
            ],
        });

        openAIMock = await RunOpenAIMocks(mattermost.network);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // 4. Login as sysadmin
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // 5. Navigate to system console
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // 6. Expand bot card in AI Bots panel
        const botCard = page.locator('[class*="BotContainer"]').first();
        await expect(botCard).toBeVisible();

        // 7. Click on the bot card to expand it
        await botCard.click();

        // 8. Scroll through bot configuration fields
        // 9. Locate 'Native Claude Tools' section
        const nativeToolsSection = botCard.getByText(/native claude tools/i);
        await expect(nativeToolsSection).toBeVisible();

        // 10. Verify section title shows 'Native Claude Tools'
        await expect(nativeToolsSection).toContainText('Native Claude Tools');

        // 11. Verify an 'EXPERIMENTAL' pill/badge appears next to the title
        const experimentalBadge = botCard.locator('text=EXPERIMENTAL');
        await expect(experimentalBadge).toBeVisible();

        // 12. Verify 'Web Search' checkbox is present
        // For Anthropic with native tools, Web Search checkbox is the first checkbox (vision/tools use radios)
        const webSearchCheckbox = botCard.getByRole('checkbox').first();
        await expect(webSearchCheckbox).toBeVisible();

        // 13. Verify help text reads 'Enable Claude\'s built-in web search capability'
        const helpText = botCard.locator('text=/.*claude.*web search capability.*/i');
        await expect(helpText).toBeVisible();

        // 14. The checkbox might be checked or unchecked based on config - toggle it to change state
        const initiallyChecked = await webSearchCheckbox.isChecked();

        // 15. Click the 'Web Search' checkbox to toggle it
        await webSearchCheckbox.click();

        // 16. Verify checkbox state changed
        if (initiallyChecked) {
            await expect(webSearchCheckbox).not.toBeChecked();
        } else {
            await expect(webSearchCheckbox).toBeChecked();
        }

        // 17. Click Save button
        const saveButton = systemConsole.getSaveButton();
        await saveButton.click();

        // Wait for save to complete
        await page.waitForTimeout(1000);

        // 18. Reload page
        await page.reload();

        // 19. Expand bot card
        const reloadedBotCard = page.locator('[class*="BotContainer"]').first();
        await reloadedBotCard.click();

        // 20. Verify 'Web Search' checkbox state persisted after reload
        const reloadedWebSearchCheckbox = reloadedBotCard.getByRole('checkbox').first();
        if (initiallyChecked) {
            await expect(reloadedWebSearchCheckbox).not.toBeChecked();
        } else {
            await expect(reloadedWebSearchCheckbox).toBeChecked();
        }

        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should show Native OpenAI Tools for OpenAI service with ResponsesAPI', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with OpenAI service that has useResponsesAPI set to true
        mattermost = await RunSystemConsoleContainer({
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
                    useResponsesAPI: true,
                }
            ],
            bots: [
                {
                    id: 'bot-1',
                    name: 'testbot',
                    displayName: 'Test Bot',
                    serviceID: 'openai-service',
                    customInstructions: 'You are a helpful assistant',
                    enableVision: false,
                    enableTools: false,
                    enabledNativeTools: ['web_search'],
                }
            ],
        });

        openAIMock = await RunOpenAIMocks(mattermost.network);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // 4. Login as sysadmin
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // 5. Navigate to system console
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // 6. Expand bot card
        const botCard = page.locator('[class*="BotContainer"]').first();
        await expect(botCard).toBeVisible();
        await botCard.click();

        // 7. Locate 'Native OpenAI Tools' section
        const nativeToolsSection = botCard.getByText(/native openai tools/i);
        await expect(nativeToolsSection).toBeVisible();

        // 8. Verify section title shows 'Native OpenAI Tools'
        await expect(nativeToolsSection).toContainText('Native OpenAI Tools');

        // 9. Verify 'EXPERIMENTAL' pill is present
        const experimentalBadge = botCard.locator('text=EXPERIMENTAL');
        await expect(experimentalBadge).toBeVisible();

        // 10. Verify 'Web Search' checkbox is present
        const webSearchCheckbox = botCard.getByRole('checkbox').first();
        await expect(webSearchCheckbox).toBeVisible();

        // 11. Verify help text reads 'Enable OpenAI\'s built-in web search capability'
        const helpText = botCard.locator('text=/.*openai.*web search capability.*/i');
        await expect(helpText).toBeVisible();

        // 12. Verify the checkbox is checked (since web_search is in enabled array)
        await expect(webSearchCheckbox).toBeChecked();

        // 13. Click the checkbox to uncheck it
        await webSearchCheckbox.click();

        // 14. Verify checkbox becomes unchecked
        await expect(webSearchCheckbox).not.toBeChecked();

        // 15. Click Save
        const saveButton = systemConsole.getSaveButton();
        await saveButton.click();

        // Wait for save to complete
        await page.waitForTimeout(1000);

        // 16. Reload page
        await page.reload();

        // 17. Expand bot card
        const reloadedBotCard = page.locator('[class*="BotContainer"]').first();
        await reloadedBotCard.click();

        // 18. Verify checkbox is unchecked
        const reloadedWebSearchCheckbox = reloadedBotCard.getByRole('checkbox').first();
        await expect(reloadedWebSearchCheckbox).not.toBeChecked();

        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should NOT show native tools for OpenAI service without ResponsesAPI', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with OpenAI service that has useResponsesAPI set to false
        mattermost = await RunSystemConsoleContainer({
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
                }
            ],
            bots: [
                {
                    id: 'bot-1',
                    name: 'testbot',
                    displayName: 'Test Bot',
                    serviceID: 'openai-service',
                    customInstructions: 'You are a helpful assistant',
                    enableVision: false,
                    enableTools: true,
                    enabledNativeTools: [],
                }
            ],
        });

        openAIMock = await RunOpenAIMocks(mattermost.network);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // 3. Login as sysadmin
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // 4. Navigate to system console
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // 5. Expand bot card
        const botCard = page.locator('[class*="BotContainer"]').first();
        await expect(botCard).toBeVisible();
        await botCard.click();

        // 6. Scroll through all bot configuration fields
        // 7. Verify 'Enable Vision' toggle is visible
        const enableVisionText = botCard.locator('text=Enable Vision').first();
        await expect(enableVisionText).toBeVisible();

        // 8. Verify 'Enable Tools' toggle is visible
        const enableToolsText = botCard.locator('text=Enable Tools').first();
        await expect(enableToolsText).toBeVisible();

        // 9. Verify 'Native OpenAI Tools' section is NOT present
        const nativeOpenAITools = botCard.getByText(/native openai tools/i);
        await expect(nativeOpenAITools).not.toBeVisible();

        // 10. Verify no 'Native Claude Tools' section is present
        const nativeClaudeTools = botCard.getByText(/native claude tools/i);
        await expect(nativeClaudeTools).not.toBeVisible();

        // 11. Verify 'Web Search' checkbox is NOT present (no checkboxes should be present)
        const checkboxes = botCard.getByRole('checkbox');
        await expect(checkboxes).toHaveCount(0);

        // 12. Change the service 'Use Responses API' toggle to true in services panel
        // Scroll up to find the services panel
        await page.evaluate(() => window.scrollTo(0, 0));

        // 13. Find and expand the service card in AI Services panel if it's not already expanded
        const serviceCard = page.locator('[class*="ServiceContainer"]').first();
        await expect(serviceCard).toBeVisible();

        // Check if "Use Responses API" text is visible - if not, the card is collapsed, so click to expand
        const useResponsesAPIText = page.locator('text=Use Responses API').first();
        const isExpanded = await useResponsesAPIText.isVisible().catch(() => false);
        if (!isExpanded) {
            await serviceCard.click();
            await expect(useResponsesAPIText).toBeVisible();
        }

        // 14. Find the True radio button for Use Responses API within the service card
        // The radios in the service card are: Send User ID (True/False), Use Responses API (True/False)
        // So the "Use Responses API - True" radio is the 3rd radio (index 2)
        const useResponsesAPITrue = serviceCard.getByRole('radio').nth(2);
        await expect(useResponsesAPITrue).toBeVisible();

        // 15. Enable 'Use Responses API' by clicking the True radio button
        await useResponsesAPITrue.click();
        await expect(useResponsesAPITrue).toBeChecked();

        // 16. Scroll back to the bot card to see the Native OpenAI Tools section
        // The bot configuration is reactive to service configuration changes
        await botCard.scrollIntoViewIfNeeded();

        // 17. Verify 'Native OpenAI Tools' section now appears in bot card (without needing save/reload)
        const nativeToolsAfterEnable = botCard.getByText(/native openai tools/i);
        await expect(nativeToolsAfterEnable).toBeVisible();

        // 16. Verify 'Web Search' checkbox is now visible
        const webSearchCheckbox = botCard.getByRole('checkbox').first();
        await expect(webSearchCheckbox).toBeVisible();

        // 17. Verify help text reads 'Enable OpenAI's built-in web search capability'
        const helpText = botCard.locator('text=/.*openai.*web search capability.*/i');
        await expect(helpText).toBeVisible();

        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should enable and disable Web Search for Anthropic service', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with Anthropic service
        mattermost = await RunSystemConsoleContainer({
            services: [
                {
                    id: 'anthropic-service',
                    name: 'Anthropic Service',
                    type: 'anthropic',
                    apiKey: 'test-key-anthropic',
                    defaultModel: 'claude-3-opus',
                    tokenLimit: 16384,
                    outputTokenLimit: 8192,
                }
            ],
            bots: [
                {
                    id: 'bot-1',
                    name: 'testbot',
                    displayName: 'Test Bot',
                    serviceID: 'anthropic-service',
                    customInstructions: 'You are a helpful assistant',
                    enableVision: false,
                    enableTools: false,
                    enabledNativeTools: [],
                }
            ],
        });

        openAIMock = await RunOpenAIMocks(mattermost.network);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // 2. Login as sysadmin
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // 3. Navigate to system console
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // 4. Expand bot card
        const botCard = page.locator('[class*="BotContainer"]').first();
        await expect(botCard).toBeVisible();
        await botCard.click();

        // 5. Locate 'Web Search' checkbox
        const webSearchCheckbox = botCard.getByRole('checkbox').first();
        await expect(webSearchCheckbox).toBeVisible();

        // 6. Verify checkbox is initially OFF
        await expect(webSearchCheckbox).not.toBeChecked();

        // 7. Enable checkbox
        await webSearchCheckbox.click();
        await expect(webSearchCheckbox).toBeChecked();

        // 8. Click Save
        const saveButton = systemConsole.getSaveButton();
        await saveButton.click();
        await page.waitForTimeout(1000);

        // 9. Reload and verify ON state persists
        await page.reload();
        await page.waitForLoadState('networkidle');
        const reloadedBotCard1 = page.locator('[class*="BotContainer"]').first();
        await reloadedBotCard1.click();
        await page.waitForTimeout(500);
        const reloadedCheckbox1 = reloadedBotCard1.getByRole('checkbox').first();
        await expect(reloadedCheckbox1).toBeChecked();

        // 10. Disable checkbox
        await reloadedCheckbox1.click();
        await expect(reloadedCheckbox1).not.toBeChecked();

        // 11. Save again
        await saveButton.click();
        await page.waitForTimeout(1000);

        // 12. Reload and verify OFF state persists
        await page.reload();
        await page.waitForLoadState('networkidle');
        const reloadedBotCard2 = page.locator('[class*="BotContainer"]').first();
        await reloadedBotCard2.click();
        await page.waitForTimeout(500);
        const reloadedCheckbox2 = reloadedBotCard2.getByRole('checkbox').first();
        await expect(reloadedCheckbox2).not.toBeChecked();

        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should enable and disable Web Search for OpenAI service with ResponsesAPI', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with OpenAI service with ResponsesAPI enabled
        mattermost = await RunSystemConsoleContainer({
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
                    useResponsesAPI: true,
                }
            ],
            bots: [
                {
                    id: 'bot-1',
                    name: 'testbot',
                    displayName: 'Test Bot',
                    serviceID: 'openai-service',
                    customInstructions: 'You are a helpful assistant',
                    enableVision: false,
                    enableTools: false,
                    enabledNativeTools: [],
                }
            ],
        });

        openAIMock = await RunOpenAIMocks(mattermost.network);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // 2. Login as sysadmin
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // 3. Navigate to system console
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // 4. Expand bot card
        const botCard = page.locator('[class*="BotContainer"]').first();
        await expect(botCard).toBeVisible();
        await botCard.click();

        // 5. Locate 'Web Search' checkbox
        const webSearchCheckbox = botCard.getByRole('checkbox').first();
        await expect(webSearchCheckbox).toBeVisible();

        // 6. Verify checkbox is initially OFF
        await expect(webSearchCheckbox).not.toBeChecked();

        // 7. Enable checkbox
        await webSearchCheckbox.click();
        await expect(webSearchCheckbox).toBeChecked();

        // 8. Click Save
        const saveButton = systemConsole.getSaveButton();
        await saveButton.click();
        await page.waitForTimeout(1000);

        // 9. Reload and verify ON state persists
        await page.reload();
        await page.waitForLoadState('networkidle');
        const reloadedBotCard1 = page.locator('[class*="BotContainer"]').first();
        await reloadedBotCard1.click();
        await page.waitForTimeout(500);
        const reloadedCheckbox1 = reloadedBotCard1.getByRole('checkbox').first();
        await expect(reloadedCheckbox1).toBeChecked();

        // 10. Disable checkbox
        await reloadedCheckbox1.click();
        await expect(reloadedCheckbox1).not.toBeChecked();

        // 11. Save again
        await saveButton.click();
        await page.waitForTimeout(1000);

        // 12. Reload and verify OFF state persists
        await page.reload();
        await page.waitForLoadState('networkidle');
        const reloadedBotCard2 = page.locator('[class*="BotContainer"]').first();
        await reloadedBotCard2.click();
        await page.waitForTimeout(500);
        const reloadedCheckbox2 = reloadedBotCard2.getByRole('checkbox').first();
        await expect(reloadedCheckbox2).not.toBeChecked();

        await openAIMock.stop();
        await mattermost.stop();
    });
});
