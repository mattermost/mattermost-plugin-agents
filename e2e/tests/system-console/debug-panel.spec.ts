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
 *   4-5:  Enable Channel Mention Tool Calling
 *   6-7:  Allow native web search in channels
 *   8-9:  Enable OpenTelemetry
 *   10-11: Enable Token Usage Logging
 *   12+:  Web Search, MCP settings...
 */
const RADIO_INDICES = {
    enableTokenUsageLogging: { true: 10, false: 11 },
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
});
