// spec: system-console-additional-scenarios.plan.md - Debug Panel
// seed: e2e/tests/seed.spec.ts

import { test, expect, Page } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { SystemConsoleHelper } from 'helpers/system-console';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';
import RunSystemConsoleContainer, { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Test Suite: Debug Panel
 *
 * Tests configuration options in the Debug panel of the system console.
 */

/**
 * Radio button indices on the system console page.
 * Each BooleanItem setting renders two radio buttons (true at index 0, false at index 1).
 * Specific accessors were not reliable enough to use, this approach is much more consistent.
 *
 * UPDATE THESE if the page structure changes (e.g., settings added/removed/reordered):
 *
 * Current order of radio button pairs on the page:
 *   0-1:  Plugin Enable (Mattermost built-in)
 *   2-3:  Render AI-generated links
 *   4-5:  Allow native web search in channels
 *   6-7:  Enable LLM Trace
 *   8-9:  Enable Token Usage Logging
 *   10+:  Web Search, MCP settings...
 */
const RADIO_INDICES = {
    enableLLMTrace: { true: 6, false: 7 },
    enableTokenUsageLogging: { true: 8, false: 9 },
} as const;

/**
 * Helper to get radio buttons for a debug panel setting.
 */
function getSettingRadios(page: Page, setting: keyof typeof RADIO_INDICES) {
    const indices = RADIO_INDICES[setting];
    return {
        true: page.getByRole('radio').nth(indices.true),
        false: page.getByRole('radio').nth(indices.false),
    };
}

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.describe.serial('Debug Panel', () => {
    test('should toggle Enable LLM Trace', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with enableLLMTrace set to false
        mattermost = await RunSystemConsoleContainer({
            enableLLMTrace: false,
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

        // Scroll down to locate the Debug panel
        const debugPanel = systemConsole.getDebugPanel();
        await debugPanel.scrollIntoViewIfNeeded();

        // Verify the Debug panel title is visible
        await expect(debugPanel).toBeVisible();

        // Locate the 'Enable LLM Trace' radio buttons
        const llmTrace = getSettingRadios(page, 'enableLLMTrace');

        // Verify the toggle is currently OFF (false radio is checked)
        await expect(llmTrace.false).toBeChecked();

        // Click the "true" radio to enable it
        await llmTrace.true.click();

        // Verify the toggle changes to ON state
        await expect(llmTrace.true).toBeChecked();

        // Click Save button at bottom of page
        const saveButton = systemConsole.getSaveButton();
        await saveButton.click();

        // Wait for save to complete
        await page.waitForTimeout(1000);

        // Reload the page
        await page.reload();

        // Scroll to Debug panel
        const reloadedDebugPanel = systemConsole.getDebugPanel();
        await reloadedDebugPanel.scrollIntoViewIfNeeded();

        // Verify 'Enable LLM Trace' toggle is still ON (true radio is checked)
        const reloadedLlmTrace = getSettingRadios(page, 'enableLLMTrace');
        await expect(reloadedLlmTrace.true).toBeChecked();

        // Click false radio to disable
        await reloadedLlmTrace.false.click();

        // Verify the toggle changes to OFF state
        await expect(reloadedLlmTrace.false).toBeChecked();

        // Click Save
        await saveButton.click();

        // Reload page
        await page.reload();

        // Verify toggle is OFF (false radio is checked)
        const finalLlmTrace = getSettingRadios(page, 'enableLLMTrace');
        await expect(finalLlmTrace.false).toBeChecked();

        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should toggle Enable Token Usage Logging', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with enableTokenUsageLogging set to false
        mattermost = await RunSystemConsoleContainer({
            enableTokenUsageLogging: false,
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

        // Scroll to the Debug panel
        const debugPanel = systemConsole.getDebugPanel();
        await debugPanel.scrollIntoViewIfNeeded();

        // Locate the 'Enable Token Usage Logging' radio buttons
        const tokenLogging = getSettingRadios(page, 'enableTokenUsageLogging');

        // Verify the toggle is currently OFF
        await expect(tokenLogging.false).toBeChecked();

        // Click the "true" radio to enable it
        await tokenLogging.true.click();

        // Verify the toggle changes to ON state
        await expect(tokenLogging.true).toBeChecked();

        // Click Save button
        const saveButton = systemConsole.getSaveButton();
        await saveButton.click();

        // Wait for save to complete
        await page.waitForTimeout(1000);

        // Reload the page
        await page.reload();

        // Verify 'Enable Token Usage Logging' toggle is ON after reload
        const reloadedTokenLogging = getSettingRadios(page, 'enableTokenUsageLogging');
        await expect(reloadedTokenLogging.true).toBeChecked();

        // Toggle it OFF
        await reloadedTokenLogging.false.click();

        // Verify the toggle changes to OFF state
        await expect(reloadedTokenLogging.false).toBeChecked();

        // Save the change
        await saveButton.click();

        // Reload and verify it's OFF
        await page.reload();

        const finalTokenLogging = getSettingRadios(page, 'enableTokenUsageLogging');
        await expect(finalTokenLogging.false).toBeChecked();

        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should configure both debug toggles independently', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with both enableLLMTrace and enableTokenUsageLogging set to false
        mattermost = await RunSystemConsoleContainer({
            enableLLMTrace: false,
            enableTokenUsageLogging: false,
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

        // Login as sysadmin
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // Navigate to system console
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // Scroll to Debug panel
        const debugPanel = systemConsole.getDebugPanel();
        await debugPanel.scrollIntoViewIfNeeded();

        // Get radio buttons for both debug settings
        const llmTrace = getSettingRadios(page, 'enableLLMTrace');
        const tokenLogging = getSettingRadios(page, 'enableTokenUsageLogging');

        // Verify both toggles are OFF
        await expect(llmTrace.false).toBeChecked();
        await expect(tokenLogging.false).toBeChecked();

        // Enable only 'Enable LLM Trace', leave 'Enable Token Usage Logging' OFF
        await llmTrace.true.click();
        await expect(llmTrace.true).toBeChecked();
        await expect(tokenLogging.false).toBeChecked();

        // Click Save
        const saveButton = systemConsole.getSaveButton();
        await saveButton.click();
        await page.waitForTimeout(1000);

        // Reload page
        await page.reload();

        // Verify 'Enable LLM Trace' is ON and 'Enable Token Usage Logging' is OFF
        const reloadedLlmTrace = getSettingRadios(page, 'enableLLMTrace');
        const reloadedTokenLogging = getSettingRadios(page, 'enableTokenUsageLogging');

        await expect(reloadedLlmTrace.true).toBeChecked();
        await expect(reloadedTokenLogging.false).toBeChecked();

        // Now enable 'Enable Token Usage Logging' while keeping 'Enable LLM Trace' ON
        await reloadedTokenLogging.true.click();
        await expect(reloadedLlmTrace.true).toBeChecked();
        await expect(reloadedTokenLogging.true).toBeChecked();

        // Click Save
        await saveButton.click();
        await page.waitForTimeout(1000);

        // Reload page
        await page.reload();

        // Verify both toggles are ON
        const bothOnLlmTrace = getSettingRadios(page, 'enableLLMTrace');
        const bothOnTokenLogging = getSettingRadios(page, 'enableTokenUsageLogging');

        await expect(bothOnLlmTrace.true).toBeChecked();
        await expect(bothOnTokenLogging.true).toBeChecked();

        // Disable 'Enable LLM Trace', keep 'Enable Token Usage Logging' ON
        await bothOnLlmTrace.false.click();
        await expect(bothOnLlmTrace.false).toBeChecked();
        await expect(bothOnTokenLogging.true).toBeChecked();

        // Click Save
        await saveButton.click();
        await page.waitForTimeout(1000);

        // Reload page
        await page.reload();

        // Verify 'Enable LLM Trace' is OFF and 'Enable Token Usage Logging' is ON
        const finalLlmTrace = getSettingRadios(page, 'enableLLMTrace');
        const finalTokenLogging = getSettingRadios(page, 'enableTokenUsageLogging');

        await expect(finalLlmTrace.false).toBeChecked();
        await expect(finalTokenLogging.true).toBeChecked();

        await openAIMock.stop();
        await mattermost.stop();
    });
});
