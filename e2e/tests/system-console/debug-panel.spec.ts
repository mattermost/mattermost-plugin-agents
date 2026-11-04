// spec: system-console-additional-scenarios.plan.md - Debug Panel
// seed: e2e/tests/seed.spec.ts

import { expect, Page } from '@playwright/test';
import { containerTest } from 'helpers/container-fixture';
import { MattermostPage } from 'helpers/mm';
import { SystemConsoleHelper } from 'helpers/system-console';
import { RunOpenAIMocks } from 'helpers/openai-mock';
import RunSystemConsoleContainer, { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Test Suite: Debug Panel
 *
 * Tests configuration options in the Debug panel of the system console.
 */

/**
 * Radio buttons of a Debug panel boolean setting, found by the setting's label
 * so they do not depend on how many other settings render before them.
 */
function getSettingRadios(page: Page, label: string) {
    const group = page.getByRole('radiogroup', { name: label });
    return {
        true: group.getByRole('radio', { name: 'True' }),
        false: group.getByRole('radio', { name: 'False' }),
    };
}

const tokenUsageLoggingLabel = 'Enable Token Usage Logging';

const test = containerTest(async (own) => {
    // Start container with enableTokenUsageLogging set to false
    const mattermost = own(await RunSystemConsoleContainer({
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
    }));
    own(await RunOpenAIMocks(mattermost.network));
    return { mattermost };
});

test.describe.serial('Debug Panel', () => {
    test('should toggle Enable Token Usage Logging', async ({ page, containers: { mattermost } }) => {
        test.setTimeout(60000);

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
        const tokenLogging = getSettingRadios(page, tokenUsageLoggingLabel);

        // Verify the toggle is currently OFF
        await expect(tokenLogging.false).toBeChecked();

        // Click the "true" radio to enable it
        await tokenLogging.true.click();

        // Verify the toggle changes to ON state
        await expect(tokenLogging.true).toBeChecked();

        // Click Save button
        await systemConsole.clickSave();

        // Reload the page
        await page.reload();

        // Verify 'Enable Token Usage Logging' toggle is ON after reload
        const reloadedTokenLogging = getSettingRadios(page, tokenUsageLoggingLabel);
        await expect(reloadedTokenLogging.true).toBeChecked();

        // Toggle it OFF
        await reloadedTokenLogging.false.click();

        // Verify the toggle changes to OFF state
        await expect(reloadedTokenLogging.false).toBeChecked();

        // Save the change; reloading before the PUT lands would read the old value
        await systemConsole.clickSave();

        // Reload and verify it's OFF
        await page.reload();

        const finalTokenLogging = getSettingRadios(page, tokenUsageLoggingLabel);
        await expect(finalTokenLogging.false).toBeChecked();
    });
});
